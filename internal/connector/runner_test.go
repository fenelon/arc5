package connector

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/fenelon/arc5/internal/archive"
	"github.com/fenelon/arc5/internal/model"
)

type runFunc = func(ctx context.Context, sink Sink, state State, settings map[string]string) error

// newTestRunner returns a runner with one enabled "stub" account whose
// connector behaves as run does. The runner is not started.
func newTestRunner(t *testing.T, run runFunc) (*Runner, *archive.Store, int64, context.Context) {
	t.Helper()
	store := openStore(t)
	acc := newAccount(t, store)
	reg := NewRegistry()
	reg.Register(stubInfo("stub"), func() Connector { return stubConnector{run: run} })
	r := NewRunner(store, reg)
	r.Backoff = func(int) time.Duration { return time.Millisecond }
	r.Logf = func(string, ...any) {}
	ctx, cancel := context.WithCancel(context.Background())
	// Registered after openStore's cleanup, so it runs first: stop connectors, then close the store.
	t.Cleanup(func() { cancel(); r.Wait() })
	return r, store, acc, ctx
}

func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func statusOf(r *Runner, accountID int64) Status {
	for _, s := range r.Status() {
		if s.AccountID == accountID {
			return s
		}
	}
	return Status{}
}

func blockUntilCancelled(ctx context.Context) error {
	<-ctx.Done()
	return ctx.Err()
}

func TestRunnerRestartsAfterError(t *testing.T) {
	var calls atomic.Int32
	r, _, acc, ctx := newTestRunner(t, func(ctx context.Context, _ Sink, _ State, _ map[string]string) error {
		if calls.Add(1) < 3 {
			return errors.New("boom")
		}
		return blockUntilCancelled(ctx)
	})
	if err := r.Start(ctx); err != nil {
		t.Fatal(err)
	}
	eventually(t, "the third run", func() bool {
		return calls.Load() == 3 && statusOf(r, acc).State == StateRunning
	})
	if st := statusOf(r, acc); st.LastError != "" || st.Connector != "stub" || st.Label != "Test" {
		t.Fatalf("status = %+v", st)
	}
	time.Sleep(20 * time.Millisecond)
	if n := calls.Load(); n != 3 {
		t.Fatalf("a healthy connector was restarted: %d runs", n)
	}
}

// Review Focus 5: a panicking connector must not take the process down.
func TestRunnerRecoversFromPanic(t *testing.T) {
	var calls atomic.Int32
	r, _, acc, ctx := newTestRunner(t, func(ctx context.Context, _ Sink, _ State, _ map[string]string) error {
		if calls.Add(1) == 1 {
			panic("nil map write")
		}
		return blockUntilCancelled(ctx)
	})
	if err := r.Start(ctx); err != nil {
		t.Fatal(err)
	}
	eventually(t, "a restart after the panic", func() bool {
		return calls.Load() == 2 && statusOf(r, acc).State == StateRunning
	})
}

func TestRunnerReportsPanicAsError(t *testing.T) {
	r, _, acc, ctx := newTestRunner(t, func(context.Context, Sink, State, map[string]string) error {
		panic("nil map write")
	})
	r.Backoff = func(int) time.Duration { return time.Hour }
	if err := r.Start(ctx); err != nil {
		t.Fatal(err)
	}
	eventually(t, "backoff state", func() bool { return statusOf(r, acc).State == StateBackoff })
	if msg := statusOf(r, acc).LastError; !strings.Contains(msg, "panicked") || !strings.Contains(msg, "nil map write") {
		t.Fatalf("last error = %q", msg)
	}
}

func TestRunnerStopsRetryingWhenLoginIsNeeded(t *testing.T) {
	var calls atomic.Int32
	r, _, acc, ctx := newTestRunner(t, func(context.Context, Sink, State, map[string]string) error {
		calls.Add(1)
		return fmt.Errorf("session revoked: %w", ErrNeedsLogin)
	})
	if err := r.Start(ctx); err != nil {
		t.Fatal(err)
	}
	eventually(t, "needs-login state", func() bool { return statusOf(r, acc).State == StateNeedsLogin })
	time.Sleep(20 * time.Millisecond)
	if n := calls.Load(); n != 1 {
		t.Fatalf("runs = %d, want 1", n)
	}
}

