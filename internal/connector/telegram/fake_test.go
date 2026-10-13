package telegram

import (
	"context"
	"errors"
	"io"
	"slices"
	"strconv"
	"strings"
	"sync"

	"github.com/gotd/td/tg"

	"github.com/fenelon/arc5/internal/archive"
	"github.com/fenelon/arc5/internal/connector"
	"github.com/fenelon/arc5/internal/model"
)

// fakeConn is Telegram as the tests want it. Each behaviour is a function
// field; an unset one answers as a plain account would.
type fakeConn struct {
	mu        sync.Mutex
	self      *tg.User
	selfErr   error
	sendCode  func(phone string) (string, error)
	signIn    func(code string) error
	password  func(pw string) error
	logOut    func() error
	dialogs   func(offset dialogOffset) (dialogPage, error)
	history   func(peer tg.InputPeerClass, offsetID, minID, limit int) (historyPage, error)
	messages  func(peer tg.InputPeerClass, ids []int) (historyPage, error)
	download  func(loc tg.InputFileLocationClass, w io.Writer) error
	listen    func(ctx context.Context) error
	closed    int
	loggedOut int
	calls     []string
}

func (f *fakeConn) note(call string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, call)
}

func (f *fakeConn) Self(context.Context) (*tg.User, error) {
	f.note("self")
	if f.selfErr != nil {
		return nil, f.selfErr
	}
	if f.self == nil {
		return &tg.User{ID: self, Self: true, FirstName: "Me"}, nil
	}
	return f.self, nil
}

func (f *fakeConn) SendCode(_ context.Context, phone string) (string, error) {
	f.note("sendCode " + phone)
	if f.sendCode != nil {
		return f.sendCode(phone)
	}
	return "hash", nil
}

func (f *fakeConn) SignIn(_ context.Context, _, code, _ string) error {
	f.note("signIn " + code)
	if f.signIn != nil {
		return f.signIn(code)
	}
	return nil
}

func (f *fakeConn) CheckPassword(_ context.Context, pw string) error {
	f.note("password")
	if f.password != nil {
		return f.password(pw)
	}
	return nil
}

func (f *fakeConn) LogOut(context.Context) error {
	f.mu.Lock()
	f.loggedOut++
	f.mu.Unlock()
	if f.logOut != nil {
		return f.logOut()
	}
	return nil
}

func (f *fakeConn) Dialogs(_ context.Context, offset dialogOffset) (dialogPage, error) {
	if f.dialogs != nil {
		return f.dialogs(offset)
	}
	return dialogPage{}, nil
}

func (f *fakeConn) History(_ context.Context, peer tg.InputPeerClass, offsetID, minID, limit int) (historyPage, error) {
	if f.history != nil {
		return f.history(peer, offsetID, minID, limit)
	}
	return historyPage{}, nil
}

func (f *fakeConn) Messages(_ context.Context, peer tg.InputPeerClass, ids []int) (historyPage, error) {
	if f.messages != nil {
		return f.messages(peer, ids)
	}
	return historyPage{}, nil
}

func (f *fakeConn) Download(_ context.Context, loc tg.InputFileLocationClass, w io.Writer) error {
	if f.download != nil {
		return f.download(loc, w)
	}
	return errors.New("no files here")
}

func (f *fakeConn) Listen(ctx context.Context, _ int64) error {
	if f.listen != nil {
		return f.listen(ctx)
	}
	<-ctx.Done()
	return ctx.Err()
}

func (f *fakeConn) Close() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.closed++
}

// fakeDialer hands out conn and remembers what it was dialled with.
type fakeDialer struct {
	conn     *fakeConn
	err      error
	mu       sync.Mutex
	dials    []credentials
	listener listener
}

func (d *fakeDialer) dial(_ context.Context, creds credentials, _ connector.State, l listener) (conn, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.dials = append(d.dials, creds)
	d.listener = l
	if d.err != nil {
		return nil, d.err
	}
	return d.conn, nil
}

// memSink is the archive as a connector sees it, in memory.
type memSink struct {
	mu        sync.Mutex
	chats     map[string]model.Chat
	archived  map[string]bool
	senders   map[string]model.Sender
	messages  map[string]model.Message // by chat and message source ID
	deleted   map[string]bool
	media     map[model.AttachmentRef][]byte
	statuses  map[model.AttachmentRef]string
	avatars   map[model.AvatarRef][]byte
	requests  chan model.AttachmentRef
	progress  []connector.Progress
	failWrite error
	failMedia error // PutMedia reads a little, then fails with this
}

