package telegram

import (
	"context"
	"errors"
	"io"
	"sync"

	"github.com/gotd/td/tg"

	"github.com/fenelon/arc5/internal/connector"
)

// fakeConn is Telegram as the tests want it. Each behaviour is a function
// field; an unset one answers as a plain account would.
type fakeConn struct {
	mu        sync.Mutex
	self      *tg.User
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
