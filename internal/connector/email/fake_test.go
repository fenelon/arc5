package email

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/fenelon/arc5/internal/mail"
)

// fakeServer is a mailbox the fake client reads, which tests change under it.
type fakeServer struct {
	mu       sync.Mutex
	password string
	order    []string
	folders  map[string]*fakeFolder
	changed  chan struct{}
	dials    int
	full     []string // "folder/uid" of every whole-message fetch
	dialErr  error
	listErr  error
	// refuseFetch makes fetching "folder/uid" fail as Gmail's "NO Some
	// messages could not be FETCHed" does; refuseSelect, opening a folder.
	refuseFetch  map[string]bool
	refuseSelect map[string]bool
	noUIDNext    bool              // the server leaves UIDNEXT out, as RFC 3501 allows
	onSelect     func(name string) // called as a folder is opened
	idles        int
	searches     []string // "folder from:to" of every search
}

// startAt makes a folder's UIDs start at uid, as a long-used mailbox's do.
func (s *fakeServer) startAt(folder string, uid uint32) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.folders[folder].next = uid
}

var errFakeRefused = fmt.Errorf("%w: NO Some messages could not be FETCHed", errServerRefused)

type fakeFolder struct {
	attrs    []string
	validity uint32
	next     uint32
	msgs     []fakeMessage
}

type fakeMessage struct {
	uid      uint32
	raw      []byte
	received time.Time
}

