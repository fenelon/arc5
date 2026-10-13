package telegram

import (
	"context"
	"crypto/rand"
	"errors"
	"io"
	"runtime"
	"strings"
	"time"

	"github.com/gotd/td/telegram"
	"github.com/gotd/td/telegram/auth"
	"github.com/gotd/td/telegram/downloader"
	"github.com/gotd/td/telegram/updates"
	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgerr"

	"github.com/fenelon/arc5/internal/connector"
)

// api is everything the connector asks of Telegram. It is narrow on purpose:
// it reads, and it has no way to report media as opened, which would start a
// self-destruct countdown on the sender's side. A test in api_test.go keeps
// it that way.
type api interface {
	Self(ctx context.Context) (*tg.User, error)
	// SendCode asks Telegram to send a login code and returns the hash the
	// sign-in needs; errSignedIn means the session is already signed in.
	SendCode(ctx context.Context, phone string) (string, error)
	// SignIn returns errPasswordNeeded for an account with a two-step password.
	SignIn(ctx context.Context, phone, code, hash string) error
	CheckPassword(ctx context.Context, password string) error
	LogOut(ctx context.Context) error
	Dialogs(ctx context.Context, offset dialogOffset) (dialogPage, error)
	// History pages backwards from offsetID (0 for the newest), returning
	// only messages newer than minID (0 for no bound).
	History(ctx context.Context, peer tg.InputPeerClass, offsetID, minID, limit int) (historyPage, error)
	Messages(ctx context.Context, peer tg.InputPeerClass, ids []int) (historyPage, error)
	Download(ctx context.Context, loc tg.InputFileLocationClass, w io.Writer) error
}

// conn is a connection to Telegram: the api, live updates, and its end.
type conn interface {
	api
	// Listen delivers live updates to the dial's listener until ctx ends.
	Listen(ctx context.Context, selfID int64) error
	Close()
}

// listener receives live updates, and hears when Telegram cannot replay
// what was missed while disconnected.
type listener struct {
	handler        telegram.UpdateHandler // nil for a connection that only logs in
	tooLong        func()
	channelTooLong func(channelID int64)
}

// dialer connects to Telegram with the account's keys and session.
type dialer func(ctx context.Context, creds credentials, state connector.State, l listener) (conn, error)

var (
	errPasswordNeeded = errors.New("the account has a two-step password")
	errSignedIn       = errors.New("already signed in")
)

const dialogsPerPage = 100

// dialogOffset is where a page of the chat list starts.
type dialogOffset struct {
	Date, ID int
	Peer     tg.InputPeerClass
}

type dialogPage struct {
	Dialogs  []*tg.Dialog
	Messages []tg.MessageClass // each dialog's newest message
	Users    []tg.UserClass
	Chats    []tg.ChatClass
	Next     *dialogOffset // nil on the last page
}

type historyPage struct {
	Messages []tg.MessageClass
	Users    []tg.UserClass
	Chats    []tg.ChatClass
}

// client is the api over gotd's generated client.
type client struct {
	raw  *tg.Client
	auth *auth.Client
}

func newClient(inv tg.Invoker, creds credentials) *client {
	raw := tg.NewClient(inv)
	return &client{raw: raw, auth: auth.NewClient(raw, rand.Reader, creds.ID, creds.Hash)}
}

func (c *client) Self(ctx context.Context) (*tg.User, error) {
	users, err := c.raw.UsersGetUsers(ctx, []tg.InputUserClass{&tg.InputUserSelf{}})
	if err != nil {
		return nil, err
	}
	for _, u := range users {
		if u, ok := u.(*tg.User); ok {
			return u, nil
		}
	}
	return nil, errors.New("telegram did not describe the signed-in user")
}

func (c *client) SendCode(ctx context.Context, phone string) (string, error) {
	sent, err := c.auth.SendCode(ctx, phone, auth.SendCodeOptions{})
	if err != nil {
		return "", err
	}
	switch s := sent.(type) {
	case *tg.AuthSentCode:
		return s.PhoneCodeHash, nil
	case *tg.AuthSentCodeSuccess:
		return "", errSignedIn
	}
	return "", errors.New("telegram answered the login request in a way arc5 does not know")
}

func (c *client) SignIn(ctx context.Context, phone, code, hash string) error {
	_, err := c.auth.SignIn(ctx, phone, code, hash)
	if errors.Is(err, auth.ErrPasswordAuthNeeded) {
		return errPasswordNeeded
	}
	return err
}

