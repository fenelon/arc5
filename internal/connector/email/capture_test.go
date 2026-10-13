package email

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/fenelon/arc5/internal/archive"
	"github.com/fenelon/arc5/internal/connector"
	"github.com/fenelon/arc5/internal/model"
)

var gmailFolders = []string{
	"INBOX", `[Gmail]/All Mail \all \hasnochildren`, `[Gmail]/Sent Mail \sent`, `[Gmail]/Drafts \drafts`, `[Gmail]/Spam \junk`,
	`[Gmail]/Trash \trash`, `[Gmail]/Starred \flagged`, `[Gmail]/Important \important`, "Work",
}

var otherFolders = []string{"INBOX", "Sent", "Drafts", "Junk", "Trash", "Archive", "Projects/2026"}

// running is a run of the connector against a fake server and a real archive.
type running struct {
	t      *testing.T
	a      *archived
	srv    *fakeServer
	state  *memState
	cycles chan struct{}
	cancel context.CancelFunc
	done   chan error
}

var now = time.Date(2026, 10, 13, 12, 0, 0, 0, time.UTC)

func login(state *memState, password string) {
	saveJSON(state, keyAccount, account{Address: "me@example.org", Host: "imap.example.org", Port: 993, Username: "me@example.org", Password: password})
}

// start runs the connector; slice 0 makes each cycle take one batch of history.
func start(t *testing.T, a *archived, srv *fakeServer, state *memState, settings map[string]string, slice time.Duration) *running {
	t.Helper()
	r := &running{t: t, a: a, srv: srv, state: state, cycles: make(chan struct{}, 1), done: make(chan error, 1)}
	c := &Connector{
		dial: srv.dial, now: func() time.Time { return now }, sleep: func(ctx context.Context, _ time.Duration) error { return ctx.Err() },
		slice: slice, wait: 20 * time.Millisecond,
		afterCycle: func() {
			select {
			case r.cycles <- struct{}{}:
			default:
			}
		},
	}
	ctx, cancel := context.WithCancel(context.Background())
	r.cancel = cancel
	go func() { r.done <- c.Run(ctx, a.sink, state, settings) }()
	t.Cleanup(r.stop)
	return r
}

func (r *running) stop() {
	if r.cancel == nil {
		return
	}
	r.cancel()
	r.cancel = nil
	select {
	case err := <-r.done:
		if err != nil && !errors.Is(err, context.Canceled) {
			r.t.Errorf("run ended with %v", err)
		}
	case <-time.After(5 * time.Second):
		r.t.Error("the run did not stop")
	}
}

// settle waits for two whole cycles to pass, so changes made before it are
// taken in.
func (r *running) settle() {
	r.t.Helper()
	for i := 0; i < 2; i++ {
		select {
		case <-r.cycles:
		case err := <-r.done:
			r.t.Fatalf("the run ended: %v", err)
		case <-time.After(10 * time.Second):
			r.t.Fatal("no cycle finished")
		}
	}
}

func msg(id, from, subject, body string, extra ...string) string {
	headers := fmt.Sprintf("From: %s\nTo: me@example.org\nSubject: %s\nDate: Mon, 05 Oct 2026 10:00:00 +0000\nMessage-ID: <%s>\n", from, subject, id)
	for _, h := range extra {
		headers += h + "\n"
	}
	return headers + "\n" + body + "\n"
}

func (a *archived) count(t *testing.T) int {
	t.Helper()
	var n int
	a.store.Reader().QueryRow(`SELECT COUNT(*) FROM messages m JOIN chats c ON c.id = m.chat_id WHERE c.account_id = ?`, a.acc).Scan(&n)
	return n
}

func (a *archived) filed(t *testing.T, id string) []string {
	t.Helper()
	rows, err := a.store.Reader().Query(`
		SELECT f.source_id || CASE WHEN mf.gone_at IS NULL THEN '' ELSE ' (gone)' END
		FROM message_folders mf JOIN folders f ON f.id = mf.folder_id JOIN messages m ON m.id = mf.message_id
		WHERE m.source_id = ? ORDER BY f.source_id`, id)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var s string
		rows.Scan(&s)
		out = append(out, s)
	}
	return out
}

func (a *archived) deleted(t *testing.T, id string) bool {
	t.Helper()
	var deleted bool
	a.store.Reader().QueryRow(`SELECT deleted_at IS NOT NULL FROM messages WHERE source_id = ?`, id).Scan(&deleted)
	return deleted
}