func newFakeServer(folders ...string) *fakeServer {
	s := &fakeServer{password: "app-password", folders: map[string]*fakeFolder{}, changed: make(chan struct{}, 1)}
	for _, f := range folders {
		name, attrs, _ := strings.Cut(f, ` \`) // attributes start with a backslash
		if attrs != "" {
			attrs = `\` + attrs
		}
		s.order = append(s.order, name)
		s.folders[name] = &fakeFolder{attrs: strings.Fields(attrs), validity: 7, next: 1}
	}
	return s
}

func (s *fakeServer) poke() {
	select {
	case s.changed <- struct{}{}:
	default:
	}
}

// add puts a message in a folder and returns its UID.
func (s *fakeServer) add(folder, raw string, received time.Time) uint32 {
	s.mu.Lock()
	defer s.mu.Unlock()
	f := s.folders[folder]
	uid := f.next
	f.next++
	f.msgs = append(f.msgs, fakeMessage{uid: uid, raw: []byte(strings.ReplaceAll(raw, "\n", "\r\n")), received: received})
	s.poke()
	return uid
}

func (s *fakeServer) expunge(folder string, uid uint32) {
	s.mu.Lock()
	defer s.mu.Unlock()
	f := s.folders[folder]
	for i, m := range f.msgs {
		if m.uid == uid {
			f.msgs = append(f.msgs[:i], f.msgs[i+1:]...)
			break
		}
	}
	s.poke()
}

// renumber gives a folder a new UIDVALIDITY and new UIDs, as a server
// rebuilding its index does.
func (s *fakeServer) renumber(folder string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	f := s.folders[folder]
	f.validity++
	f.next = 100
	for i := range f.msgs {
		f.msgs[i].uid = f.next
		f.next++
	}
	s.poke()
}

func (s *fakeServer) fetches() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.full...)
}

func (s *fakeServer) dial(_ context.Context, a account) (client, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.dials++
	if s.dialErr != nil {
		return nil, s.dialErr
	}
	if a.Password != s.password {
		return nil, errLoginRefused
	}
	return &fakeClient{s: s}, nil
}

type fakeClient struct {
	s   *fakeServer
	sel string
}

func (c *fakeClient) List(context.Context) ([]mailbox, error) {
	c.s.mu.Lock()
	defer c.s.mu.Unlock()
	if c.s.listErr != nil {
		return nil, c.s.listErr
	}
	var out []mailbox
	for _, name := range c.s.order {
		out = append(out, mailbox{Name: name, Attrs: c.s.folders[name].attrs})
	}
	return out, nil
}

func (c *fakeClient) Select(_ context.Context, name string) (selected, error) {
	if c.s.onSelect != nil {
		c.s.onSelect(name)
	}
	c.s.mu.Lock()
	defer c.s.mu.Unlock()
	f, ok := c.s.folders[name]
	if !ok {
		return selected{}, fmt.Errorf("no folder %q", name)
	}
	if c.s.refuseSelect[name] {
		return selected{}, fmt.Errorf("%w: NO [CANNOT] this folder cannot be opened", errServerRefused)
	}
	c.sel = name
	next := f.next
	if c.s.noUIDNext {
		next = 0
	}
	return selected{UIDValidity: f.validity, UIDNext: next, Messages: uint32(len(f.msgs))}, nil
}

func (c *fakeClient) folder() *fakeFolder { return c.s.folders[c.sel] }

func (c *fakeClient) Search(_ context.Context, from, to uint32, since time.Time) ([]uint32, error) {
	c.s.mu.Lock()
	defer c.s.mu.Unlock()
	c.s.searches = append(c.s.searches, fmt.Sprintf("%s %d:%d", c.sel, from, to))
	var out []uint32
	for _, m := range c.folder().msgs {
		if m.uid >= from && (to == 0 || m.uid <= to) && (since.IsZero() || !m.received.Before(since)) {
			out = append(out, m.uid)
		}
	}
	return out, nil
}

func (c *fakeClient) message(uid uint32) (fakeMessage, bool) {
	for _, m := range c.folder().msgs {
		if m.uid == uid {
			return m, true
		}
	}
	return fakeMessage{}, false
}

func headerOf(raw []byte) []byte {
	if i := bytes.Index(raw, []byte("\r\n\r\n")); i >= 0 {
		return raw[:i+4]
	}
	return raw
}

func (c *fakeClient) Headers(_ context.Context, uids []uint32) ([]header, error) {
	c.s.mu.Lock()
	defer c.s.mu.Unlock()
	var out []header
	for _, uid := range uids {
		m, ok := c.message(uid)
		if !ok {
			continue
		}
		if c.s.refuseFetch[fmt.Sprintf("%s/%d", c.sel, uid)] && len(uids) > 1 {
			return nil, errFakeRefused // the whole command fails, as on Gmail
		}
		h := header{UID: uid, Size: int64(len(m.raw)), Received: m.received}
		for _, line := range strings.Split(string(headerOf(m.raw)), "\r\n") {
			if k, v, ok := strings.Cut(line, ":"); ok && strings.EqualFold(k, "Message-ID") {
				h.MessageID = mail.HeaderID(v)
			}
		}
		out = append(out, h)
	}
	return out, nil
}

func (c *fakeClient) Fetch(_ context.Context, uid uint32, headersOnly bool) ([]byte, error) {
	c.s.mu.Lock()
	defer c.s.mu.Unlock()
	m, ok := c.message(uid)
	if !ok {
		return nil, errGone
	}
	if c.s.refuseFetch[fmt.Sprintf("%s/%d", c.sel, uid)] {
		return nil, errFakeRefused
	}
	if headersOnly {
		return headerOf(m.raw), nil
	}
	c.s.full = append(c.s.full, fmt.Sprintf("%s/%d", c.sel, uid))
	return m.raw, nil
}

func (c *fakeClient) Idle(ctx context.Context, wait time.Duration) error {
	c.s.mu.Lock()
	c.s.idles++
	c.s.mu.Unlock()
	select {
	case <-c.s.changed:
		return nil
	case <-time.After(wait):
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (c *fakeClient) Close() error { return nil }

// memState is a connector's State in memory.
type memState struct {
	mu sync.Mutex
	m  map[string][]byte
}

func newMemState() *memState { return &memState{m: map[string][]byte{}} }

func (s *memState) Get(key string) ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.m[key], nil
}

func (s *memState) Set(key string, value []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.m[key] = value
	return nil
}