func (c *client) CheckPassword(ctx context.Context, password string) error {
	_, err := c.auth.Password(ctx, password)
	return err
}

func (c *client) LogOut(ctx context.Context) error {
	_, err := c.raw.AuthLogOut(ctx)
	return err
}

func (c *client) Dialogs(ctx context.Context, offset dialogOffset) (dialogPage, error) {
	peer := offset.Peer
	if peer == nil {
		peer = &tg.InputPeerEmpty{}
	}
	res, err := c.raw.MessagesGetDialogs(ctx, &tg.MessagesGetDialogsRequest{
		OffsetDate: offset.Date, OffsetID: offset.ID, OffsetPeer: peer, Limit: dialogsPerPage,
	})
	if err != nil {
		return dialogPage{}, err
	}
	var (
		page  dialogPage
		raw   []tg.DialogClass
		total int
	)
	switch r := res.(type) {
	case *tg.MessagesDialogs:
		raw, page.Messages, page.Users, page.Chats = r.Dialogs, r.Messages, r.Users, r.Chats
	case *tg.MessagesDialogsSlice:
		raw, page.Messages, page.Users, page.Chats, total = r.Dialogs, r.Messages, r.Users, r.Chats, r.Count
	}
	for _, d := range raw {
		if d, ok := d.(*tg.Dialog); ok {
			page.Dialogs = append(page.Dialogs, d)
		}
	}
	if total > len(raw) && len(page.Dialogs) > 0 {
		page.Next = nextDialogs(page)
	}
	return page, nil
}

// nextDialogs is the offset after a page: its last dialog's newest message.
func nextDialogs(page dialogPage) *dialogOffset {
	last := page.Dialogs[len(page.Dialogs)-1]
	e := newEntities(page.Users, page.Chats)
	peer, ok := e.inputPeer(last.Peer)
	if !ok {
		return nil
	}
	next := &dialogOffset{ID: last.TopMessage, Peer: peer}
	for _, m := range page.Messages {
		if m.GetID() == last.TopMessage && peerSourceID(messagePeer(m)) == peerSourceID(last.Peer) {
			if msg, ok := m.(interface{ GetDate() int }); ok {
				next.Date = msg.GetDate()
			}
		}
	}
	return next
}

// messagePeer is the chat a message belongs to.
func messagePeer(m tg.MessageClass) tg.PeerClass {
	switch m := m.(type) {
	case *tg.Message:
		return m.PeerID
	case *tg.MessageService:
		return m.PeerID
	}
	return nil
}

// inputPeer is how requests name a chat: with the access hash Telegram gave
// this account for it.
func (e *entities) inputPeer(p tg.PeerClass) (tg.InputPeerClass, bool) {
	switch p := p.(type) {
	case *tg.PeerUser:
		if u := e.users[p.UserID]; u != nil {
			return &tg.InputPeerUser{UserID: u.ID, AccessHash: u.AccessHash}, true
		}
	case *tg.PeerChat:
		return &tg.InputPeerChat{ChatID: p.ChatID}, true
	case *tg.PeerChannel:
		if c := e.channels[p.ChannelID]; c != nil {
			return &tg.InputPeerChannel{ChannelID: c.ID, AccessHash: c.AccessHash}, true
		}
	}
	return nil, false
}

func toPage(res tg.MessagesMessagesClass) historyPage {
	switch r := res.(type) {
	case *tg.MessagesMessages:
		return historyPage{Messages: r.Messages, Users: r.Users, Chats: r.Chats}
	case *tg.MessagesMessagesSlice:
		return historyPage{Messages: r.Messages, Users: r.Users, Chats: r.Chats}
	case *tg.MessagesChannelMessages:
		return historyPage{Messages: r.Messages, Users: r.Users, Chats: r.Chats}
	}
	return historyPage{}
}

func (c *client) History(ctx context.Context, peer tg.InputPeerClass, offsetID, minID, limit int) (historyPage, error) {
	res, err := c.raw.MessagesGetHistory(ctx, &tg.MessagesGetHistoryRequest{Peer: peer, OffsetID: offsetID, MinID: minID, Limit: limit})
	if err != nil {
		return historyPage{}, err
	}
	return toPage(res), nil
}

