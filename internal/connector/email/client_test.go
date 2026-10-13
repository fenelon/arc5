package email

import (
	"bytes"
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"
	"github.com/emersion/go-imap/v2/imapserver"
	"github.com/emersion/go-imap/v2/imapserver/imapmemserver"
)

// memServer is go-imap's in-process server on a local port, with one user.
type memServer struct {
	addr string
	user *imapmemserver.User
}

func newMemServer(t *testing.T) *memServer {
	t.Helper()
	mem := imapmemserver.New()
	user := imapmemserver.NewUser("me@example.org", "app-password")
	for _, name := range []string{"INBOX", "Sent", "Archive"} {
		if err := user.Create(name, nil); err != nil {
			t.Fatal(err)
		}
	}
	mem.AddUser(user)
	srv := imapserver.New(&imapserver.Options{
		NewSession: func(*imapserver.Conn) (imapserver.Session, *imapserver.GreetingData, error) {
			return mem.NewSession(), nil, nil
		},
		Caps:         imap.CapSet{imap.CapIMAP4rev1: {}, imap.CapIMAP4rev2: {}},
		InsecureAuth: true,
		Logger:       quietLogger{},
	})
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go srv.Serve(ln)
	t.Cleanup(func() { srv.Close() })
	return &memServer{addr: ln.Addr().String(), user: user}
}

type quietLogger struct{}

func (quietLogger) Printf(string, ...any) {}

// literal is an imap.LiteralReader over bytes.
type literal struct {
	*bytes.Reader
	size int64
}

func (l literal) Size() int64 { return l.size }

func (m *memServer) add(t *testing.T, folder, raw string, received time.Time) {
	t.Helper()
	data := []byte(strings.ReplaceAll(raw, "\n", "\r\n"))
	if _, err := m.user.Append(folder, literal{bytes.NewReader(data), int64(len(data))}, &imap.AppendOptions{Time: received}); err != nil {
		t.Fatal(err)
	}
}

// connect logs in as the connector does, over a plain local connection.
func (m *memServer) connect(t *testing.T, password string) (*imapClient, error) {
	t.Helper()
	conn, err := net.Dial("tcp", m.addr)
	if err != nil {
		t.Fatal(err)
	}
	changes := make(chan struct{}, 1)
	c := imapclient.New(conn, &imapclient.Options{UnilateralDataHandler: changeHandler(changes)})
	ic := &imapClient{c: c, changes: changes}
	if err := ic.login(context.Background(), "me@example.org", password); err != nil {
		c.Close()
		return nil, err
	}
	t.Cleanup(func() { ic.Close() })
	return ic, nil
}