func newMemSink() *memSink {
	return &memSink{
		chats: map[string]model.Chat{}, archived: map[string]bool{}, senders: map[string]model.Sender{},
		messages: map[string]model.Message{}, deleted: map[string]bool{}, media: map[model.AttachmentRef][]byte{},
		statuses: map[model.AttachmentRef]string{}, avatars: map[model.AvatarRef][]byte{}, requests: make(chan model.AttachmentRef, 10),
	}
}

func key(chat, id string) string { return chat + "/" + id }

func (s *memSink) UpsertChat(c model.Chat) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.failWrite != nil {
		return s.failWrite
	}
	if _, known := s.chats[c.SourceID]; !known {
		s.archived[c.SourceID] = c.Kind.ArchivedByDefault()
	}
	s.chats[c.SourceID] = c
	return nil
}

func (s *memSink) UpsertSender(sn model.Sender) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.senders[sn.SourceID] = sn
	return nil
}

func (s *memSink) UpsertMessage(m model.Message) ([]model.AttachmentRef, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.failWrite != nil {
		return nil, s.failWrite
	}
	if _, known := s.chats[m.ChatSourceID]; !known {
		return nil, archive.ErrUnknownChat
	}
	if old, ok := s.messages[key(m.ChatSourceID, m.SourceID)]; ok && len(m.Attachments) == 0 {
		m.Attachments = old.Attachments
	}
	s.messages[key(m.ChatSourceID, m.SourceID)] = m
	var refs []model.AttachmentRef
	for i, a := range m.Attachments {
		ref := model.AttachmentRef{ChatSourceID: m.ChatSourceID, MessageSourceID: m.SourceID, Position: i}
		if !a.NoFetch && s.media[ref] == nil && s.statuses[ref] == "" {
			refs = append(refs, ref)
		}
	}
	return refs, nil
}

func (s *memSink) MarkDeleted(chat string, ids []string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, id := range ids {
		s.deleted[key(chat, id)] = true
	}
	return nil
}

func (s *memSink) LookupChats(id string, kinds []model.ChatKind) ([]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []string
	for k, m := range s.messages {
		if m.SourceID == id && slices.Contains(kinds, s.chats[m.ChatSourceID].Kind) {
			out = append(out, strings.TrimSuffix(k, "/"+id))
		}
	}
	slices.Sort(out)
	return out, nil
}

func (s *memSink) PutMedia(ref model.AttachmentRef, r io.Reader) error {
	s.mu.Lock()
	fail := s.failMedia
	s.mu.Unlock()
	if fail != nil {
		io.ReadFull(r, make([]byte, 2))
		return fail
	}
	data, err := io.ReadAll(r)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.media[ref] = data
	return nil
}

func (s *memSink) SetAttachmentStatus(ref model.AttachmentRef, status model.AttachmentStatus, reason string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.statuses[ref] = string(status) + ": " + reason
	return nil
}

func (s *memSink) ChatArchived(chat string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.archived[chat], nil
}

func (s *memSink) RequestedMedia() <-chan model.AttachmentRef { return s.requests }

func (s *memSink) PendingMedia() ([]model.AttachmentRef, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []model.AttachmentRef
	for _, m := range s.messages {
		for i, a := range m.Attachments {
			ref := model.AttachmentRef{ChatSourceID: m.ChatSourceID, MessageSourceID: m.SourceID, Position: i}
			if !a.NoFetch && s.media[ref] == nil && s.statuses[ref] == "" {
				out = append(out, ref)
			}
		}
	}
	return out, nil
}

func (s *memSink) ReportProgress(p connector.Progress) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.progress = append(s.progress, p)
}

func (s *memSink) AvatarStored(ref model.AvatarRef) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.avatars[ref] != nil, nil
}

func (s *memSink) PutAvatar(ref model.AvatarRef, r io.Reader) error {
	data, err := io.ReadAll(r)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.avatars[ref] = data
	return nil
}

// lastProgress is the newest progress report.
func (s *memSink) lastProgress() connector.Progress {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.progress) == 0 {
		return connector.Progress{}
	}
	return s.progress[len(s.progress)-1]
}

// texts lists a chat's stored messages by ID, as "id:text".
func (s *memSink) texts(chat string) []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []string
	for _, m := range s.messages {
		if m.ChatSourceID == chat {
			out = append(out, m.SourceID+":"+m.Text)
		}
	}
	slices.SortFunc(out, func(a, b string) int {
		ai, _ := strconv.Atoi(strings.SplitN(a, ":", 2)[0])
		bi, _ := strconv.Atoi(strings.SplitN(b, ":", 2)[0])
		return ai - bi
	})
	return out
}