func TestRunnerPausesOnStorageError(t *testing.T) {
	r, _, acc, ctx := newTestRunner(t, func(context.Context, Sink, State, map[string]string) error {
		return fmt.Errorf("%w: disk full", ErrStorage)
	})
	r.Backoff = func(int) time.Duration { return time.Hour }
	if err := r.Start(ctx); err != nil {
		t.Fatal(err)
	}
	eventually(t, "paused state", func() bool { return statusOf(r, acc).State == StatePaused })
	if msg := statusOf(r, acc).LastError; !strings.Contains(msg, "disk full") {
		t.Fatalf("last error = %q", msg)
	}
}

func TestRunnerTreatsCleanExitAsFailure(t *testing.T) {
	var calls atomic.Int32
	r, _, _, ctx := newTestRunner(t, func(ctx context.Context, _ Sink, _ State, _ map[string]string) error {
		if calls.Add(1) == 1 {
			return nil // a connector must not stop on its own
		}
		return blockUntilCancelled(ctx)
	})
	if err := r.Start(ctx); err != nil {
		t.Fatal(err)
	}
	eventually(t, "a restart after a clean exit", func() bool { return calls.Load() == 2 })
}

func TestRunnerStopsUnknownConnector(t *testing.T) {
	store := openStore(t)
	acc, err := store.CreateAccount("retired", "Old", nil)
	if err != nil {
		t.Fatal(err)
	}
	r := NewRunner(store, NewRegistry())
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(func() { cancel(); r.Wait() })
	if err := r.Start(ctx); err != nil {
		t.Fatal(err)
	}
	eventually(t, "stopped state", func() bool {
		st := statusOf(r, acc)
		return st.State == StateStopped && strings.Contains(st.LastError, "retired")
	})
}

func TestRunnerSkipsDisabledAccounts(t *testing.T) {
	var calls atomic.Int32
	r, store, acc, ctx := newTestRunner(t, func(ctx context.Context, _ Sink, _ State, _ map[string]string) error {
		calls.Add(1)
		return blockUntilCancelled(ctx)
	})
	if err := store.SetAccountEnabled(acc, false); err != nil {
		t.Fatal(err)
	}
	if err := r.Start(ctx); err != nil {
		t.Fatal(err)
	}
	time.Sleep(20 * time.Millisecond)
	if calls.Load() != 0 || len(r.Status()) != 0 {
		t.Fatalf("disabled account ran: calls = %d, status = %+v", calls.Load(), r.Status())
	}
}

func TestRestartPicksUpNewSettings(t *testing.T) {
	seen := make(chan string, 4)
	r, store, acc, ctx := newTestRunner(t, func(ctx context.Context, _ Sink, _ State, settings map[string]string) error {
		seen <- settings["k"]
		return blockUntilCancelled(ctx)
	})
	if err := r.Start(ctx); err != nil {
		t.Fatal(err)
	}
	if got := <-seen; got != "v1" {
		t.Fatalf("first run saw %q", got)
	}
	if err := store.UpdateAccountSettings(acc, map[string]string{"k": "v2"}); err != nil {
		t.Fatal(err)
	}
	if err := r.Restart(acc); err != nil {
		t.Fatal(err)
	}
	if got := <-seen; got != "v2" {
		t.Fatalf("restarted run saw %q", got)
	}
}

func TestRestartOfDisabledAccountStopsIt(t *testing.T) {
	r, store, acc, ctx := newTestRunner(t, func(ctx context.Context, _ Sink, _ State, _ map[string]string) error {
		return blockUntilCancelled(ctx)
	})
	if err := r.Start(ctx); err != nil {
		t.Fatal(err)
	}
	eventually(t, "running state", func() bool { return statusOf(r, acc).State == StateRunning })
	if err := store.SetAccountEnabled(acc, false); err != nil {
		t.Fatal(err)
	}
	if err := r.Restart(acc); err != nil {
		t.Fatal(err)
	}
	if len(r.Status()) != 0 {
		t.Fatalf("status after disabling = %+v", r.Status())
	}
}