// other is a second connection, as another mail program, to change the
// mailbox under the connector.
func (m *memServer) other(t *testing.T) *imapclient.Client {
	t.Helper()
	c, err := imapclient.DialInsecure(m.addr, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Login("me@example.org", "app-password").Wait(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	return c
}

const first = `From: Dan <dan@example.com>
Subject: One
Message-ID: <One@Example.COM>

First body.
`

const second = `From: Priya <priya@example.com>
Subject: Two
Message-ID: <two@example.com>

Second body.
`

func TestClientAgainstAServer(t *testing.T) {
	ctx := context.Background()
	srv := newMemServer(t)
	srv.add(t, "INBOX", first, time.Date(2020, 1, 2, 0, 0, 0, 0, time.UTC))
	srv.add(t, "INBOX", second, time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC))

	if _, err := srv.connect(t, "wrong"); !errors.Is(err, errLoginRefused) {
		t.Fatalf("a wrong password: %v", err)
	}
	c, err := srv.connect(t, "app-password")
	if err != nil {
		t.Fatal(err)
	}

	boxes, err := c.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, b := range boxes {
		names = append(names, b.Name)
	}
	slices.Sort(names)
	if !slices.Equal(names, []string{"Archive", "INBOX", "Sent"}) {
		t.Errorf("folders = %v", names)
	}

	sel, err := c.Select(ctx, "INBOX")
	if err != nil || sel.Messages != 2 || sel.UIDNext != 3 || sel.UIDValidity == 0 {
		t.Fatalf("select = %+v, %v", sel, err)
	}
	if uids, err := c.Search(ctx, 1, 0, time.Time{}); err != nil || !slices.Equal(uids, []uint32{1, 2}) {
		t.Errorf("all = %v, %v", uids, err)
	}
	if uids, _ := c.Search(ctx, 3, 0, time.Time{}); len(uids) != 0 {
		t.Errorf("above the last = %v", uids)
	}
	if uids, _ := c.Search(ctx, 1, 0, time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)); !slices.Equal(uids, []uint32{2}) {
		t.Errorf("since 2025 = %v", uids)
	}
	if uids, _ := c.Search(ctx, 1, 1, time.Time{}); !slices.Equal(uids, []uint32{1}) {
		t.Errorf("1:1 = %v", uids)
	}

	heads, err := c.Headers(ctx, []uint32{2, 1})
	if err != nil || len(heads) != 2 {
		t.Fatalf("headers = %+v, %v", heads, err)
	}
	if heads[0].UID != 1 || heads[0].MessageID != "One@example.com" || heads[0].Size == 0 || heads[1].MessageID != "two@example.com" ||
		!heads[0].Received.Equal(time.Date(2020, 1, 2, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("headers = %+v", heads)
	}
	whole, err := c.Fetch(ctx, 1, false)
	if err != nil || !bytes.Contains(whole, []byte("First body.")) || !bytes.Contains(whole, []byte("Subject: One")) {
		t.Errorf("whole = %q, %v", whole, err)
	}
	head, err := c.Fetch(ctx, 1, true)
	if err != nil || bytes.Contains(head, []byte("First body.")) || !bytes.Contains(head, []byte("Subject: One")) {
		t.Errorf("headers only = %q, %v", head, err)
	}
	if _, err := c.Fetch(ctx, 99, false); !errors.Is(err, errGone) {
		t.Errorf("a missing message: %v", err)
	}

	// Nothing the connector did marked mail as read.
	other := srv.other(t)
	if _, err := other.Select("INBOX", &imap.SelectOptions{ReadOnly: true}).Wait(); err != nil {
		t.Fatal(err)
	}
	msgs, err := other.Fetch(imap.SeqSetNum(1, 2), &imap.FetchOptions{Flags: true}).Collect()
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range msgs {
		if slices.Contains(m.Flags, imap.FlagSeen) {
			t.Errorf("message %d is marked as read", m.SeqNum)
		}
	}

	// IDLE wakes when new mail arrives.
	woke := make(chan error, 1)
	go func() { woke <- c.Idle(ctx, 20*time.Second) }()
	time.Sleep(200 * time.Millisecond) // let IDLE start
	srv.add(t, "INBOX", "From: x@example.com\nSubject: Three\nMessage-ID: <three@x>\n\nThird.\n", time.Now())
	select {
	case err := <-woke:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("IDLE did not wake for new mail")
	}

	// An expunge elsewhere shows as a lower count.
	if _, err := other.Select("INBOX", nil).Wait(); err != nil {
		t.Fatal(err)
	}
	if err := other.Store(imap.UIDSetNum(1), &imap.StoreFlags{Op: imap.StoreFlagsAdd, Flags: []imap.Flag{imap.FlagDeleted}}, nil).Close(); err != nil {
		t.Fatal(err)
	}
	if err := other.Expunge().Close(); err != nil {
		t.Fatal(err)
	}
	sel, err = c.Select(ctx, "INBOX")
	if err != nil || sel.Messages != 2 {
		t.Errorf("after an expunge: %+v, %v", sel, err)
	}
	if uids, _ := c.Search(ctx, 1, 0, time.Time{}); !slices.Equal(uids, []uint32{2, 3}) {
		t.Errorf("UIDs after an expunge = %v", uids)
	}

	// A cancelled context ends a command.
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if err := c.Idle(cancelled, time.Minute); !errors.Is(err, context.Canceled) {
		t.Errorf("idle with a cancelled context: %v", err)
	}
}

// The connector never sends a command that changes the mailbox.
func TestNoCommandChangesTheMailbox(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		src, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for _, call := range []string{".Store(", ".Copy(", ".Move(", ".Expunge(", ".UIDExpunge(", ".Append(", ".Delete(", ".Create(", ".Rename(", ".Subscribe(", ".Unsubscribe(", ".SetMetadata(", "UnselectAndExpunge"} {
			if strings.Contains(string(src), call) {
				t.Errorf("%s calls %s", f, call)
			}
		}
		if f == "client.go" && (strings.Contains(string(src), "ReadOnly: false") || !strings.Contains(string(src), "Select(name, &imap.SelectOptions{ReadOnly: true})")) {
			t.Errorf("%s opens a folder for writing", f)
		}
	}
}
