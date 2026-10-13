// Package telegram archives a Telegram account: its chats' history, new
// messages, edits and deletions as they happen, media, and pictures. It
// signs in as the owner's user account with the owner's own API keys.
package telegram

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/fenelon/arc5/internal/connector"
)

// Name is the connector's name, stored on each Telegram account.
const Name = "telegram"

// Connector is the Telegram connector.
type Connector struct {
	dial        dialer
	now         func() time.Time
	sleep       func(ctx context.Context, d time.Duration) error
	signOutWait time.Duration // how long signing out waits for Telegram
}

// New returns the connector, talking to the real Telegram.
func New() *Connector {
	return &Connector{dial: dialTelegram, now: time.Now, sleep: sleep, signOutWait: 20 * time.Second}
}

// Register adds the Telegram connector to a registry.
func Register(reg *connector.Registry) {
	reg.Register(connector.Info{Name: Name, Title: "Telegram", Display: connector.DisplayChats}, func() connector.Connector { return New() })
}

func sleep(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// Ages are the choices for how far back history is fetched.
var ages = []string{"All history", "Last 5 years", "Last year", "Last 3 months", "Since a date"}

// Speeds are the choices for how fast history is fetched, and the pause
// between two requests for history.
var speeds = map[string]time.Duration{"Slow": 5 * time.Second, "Normal": 2 * time.Second, "Fast": 500 * time.Millisecond}

// Settings are the per-account options on the Sources page.
func (c *Connector) Settings() []connector.Field {
	return []connector.Field{
		{Name: "age", Label: "How far back", Help: "History older than this is not fetched. Messages already archived stay.",
			Kind: connector.FieldChoice, Choices: ages, Default: ages[0]},
		{Name: "since", Label: "Since", Help: "For \"Since a date\": the first day to fetch, as 2024-03-01.", Kind: connector.FieldText},
		{Name: "speed", Label: "Speed", Help: "How quickly history is fetched. New messages are saved the moment they arrive, whatever this is.",
			Kind: connector.FieldChoice, Choices: []string{"Slow", "Normal", "Fast"}, Default: "Normal"},
		{Name: "paused", Label: "Pause catch-up", Help: "Stop fetching history for now. New messages are still saved.", Kind: connector.FieldBool},
		{Name: "keep_ephemeral", Label: "Keep self-destructing and view-once media",
			Help: "Off unless you turn it on. The sender expected it to disappear. arc5 downloads it without marking it as opened.",
			Kind: connector.FieldBool},
	}
}

// options are an account's settings, read once per run.
type options struct {
	oldest        time.Time // history before this is not fetched; zero for all
	pause         time.Duration
	paused        bool
	keepEphemeral bool
}

func readOptions(settings map[string]string, now func() time.Time) options {
	o := options{pause: speeds["Normal"], paused: settings["paused"] == "true", keepEphemeral: settings["keep_ephemeral"] == "true"}
	if d, ok := speeds[settings["speed"]]; ok {
		o.pause = d
	}
	today := now().UTC().Truncate(24 * time.Hour)
	switch settings["age"] {
	case "Last 5 years":
		o.oldest = today.AddDate(-5, 0, 0)
	case "Last year":
		o.oldest = today.AddDate(-1, 0, 0)
	case "Last 3 months":
		o.oldest = today.AddDate(0, -3, 0)
	case "Since a date":
		if day, err := time.Parse("2006-01-02", settings["since"]); err == nil {
			o.oldest = day
		}
	}
	return o
}

// run is one run of the connector for one account.
type run struct {
	c      *Connector
	sink   connector.Sink
	state  connector.State
	opts   options
	conn   conn
	hashes *updateStorage
	selfID int64

	mu    sync.Mutex
	e     *entities // every user and chat seen, for names and peers
	order []string  // chats by recent activity, newest first
}

// Run archives the account until ctx ends: it brings the chat list up to
// date, then captures live updates, fetches history and downloads media at
// the same time.
func (c *Connector) Run(ctx context.Context, sink connector.Sink, state connector.State, settings map[string]string) error {
	creds, ok, err := loadCredentials(state)
	if err != nil {
		return err
	}
	if session, err := state.Get(keySession); err != nil {
		return err
	} else if !ok || session == nil {
		return connector.ErrNeedsLogin
	}
	sink.ReportProgress(connector.Progress{Phase: connector.PhaseConnecting})
	r := &run{c: c, sink: sink, state: state, opts: readOptions(settings, c.now), hashes: newUpdateStorage(state), e: newEntities(nil, nil)}
	r.conn, err = c.dial(ctx, creds, state, listener{handler: r, tooLong: r.tooLong, channelTooLong: r.channelTooLong})
	if err != nil {
		return r.fail(err)
	}
	defer r.conn.Close()
	me, err := r.conn.Self(ctx)
	if err != nil {
		return r.fail(err)
	}
	if known, _ := loadSelf(state); known != 0 && known != me.ID {
		return fmt.Errorf("%w: the session belongs to another Telegram account", connector.ErrNeedsLogin)
	}
	r.selfID = me.ID
	r.learn(nil, nil, me)
	if err := r.syncChats(ctx); err != nil {
		return r.fail(err)
	}
	return r.fail(together(ctx,
		func(ctx context.Context) error { return r.conn.Listen(ctx, r.selfID) },
		r.backfill,
	))
}

// fail says what an error means for the runner: a dead session needs a login.
func (r *run) fail(err error) error {
	if err != nil && !errors.Is(err, context.Canceled) && unauthorized(err) {
		return fmt.Errorf("%w: %v", connector.ErrNeedsLogin, err)
	}
	return err
}

// together runs tasks until ctx ends or one fails, and returns why they
// stopped. A task that finishes without an error leaves the others running.
func together(ctx context.Context, tasks ...func(context.Context) error) error {
	ctx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	var wg sync.WaitGroup
	for _, task := range tasks {
		wg.Go(func() {
			if err := task(ctx); err != nil {
				cancel(err)
			}
		})
	}
	wg.Wait()
	return context.Cause(ctx)
}