func TestRunNeedsLogin(t *testing.T) {
	a := newArchived(t)
	srv := newFakeServer("INBOX")
	state := newMemState()
	login(state, "old password")
	c := &Connector{dial: srv.dial, now: time.Now, sleep: sleep, wait: time.Millisecond}
	if err := c.Run(context.Background(), a.sink, state, nil); !errors.Is(err, connector.ErrNeedsLogin) {
		t.Fatalf("a refused login: %v", err)
	}
	srv.dialErr = errors.New("connection reset")
	login(state, "app-password")
	if err := c.Run(context.Background(), a.sink, state, nil); err == nil || errors.Is(err, connector.ErrNeedsLogin) {
		t.Fatalf("a network failure: %v", err)
	}
}

func folderState(t *testing.T, a *archived) []string {
	t.Helper()
	folders, err := a.store.AccountFolders(a.acc)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, f := range folders {
		mark := "-"
		if f.Archived {
			mark = "+"
		}
		out = append(out, mark+f.Name+":"+string(f.Role))
	}
	return out
}

func TestGmailFolders(t *testing.T) {
	a := newArchived(t)
	srv := newFakeServer(gmailFolders...)
	state := newMemState()
	login(state, "app-password")
	r := start(t, a, srv, state, nil, time.Hour)
	r.settle()
	want := []string{"+Inbox:inbox", "+Sent Mail:sent", "+All Mail:all", "-Work:other", "-Starred:flagged", "-Important:important", "-Drafts:drafts", "-Spam:junk", "-Trash:trash"}
	if got := folderState(t, a); !slices.Equal(got, want) {
		t.Errorf("folders = %v\nwant %v", got, want)
	}
	if acc, _ := a.store.Account(a.acc); acc.Address != "me@example.org" {
		t.Errorf("address = %q", acc.Address)
	}
}

func TestOtherServerFolders(t *testing.T) {
	a := newArchived(t)
	srv := newFakeServer(otherFolders...)
	state := newMemState()
	login(state, "app-password")
	r := start(t, a, srv, state, nil, time.Hour)
	r.settle()
	want := []string{"+Inbox:inbox", "+Sent:sent", "+Archive:other", "+Projects/2026:other", "-Drafts:drafts", "-Junk:junk", "-Trash:trash"}
	if got := folderState(t, a); !slices.Equal(got, want) {
		t.Errorf("folders = %v\nwant %v", got, want)
	}
}

func TestBackfillStoresOnceAcrossFolders(t *testing.T) {
	a := newArchived(t)
	srv := newFakeServer(gmailFolders...)
	at := time.Date(2026, 10, 5, 10, 0, 0, 0, time.UTC)
	hello := msg("hello@example.com", "Dan <dan@example.com>", "Hello", "Hi there.")
	reply := msg("reply@example.org", "Me <me@example.org>", "Re: Hello", "Hi Dan.", "In-Reply-To: <hello@example.com>", "References: <hello@example.com>")
	noID := "From: x@example.com\nSubject: No ID\nDate: Mon, 05 Oct 2026 09:00:00 +0000\n\nNo Message-ID here.\n"
	srv.add("[Gmail]/All Mail", hello, at)
	srv.add("[Gmail]/All Mail", reply, at.Add(time.Hour))
	srv.add("[Gmail]/All Mail", noID, at.Add(-time.Hour))
	srv.add("INBOX", hello, at)
	srv.add("INBOX", noID, at.Add(-time.Hour))
	srv.add("[Gmail]/Sent Mail", reply, at.Add(time.Hour))
	srv.add("Work", hello, at) // a label that is not archived
	state := newMemState()
	login(state, "app-password")
	r := start(t, a, srv, state, nil, time.Hour)
	r.settle()

	if n := a.count(t); n != 3 {
		t.Fatalf("%d messages stored, want 3", n)
	}
	if got := a.filed(t, "hello@example.com"); !slices.Equal(got, []string{"INBOX", "[Gmail]/All Mail"}) {
		t.Errorf("hello is filed in %v", got)
	}
	if got := a.filed(t, "reply@example.org"); !slices.Equal(got, []string{"[Gmail]/All Mail", "[Gmail]/Sent Mail"}) {
		t.Errorf("the reply is filed in %v", got)
	}
	// Whole messages were fetched once each, except the one without a
	// Message-ID, which can only be recognised from its content.
	fetched := srv.fetches()
	if len(fetched) != 4 {
		t.Errorf("whole fetches = %v, want 3 messages and the second copy of the one without an ID", fetched)
	}
	// The reply joined the conversation, and is the owner's.
	m, err := a.store.Message(a.acc, "thread:hello@example.com", "reply@example.org")
	if err != nil || !m.Outgoing {
		t.Errorf("reply = %+v, %v", m, err)
	}
	var progress connector.Progress
	_ = progress
}