func TestProgressReachesStatus(t *testing.T) {
	r, _, acc, ctx := newTestRunner(t, func(ctx context.Context, sink Sink, _ State, _ map[string]string) error {
		sink.ReportProgress(Progress{Phase: PhaseBackfill, ChatsDone: 2, ChatsTotal: 9})
		return blockUntilCancelled(ctx)
	})
	if err := r.Start(ctx); err != nil {
		t.Fatal(err)
	}
	eventually(t, "progress in status", func() bool {
		p := statusOf(r, acc).Progress
		return p.Phase == PhaseBackfill && p.ChatsDone == 2 && p.ChatsTotal == 9
	})
}

// skippedAttachment stores a message whose attachment is over the cap and
// returns the attachment's database ID and ref.
func skippedAttachment(t *testing.T, store *archive.Store, acc int64) (int64, model.AttachmentRef) {
	t.Helper()
	if err := store.SetMediaCap(10); err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertChat(acc, model.Chat{SourceID: "c1", Kind: model.ChatPrivate, Title: "Alice"}); err != nil {
		t.Fatal(err)
	}
	msg := model.Message{
		ChatSourceID: "c1", SourceID: "1", SentAt: time.Unix(1_700_000_000, 0), Text: "big file",
		Attachments: []model.Attachment{{Kind: model.AttachVideo, FileName: "big.mp4", Size: 100}},
	}
	if _, err := store.UpsertMessage(acc, msg); err != nil {
		t.Fatal(err)
	}
	stored, err := store.Message(acc, "c1", "1")
	if err != nil {
		t.Fatal(err)
	}
	atts, err := store.Attachments(stored.ID)
	if err != nil {
		t.Fatal(err)
	}
	return atts[0].ID, model.AttachmentRef{ChatSourceID: "c1", MessageSourceID: "1", Position: 0}
}

func forwardRequests(got chan<- model.AttachmentRef) runFunc {
	return func(ctx context.Context, sink Sink, _ State, _ map[string]string) error {
		for {
			select {
			case ref := <-sink.RequestedMedia():
				got <- ref
			case <-ctx.Done():
				return ctx.Err()
			}
		}
	}
}