func (c *client) Messages(ctx context.Context, peer tg.InputPeerClass, ids []int) (historyPage, error) {
	refs := make([]tg.InputMessageClass, len(ids))
	for i, id := range ids {
		refs[i] = &tg.InputMessageID{ID: id}
	}
	var (
		res tg.MessagesMessagesClass
		err error
	)
	if ch, ok := peer.(*tg.InputPeerChannel); ok {
		res, err = c.raw.ChannelsGetMessages(ctx, &tg.ChannelsGetMessagesRequest{
			Channel: &tg.InputChannel{ChannelID: ch.ChannelID, AccessHash: ch.AccessHash}, ID: refs})
	} else {
		res, err = c.raw.MessagesGetMessages(ctx, refs)
	}
	if err != nil {
		return historyPage{}, err
	}
	return toPage(res), nil
}

func (c *client) Download(ctx context.Context, loc tg.InputFileLocationClass, w io.Writer) error {
	_, err := downloader.NewDownloader().Download(c.raw, loc).Stream(ctx, w)
	return err
}

// floodWait reports how long Telegram asks to wait before trying again.
func floodWait(err error) (time.Duration, bool) {
	return tgerr.AsFloodWait(err)
}

// unauthorized reports whether the session is missing, revoked or dead, so
// the owner has to log in again.
func unauthorized(err error) bool {
	return auth.IsUnauthorized(err) || tgerr.IsCode(err, 401) ||
		tgerr.Is(err, "AUTH_KEY_UNREGISTERED", "SESSION_REVOKED", "SESSION_EXPIRED", "USER_DEACTIVATED", "USER_DEACTIVATED_BAN", "AUTH_KEY_DUPLICATED")
}

// referenceExpired reports a file reference too old to download with; the
// message is fetched again for a fresh one.
func referenceExpired(err error) bool {
	e, ok := tgerr.As(err)
	return ok && strings.HasPrefix(e.Type, "FILE_REFERENCE_") && (strings.HasSuffix(e.Type, "_EXPIRED") || strings.HasSuffix(e.Type, "_INVALID"))
}

// fileGone reports a file Telegram no longer has.
func fileGone(err error) bool {
	return tgerr.Is(err, "FILE_ID_INVALID", "LOCATION_INVALID", "MEDIA_EMPTY", "MEDIA_INVALID", "FILE_REFERENCE_EMPTY")
}

// liveConn is a running gotd client.
type liveConn struct {
	*client
	tc     *telegram.Client
	gaps   *updates.Manager
	cancel context.CancelFunc
	done   chan error
}

// dialTelegram connects to Telegram. The connection lives until Close, not
// until ctx ends; ctx bounds only the connecting.
func dialTelegram(ctx context.Context, creds credentials, state connector.State, l listener) (conn, error) {
	opts := telegram.Options{
		SessionStorage: sessionStorage{state},
		Device:         telegram.DeviceConfig{DeviceModel: "arc5 archive", SystemVersion: runtime.GOOS, AppVersion: "arc5"},
		NoUpdates:      l.handler == nil,
	}
	var gaps *updates.Manager
	if l.handler != nil {
		st := newUpdateStorage(state)
		gaps = updates.New(updates.Config{
			Handler: l.handler, Storage: st, AccessHasher: st, UserAccessHasher: st,
			OnTooLong: l.tooLong, OnChannelTooLong: l.channelTooLong,
		})
		opts.UpdateHandler = gaps
	}
	tc := telegram.NewClient(creds.ID, creds.Hash, opts)
	runCtx, cancel := context.WithCancel(context.Background())
	ready, done := make(chan struct{}), make(chan error, 1)
	go func() {
		done <- tc.Run(runCtx, func(ctx context.Context) error {
			close(ready)
			<-ctx.Done()
			return ctx.Err()
		})
	}()
	select {
	case <-ready:
		return &liveConn{client: newClient(tc, creds), tc: tc, gaps: gaps, cancel: cancel, done: done}, nil
	case err := <-done:
		cancel()
		return nil, err
	case <-ctx.Done():
		cancel()
		<-done
		return nil, ctx.Err()
	}
}

func (c *liveConn) Listen(ctx context.Context, selfID int64) error {
	if c.gaps == nil {
		<-ctx.Done()
		return ctx.Err()
	}
	return c.gaps.Run(ctx, c.tc.API(), selfID, updates.AuthOptions{})
}

func (c *liveConn) Close() {
	c.cancel()
	<-c.done
}