func TestCheckpointsResume(t *testing.T) {
	a := newArchived(t)
	srv := newFakeServer("INBOX")
	for i := 0; i < 120; i++ {
		srv.add("INBOX", msg(fmt.Sprintf("m%d@x", i), "a@example.com", fmt.Sprint("Number ", i), "body"), now.Add(-time.Duration(120-i)*time.Hour))
	}
	state := newMemState()
	login(state, "app-password")
	// One batch of history, then the run stops, as if the computer did.
	c := &Connector{dial: srv.dial, now: func() time.Time { return now }, sleep: func(ctx context.Context, _ time.Duration) error { return ctx.Err() }, wait: time.Millisecond}
	ctx, cancel := context.WithCancel(context.Background())
	c.afterCycle = cancel
	if err := c.Run(ctx, a.sink, state, nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("first run: %v", err)
	}
	partial := a.count(t)
	if partial == 0 || partial == 120 {
		t.Fatalf("after two cycles %d messages are stored; the test needs a partial catch-up", partial)
	}
	// The newest come first.
	if _, err := a.store.Message(a.acc, "thread:m119@x", "m119@x"); err != nil {
		t.Errorf("the newest message is not among the first: %v", err)
	}
	r := start(t, a, srv, state, nil, time.Hour)
	r.settle()
	if n := a.count(t); n != 120 {
		t.Fatalf("%d messages after resuming", n)
	}
	if fetched := srv.fetches(); len(fetched) != 120 {
		t.Errorf("%d whole fetches, want 120: a restart fetched again", len(fetched))
	}
	uids, _ := a.sink.FolderUIDs("INBOX")
	if len(uids) != 120 {
		t.Errorf("%d UIDs filed", len(uids))
	}
}

func TestNewMailAndDeletions(t *testing.T) {
	a := newArchived(t)
	srv := newFakeServer(otherFolders...)
	at := time.Date(2026, 10, 5, 10, 0, 0, 0, time.UTC)
	one := msg("one@x", "a@example.com", "One", "first")
	two := msg("two@x", "b@example.com", "Two", "second")
	srv.add("INBOX", one, at)
	srv.add("Archive", one, at)
	inboxTwo := srv.add("INBOX", two, at)
	state := newMemState()
	login(state, "app-password")
	r := start(t, a, srv, state, nil, time.Hour)
	r.settle()
	if n := a.count(t); n != 2 {
		t.Fatalf("%d stored", n)
	}

	// New mail arrives.
	srv.add("INBOX", msg("three@x", "c@example.com", "Three", "third"), now)
	r.settle()
	if got := a.filed(t, "three@x"); !slices.Equal(got, []string{"INBOX"}) {
		t.Errorf("new mail filed in %v", got)
	}

	// Two is moved from the Inbox to the Archive: not a deletion.
	srv.add("Archive", two, at)
	srv.expunge("INBOX", inboxTwo)
	r.settle()
	if a.deleted(t, "two@x") {
		t.Error("a moved message is marked deleted")
	}
	if got := a.filed(t, "two@x"); !slices.Equal(got, []string{"Archive", "INBOX (gone)"}) {
		t.Errorf("two is filed in %v", got)
	}

	// One leaves the Inbox, then the Archive: deleted only when gone from both.
	srv.expunge("INBOX", 1)
	r.settle()
	if a.deleted(t, "one@x") {
		t.Error("deleted after leaving one folder of two")
	}
	srv.expunge("Archive", 1)
	r.settle()
	if !a.deleted(t, "one@x") {
		t.Error("not deleted after leaving every folder")
	}
}

func TestUIDValidityChange(t *testing.T) {
	a := newArchived(t)
	srv := newFakeServer("INBOX")
	for i := 0; i < 3; i++ {
		srv.add("INBOX", msg(fmt.Sprintf("v%d@x", i), "a@example.com", "V", "body"), now.Add(-time.Hour))
	}
	state := newMemState()
	login(state, "app-password")
	r := start(t, a, srv, state, nil, time.Hour)
	r.settle()
	srv.renumber("INBOX")
	r.settle()
	r.settle()
	if n := a.count(t); n != 3 {
		t.Errorf("%d messages after renumbering", n)
	}
	if uids, _ := a.sink.FolderUIDs("INBOX"); !slices.Equal(uids, []uint32{100, 101, 102}) {
		t.Errorf("UIDs = %v", uids)
	}
	if len(srv.fetches()) != 3 {
		t.Errorf("whole fetches = %v", srv.fetches())
	}
	for i := 0; i < 3; i++ {
		if a.deleted(t, fmt.Sprintf("v%d@x", i)) {
			t.Error("renumbering marked a message deleted")
		}
	}
}

