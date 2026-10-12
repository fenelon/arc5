package connector

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/fenelon/arc5/internal/archive"
	"github.com/fenelon/arc5/internal/model"
)

type RunState string

const (
	StateRunning    RunState = "running"     // Run is executing; see Progress for what it is doing
	StateBackoff    RunState = "backoff"     // failed; waiting to retry
	StatePaused     RunState = "paused"      // the archive could not be written; waiting to retry
	StateNeedsLogin RunState = "needs_login" // will not retry until the owner logs in
	StateStopped    RunState = "stopped"
)

// Status is the runner's view of one account's connector.
type Status struct {
	AccountID int64
	Connector string
	Label     string
	State     RunState
	Since     time.Time
	LastError string
	Progress  Progress
}

const (
	maxBackoff = 5 * time.Minute
	// A run that lasted this long counts as healthy and resets the backoff.
	healthyRun = time.Minute
	// Room for requests made while a connector is busy.
	requestBuffer = 256
)

// DefaultBackoff waits one second after the first failure and doubles up to five minutes.
func DefaultBackoff(attempt int) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	if attempt > 9 { // 1s << 9 is already past the cap
		return maxBackoff
	}
	return time.Second << (attempt - 1)
}

var errUnknownConnector = errors.New("no such connector")

// Runner supervises one connector per enabled account.
type Runner struct {
	store *archive.Store
	reg   *Registry
	// Backoff returns how long to wait before retry number attempt (from 1).
	Backoff func(attempt int) time.Duration
	now     func() time.Time

	mu       sync.Mutex
	ctx      context.Context
	accounts map[int64]*supervised
}

type supervised struct {
	cancel   context.CancelFunc
	done     chan struct{}
	status   Status
	requests chan model.AttachmentRef
}

func NewRunner(store *archive.Store, reg *Registry) *Runner {
	return &Runner{
		store:    store,
		reg:      reg,
		Backoff:  DefaultBackoff,
		now:      time.Now,
		ctx:      context.Background(),
		accounts: map[int64]*supervised{},
	}
}

// Start launches a connector for every enabled account. Connectors stop when
// ctx is cancelled; call Wait afterwards to let them finish.
func (r *Runner) Start(ctx context.Context) error {
	accounts, err := r.store.Accounts()
	if err != nil {
		return err
	}
	r.mu.Lock()
	r.ctx = ctx
	r.mu.Unlock()
	for _, acc := range accounts {
		if acc.Enabled {
			r.launch(acc)
		}
	}
	return nil
}

func (r *Runner) launch(acc archive.Account) {
	r.mu.Lock()
	ctx, cancel := context.WithCancel(r.ctx)
	s := &supervised{
		cancel: cancel,
		done:   make(chan struct{}),
		status: Status{AccountID: acc.ID, Connector: acc.Connector, Label: acc.Label, State: StateStopped, Since: r.now()},
	}
	r.accounts[acc.ID] = s
	r.mu.Unlock()
	go r.supervise(ctx, acc, s)
}

// Restart stops an account's connector, reloads the account, and starts it
// again if it is enabled. Call it after changing an account's settings or a
// chat's archive toggle; checkpoints make the restart lossless.
func (r *Runner) Restart(accountID int64) error {
	r.mu.Lock()
	s := r.accounts[accountID]
	delete(r.accounts, accountID)
	r.mu.Unlock()
	if s != nil {
		s.cancel()
		<-s.done
	}
	acc, err := r.store.Account(accountID)
	if err != nil {
		return err
	}
	if acc.Enabled {
		r.launch(acc)
	}
	return nil
}

// Wait blocks until every supervised connector has stopped.
func (r *Runner) Wait() {
	r.mu.Lock()
	all := make([]*supervised, 0, len(r.accounts))
	for _, s := range r.accounts {
		all = append(all, s)
	}
	r.mu.Unlock()
	for _, s := range all {
		<-s.done
	}
}

func (r *Runner) Status() []Status {
	r.mu.Lock()
	out := make([]Status, 0, len(r.accounts))
	for _, s := range r.accounts {
		out = append(out, s.status)
	}
	r.mu.Unlock()
	sort.Slice(out, func(i, j int) bool { return out[i].AccountID < out[j].AccountID })
	return out
}

// RequestMedia asks for an attachment to be downloaded regardless of the size
// cap. The request is stored first, so it is delivered when the connector next
// starts if it cannot be handed over now.
func (r *Runner) RequestMedia(attachmentID int64) error {
	accountID, ref, err := r.store.RequestMedia(attachmentID)
	if err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if s := r.accounts[accountID]; s != nil && s.requests != nil {
		select {
		case s.requests <- ref:
		default: // full; the stored request is picked up on the next start
		}
	}
	return nil
}

func (r *Runner) update(s *supervised, change func(*Status)) {
	r.mu.Lock()
	change(&s.status)
	r.mu.Unlock()
}

func (r *Runner) setState(s *supervised, state RunState, lastError string) {
	now := r.now()
	r.update(s, func(st *Status) {
		st.State, st.Since, st.LastError = state, now, lastError
	})
}

func (r *Runner) supervise(ctx context.Context, acc archive.Account, s *supervised) {
	defer close(s.done)
	attempt := 0
	for {
		started := r.now()
		err := r.runOnce(ctx, acc, s)
		switch {
		case ctx.Err() != nil:
			r.setState(s, StateStopped, "")
			return
		case errors.Is(err, errUnknownConnector):
			r.setState(s, StateStopped, err.Error())
			return
		case errors.Is(err, ErrNeedsLogin):
			r.setState(s, StateNeedsLogin, err.Error())
			return
		}

		next := StateBackoff
		if errors.Is(err, ErrStorage) {
			next = StatePaused
		}
		if r.now().Sub(started) >= healthyRun {
			attempt = 0
		}
		attempt++
		r.setState(s, next, err.Error())

		timer := time.NewTimer(r.Backoff(attempt))
		select {
		case <-ctx.Done():
			timer.Stop()
			r.setState(s, StateStopped, "")
			return
		case <-timer.C:
		}
	}
}

// runOnce runs the connector until it returns. It always returns a non-nil
// error unless ctx was cancelled, and turns a panic into an error.
func (r *Runner) runOnce(ctx context.Context, acc archive.Account, s *supervised) (err error) {
	c, ok := r.reg.New(acc.Connector)
	if !ok {
		return fmt.Errorf("%w: %q", errUnknownConnector, acc.Connector)
	}

	pending, err := r.store.RequestedMedia(acc.ID)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrStorage, err)
	}
	requests := make(chan model.AttachmentRef, len(pending)+requestBuffer)
	for _, ref := range pending {
		requests <- ref
	}
	r.mu.Lock()
	s.requests = requests
	r.mu.Unlock()

	sink := newSink(r.store, acc.ID, requests, func(p Progress) {
		r.update(s, func(st *Status) { st.Progress = p })
	})
	now := r.now()
	r.update(s, func(st *Status) {
		st.State, st.Since, st.LastError, st.Progress = StateRunning, now, "", Progress{}
	})

	defer func() {
		if p := recover(); p != nil {
			err = fmt.Errorf("connector panicked: %v", p)
		}
	}()
	err = c.Run(ctx, sink, accountState{store: r.store, accountID: acc.ID}, acc.Settings)
	if err == nil {
		err = errors.New("connector stopped without an error")
	}
	return err
}
