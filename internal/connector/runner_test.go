package connector

import (
	"context"
	"errors"
	"fmt"
	"strings"
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
	reg.Register("stub", func() Connector { return stubConnector{run: run} })
	r := NewRunner(store, reg)
	r.Backoff = func(int) time.Duration { return time.Millisecond }
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