func TestRequestMediaReachesRunningConnector(t *testing.T) {
	got := make(chan model.AttachmentRef, 4)
	r, store, acc, ctx := newTestRunner(t, forwardRequests(got))
	attID, ref := skippedAttachment(t, store, acc)
	if err := r.Start(ctx); err != nil {
		t.Fatal(err)
	}
	eventually(t, "running state", func() bool { return statusOf(r, acc).State == StateRunning })
	if err := r.RequestMedia(attID); err != nil {
		t.Fatal(err)
	}
	select {
	case delivered := <-got:
		if delivered != ref {
			t.Fatalf("delivered %+v, want %+v", delivered, ref)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("request never reached the connector")
	}
}

func TestRequestMadeWhileStoppedIsDeliveredOnStart(t *testing.T) {
	got := make(chan model.AttachmentRef, 4)
	r, store, acc, ctx := newTestRunner(t, forwardRequests(got))
	attID, ref := skippedAttachment(t, store, acc)
	if err := r.RequestMedia(attID); err != nil { // nothing is running yet
		t.Fatal(err)
	}
	if err := r.Start(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case delivered := <-got:
		if delivered != ref {
			t.Fatalf("delivered %+v, want %+v", delivered, ref)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("stored request was not delivered on start")
	}
}

func TestDefaultBackoff(t *testing.T) {
	for attempt, want := range map[int]time.Duration{
		1: time.Second, 2: 2 * time.Second, 3: 4 * time.Second, 9: 256 * time.Second, 10: 5 * time.Minute, 60: 5 * time.Minute,
	} {
		if got := DefaultBackoff(attempt); got != want {
			t.Errorf("DefaultBackoff(%d) = %v, want %v", attempt, got, want)
		}
	}
}

// Overlapping restarts (two requests toggling the same account) must never
// leave more than one connector running for it.
func TestConcurrentRestartsLeaveOneConnector(t *testing.T) {
	var live, peak atomic.Int32
	r, _, acc, ctx := newTestRunner(t, func(ctx context.Context, _ Sink, _ State, _ map[string]string) error {
		n := live.Add(1)
		for {
			p := peak.Load()
			if n <= p || peak.CompareAndSwap(p, n) {
				break
			}
		}
		defer live.Add(-1)
		return blockUntilCancelled(ctx)
	})
	if err := r.Start(ctx); err != nil {
		t.Fatal(err)
	}
	eventually(t, "running state", func() bool { return statusOf(r, acc).State == StateRunning })

	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := r.Restart(acc); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	eventually(t, "the restarted connector", func() bool { return statusOf(r, acc).State == StateRunning })
	time.Sleep(20 * time.Millisecond)
	if live.Load() != 1 || peak.Load() != 1 || len(r.Status()) != 1 {
		t.Fatalf("live = %d, peak = %d, tracked = %d; want 1, 1, 1", live.Load(), peak.Load(), len(r.Status()))
	}
}

func TestRestartDuringShutdownStartsNothing(t *testing.T) {
	var calls atomic.Int32
	r, _, acc, _ := newTestRunner(t, func(ctx context.Context, _ Sink, _ State, _ map[string]string) error {
		calls.Add(1)
		return blockUntilCancelled(ctx)
	})
	ctx, cancel := context.WithCancel(context.Background())
	if err := r.Start(ctx); err != nil {
		t.Fatal(err)
	}
	eventually(t, "the first run", func() bool { return calls.Load() == 1 })
	cancel()
	r.Wait()
	if err := r.Restart(acc); err != nil {
		t.Fatal(err)
	}
	time.Sleep(20 * time.Millisecond)
	if n := calls.Load(); n != 1 {
		t.Fatalf("a connector was started after shutdown: %d runs", n)
	}
}

func TestBackoffEscalatesAndResetsAfterAHealthyRun(t *testing.T) {
	var (
		mu       sync.Mutex
		now      = time.Unix(1_700_000_000, 0)
		attempts []int
		calls    atomic.Int32
	)
	r, _, _, ctx := newTestRunner(t, func(ctx context.Context, _ Sink, _ State, _ map[string]string) error {
		switch n := calls.Add(1); {
		case n == 4: // this run lasts two minutes before it drops
			mu.Lock()
			now = now.Add(2 * time.Minute)
			mu.Unlock()
			return errors.New("dropped after a long run")
		case n <= 5:
			return errors.New("boom")
		}
		return blockUntilCancelled(ctx)
	})
	r.now = func() time.Time {
		mu.Lock()
		defer mu.Unlock()
		return now
	}
	r.Backoff = func(attempt int) time.Duration {
		mu.Lock()
		attempts = append(attempts, attempt)
		mu.Unlock()
		return time.Millisecond
	}
	if err := r.Start(ctx); err != nil {
		t.Fatal(err)
	}
	eventually(t, "the sixth run", func() bool { return calls.Load() == 6 })
	mu.Lock()
	got := slices.Clone(attempts)
	mu.Unlock()
	if want := []int{1, 2, 3, 1, 2}; !slices.Equal(got, want) {
		t.Fatalf("backoff attempts = %v, want %v", got, want)
	}
}

func TestRunnerSurvivesPanickingFactory(t *testing.T) {
	store := openStore(t)
	acc := newAccount(t, store)
	reg := NewRegistry()
	reg.Register(stubInfo("stub"), func() Connector { panic("factory blew up") })
	r := NewRunner(store, reg)
	r.Backoff = func(int) time.Duration { return time.Hour }
	r.Logf = func(string, ...any) {}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(func() { cancel(); r.Wait() })
	if err := r.Start(ctx); err != nil {
		t.Fatal(err)
	}
	eventually(t, "backoff state", func() bool { return statusOf(r, acc).State == StateBackoff })
	if msg := statusOf(r, acc).LastError; !strings.Contains(msg, "factory blew up") {
		t.Fatalf("last error = %q", msg)
	}
}

// A crash-looping connector must leave a trace an operator can read.
func TestRunnerLogsFailuresWithPanicStack(t *testing.T) {
	var (
		mu    sync.Mutex
		lines []string
	)
	r, _, acc, ctx := newTestRunner(t, func(context.Context, Sink, State, map[string]string) error {
		panic("nil map write")
	})
	r.Backoff = func(int) time.Duration { return time.Hour }
	r.Logf = func(format string, args ...any) {
		mu.Lock()
		lines = append(lines, fmt.Sprintf(format, args...))
		mu.Unlock()
	}
	if err := r.Start(ctx); err != nil {
		t.Fatal(err)
	}
	eventually(t, "backoff state", func() bool { return statusOf(r, acc).State == StateBackoff })
	mu.Lock()
	logged := strings.Join(lines, "\n")
	mu.Unlock()
	for _, want := range []string{`"Test"`, "stub", "nil map write", "goroutine ", "backoff"} {
		if !strings.Contains(logged, want) {
			t.Errorf("log lacks %q:\n%s", want, logged)
		}
	}
}

// signingOut is a connector that can end its session: SignOut records it in
// state, and Run then reports that it needs login.
type signingOut struct{ stubConnector }

func (signingOut) SignOut(_ context.Context, state State) error {
	return state.Set("signed_out", []byte("1"))
}

func TestSignOutEndsTheSessionAndRestarts(t *testing.T) {
	store := openStore(t)
	acc := newAccount(t, store)
	reg := NewRegistry()
	run := func(ctx context.Context, _ Sink, state State, _ map[string]string) error {
		if v, _ := state.Get("signed_out"); len(v) > 0 {
			return ErrNeedsLogin
		}
		return blockUntilCancelled(ctx)
	}
	reg.Register(stubInfo("stub"), func() Connector { return signingOut{stubConnector{run: run}} })
	r := NewRunner(store, reg)
	r.Logf = func(string, ...any) {}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(func() { cancel(); r.Wait() })
	if err := r.Start(ctx); err != nil {
		t.Fatal(err)
	}
	eventually(t, "the connector to run", func() bool { return statusOf(r, acc).State == StateRunning })

	if err := r.SignOut(ctx, acc); err != nil {
		t.Fatal(err)
	}
	eventually(t, "the connector to need login", func() bool { return statusOf(r, acc).State == StateNeedsLogin })
	if v, _ := store.StateGet(acc, "signed_out"); string(v) != "1" {
		t.Errorf("SignOut did not get the account's state: %q", v)
	}
	if err := r.SignOut(ctx, 9999); !errors.Is(err, archive.ErrNotFound) {
		t.Errorf("unknown account: %v, want ErrNotFound", err)
	}
}

func TestSignOutNeedsAConnectorThatCan(t *testing.T) {
	r, _, acc, ctx := newTestRunner(t, func(ctx context.Context, _ Sink, _ State, _ map[string]string) error {
		return blockUntilCancelled(ctx)
	})
	if err := r.Start(ctx); err != nil {
		t.Fatal(err)
	}
	eventually(t, "the connector to run", func() bool { return statusOf(r, acc).State == StateRunning })
	if err := r.SignOut(ctx, acc); !errors.Is(err, ErrCannotSignOut) {
		t.Fatalf("SignOut on a connector without it = %v, want ErrCannotSignOut", err)
	}
	if st := statusOf(r, acc).State; st != StateRunning {
		t.Errorf("the connector was disturbed: %s", st)
	}
}