func TestLongerAgeFetchesOlderMail(t *testing.T) {
	a := newArchived(t)
	srv := newFakeServer("INBOX")
	srv.add("INBOX", msg("old@x", "a@example.com", "Old", "old"), time.Date(2025, 6, 1, 0, 0, 0, 0, time.UTC))
	srv.add("INBOX", msg("new@x", "a@example.com", "New", "new"), time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC))
	state := newMemState()
	login(state, "app-password")
	r := start(t, a, srv, state, map[string]string{"age": "Since a date", "since": "2026-01-01"}, time.Hour)
	r.settle()
	r.stop()
	if n := a.count(t); n != 1 {
		t.Fatalf("%d stored under a 2026 limit", n)
	}
	r = start(t, a, srv, state, map[string]string{"age": "All history"}, time.Hour)
	r.settle()
	if n := a.count(t); n != 2 {
		t.Fatalf("%d stored after lengthening the limit", n)
	}
	if len(srv.fetches()) != 2 {
		t.Errorf("whole fetches = %v", srv.fetches())
	}
}

func TestPausedSkipsHistory(t *testing.T) {
	a := newArchived(t)
	srv := newFakeServer("INBOX")
	srv.add("INBOX", msg("old@x", "a@example.com", "Old", "old"), now.Add(-time.Hour))
	state := newMemState()
	login(state, "app-password")
	r := start(t, a, srv, state, map[string]string{"paused": "true"}, time.Hour)
	r.settle()
	srv.add("INBOX", msg("new@x", "a@example.com", "New", "new"), now)
	r.settle()
	if _, err := a.store.Message(a.acc, "thread:new@x", "new@x"); err != nil {
		t.Errorf("new mail was not saved while paused: %v", err)
	}
	if _, err := a.store.Message(a.acc, "thread:old@x", "old@x"); !errors.Is(err, archive.ErrNotFound) {
		t.Errorf("old mail was fetched while paused: %v", err)
	}
}

func TestOutgoingByOwnAddresses(t *testing.T) {
	a := newArchived(t)
	srv := newFakeServer("INBOX")
	srv.add("INBOX", msg("w@x", "Me at work <ME@work.example>", "From work", "hi"), now)
	state := newMemState()
	login(state, "app-password")
	r := start(t, a, srv, state, map[string]string{"addresses": " other@example.com, me@work.example "}, time.Hour)
	r.settle()
	if m, err := a.store.Message(a.acc, "thread:w@x", "w@x"); err != nil || !m.Outgoing {
		t.Errorf("mail from another own address = %+v, %v", m, err)
	}
}

func TestFetchNowDuringARun(t *testing.T) {
	store := newArchived(t)
	requests := make(chan model.AttachmentRef, 1)
	store.sink = connector.NewSink(store.store, store.acc, requests, func(connector.Progress) {})
	if err := store.store.SetMediaCap(4); err != nil {
		t.Fatal(err)
	}
	srv := newFakeServer("INBOX")
	srv.add("INBOX", "From: a@example.com\nSubject: Big\nMessage-ID: <big@x>\nContent-Type: multipart/mixed; boundary=b\n\n--b\nContent-Type: text/plain\n\nhi\n--b\nContent-Type: application/zip\nContent-Disposition: attachment; filename=big.zip\n\nmore than four bytes\n--b--\n", now)
	state := newMemState()
	login(state, "app-password")
	r := start(t, store, srv, state, nil, time.Hour)
	r.settle()
	m, err := store.store.Message(store.acc, "thread:big@x", "big@x")
	if err != nil {
		t.Fatal(err)
	}
	requests <- model.AttachmentRef{ChatSourceID: "thread:big@x", MessageSourceID: "big@x", Position: 0}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		atts, _ := store.store.Attachments(m.ID)
		if len(atts) == 1 && atts[0].Status == model.StatusStored {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Error("the requested file was not taken out of the original")
}

func TestProgress(t *testing.T) {
	a := newArchived(t)
	var last connector.Progress
	reports := make(chan connector.Progress, 100)
	a.sink = connector.NewSink(a.store, a.acc, nil, func(p connector.Progress) {
		select {
		case reports <- p:
		default:
		}
	})
	srv := newFakeServer("INBOX", "Sent")
	srv.add("INBOX", msg("p@x", "a@example.com", "P", "p"), now)
	state := newMemState()
	login(state, "app-password")
	r := start(t, a, srv, state, nil, time.Hour)
	r.settle()
	for len(reports) > 0 {
		last = <-reports
	}
	if last.Phase != connector.PhaseLive || last.ChatsTotal != 2 || last.ChatsDone != 2 {
		t.Errorf("last progress = %+v", last)
	}
}
