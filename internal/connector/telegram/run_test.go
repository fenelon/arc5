package telegram

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgerr"

	"github.com/fenelon/arc5/internal/connector"
	"github.com/fenelon/arc5/internal/model"
)

// signedIn gives a state the keys and session of a logged-in source.
func signedIn(t *testing.T) *memState {
	t.Helper()
	state := newMemState()
	if err := saveCredentials(state, credentials{ID: 1, Hash: "0123456789abcdef0123456789abcdef"}); err != nil {
		t.Fatal(err)
	}
	if err := state.Set(keySession, []byte("session")); err != nil {
		t.Fatal(err)
	}
	if err := saveSelf(state, self); err != nil {
		t.Fatal(err)
	}
	return state
}

// eventually waits up to two seconds for cond.
func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// running starts Run and returns a stop function that cancels it and
// returns its error.
func running(t *testing.T, c *Connector, sink connector.Sink, state connector.State, settings map[string]string) func() error {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- c.Run(ctx, sink, state, settings) }()
	return func() error {
		cancel()
		select {
		case err := <-done:
			return err
		case <-time.After(5 * time.Second):
			t.Fatal("Run did not stop")
			return nil
		}
	}
}

func TestRunNeedsLogin(t *testing.T) {
	ctx := context.Background()
	c := newTestConnector(&fakeDialer{conn: &fakeConn{}})
	if err := c.Run(ctx, newMemSink(), newMemState(), nil); !errors.Is(err, connector.ErrNeedsLogin) {
		t.Errorf("no keys: %v", err)
	}
	state := signedIn(t)
	state.Set(keySession, nil)
	if err := c.Run(ctx, newMemSink(), state, nil); !errors.Is(err, connector.ErrNeedsLogin) {
		t.Errorf("no session: %v", err)
	}
	c = newTestConnector(&fakeDialer{conn: &fakeConn{selfErr: tgerr.New(401, "SESSION_REVOKED")}})
	if err := c.Run(ctx, newMemSink(), signedIn(t), nil); !errors.Is(err, connector.ErrNeedsLogin) {
		t.Errorf("a revoked session: %v", err)
	}
}

// chatList is the fixture's chat list in two pages.
func chatList() func(offset dialogOffset) (dialogPage, error) {
	e := fixture()
	users := []tg.UserClass{e.users[1], e.users[4], e.users[self]}
	chats := []tg.ChatClass{e.chats[10], e.channels[20], e.channels[21]}
	top := func(peer tg.PeerClass, id, date int) tg.MessageClass {
		return &tg.Message{ID: id, PeerID: peer, Date: date}
	}
	return func(offset dialogOffset) (dialogPage, error) {
		if offset.ID == 0 {
			return dialogPage{
				Dialogs:  []*tg.Dialog{{Peer: &tg.PeerUser{UserID: 1}, TopMessage: 50}, {Peer: &tg.PeerUser{UserID: 4}, TopMessage: 40}},
				Messages: []tg.MessageClass{top(&tg.PeerUser{UserID: 1}, 50, 5000), top(&tg.PeerUser{UserID: 4}, 40, 4000)},
				Users:    users, Chats: chats,
				Next: &dialogOffset{ID: 40, Date: 4000, Peer: &tg.InputPeerUser{UserID: 4, AccessHash: 14}},
			}, nil
		}
		return dialogPage{
			Dialogs: []*tg.Dialog{{Peer: &tg.PeerChat{ChatID: 10}, TopMessage: 30}, {Peer: &tg.PeerChannel{ChannelID: 20}, TopMessage: 900},
				{Peer: &tg.PeerChannel{ChannelID: 21}, TopMessage: 800}},
			Messages: []tg.MessageClass{top(&tg.PeerChat{ChatID: 10}, 30, 3000), top(&tg.PeerChannel{ChannelID: 20}, 900, 2000), top(&tg.PeerChannel{ChannelID: 21}, 800, 1000)},
			Users:    users, Chats: chats,
		}, nil
	}
}

func TestRunSyncsTheChatList(t *testing.T) {
	fc := &fakeConn{dialogs: chatList()}
	sink, state := newMemSink(), signedIn(t)
	stop := running(t, newTestConnector(&fakeDialer{conn: fc}), sink, state, map[string]string{"paused": "true"})
	eventually(t, "the chat list", func() bool { return sink.lastProgress().Phase == connector.PhaseLive })
	if err := stop(); !errors.Is(err, context.Canceled) {
		t.Errorf("Run after cancel = %v", err)
	}
	want := map[string]model.ChatKind{"user:1": model.ChatPrivate, "user:4": model.ChatBot, "chat:10": model.ChatGroup,
		"channel:20": model.ChatGroup, "channel:21": model.ChatChannel}
	for id, kind := range want {
		if sink.chats[id].Kind != kind {
			t.Errorf("chat %s = %+v, want %s", id, sink.chats[id], kind)
		}
	}
	if len(sink.chats) != len(want) {
		t.Errorf("chats = %v", sink.chats)
	}
	if sink.progress[0].Phase != connector.PhaseConnecting {
		t.Errorf("first progress = %+v", sink.progress[0])
	}
	if h, found, _ := newUpdateStorage(state).GetChannelAccessHash(context.Background(), self, 20); !found || h != 21 {
		t.Errorf("the channel's access hash was not kept: %d, %v", h, found)
	}
	if h, found, _ := newUpdateStorage(state).GetUserAccessHash(context.Background(), self, 1); !found || h != 11 {
		t.Errorf("the user's access hash was not kept: %d, %v", h, found)
	}
	if fc.closed != 1 {
		t.Errorf("the connection was closed %d times", fc.closed)
	}
}

func TestRunStopsOnStorageFailure(t *testing.T) {
	sink := newMemSink()
	sink.failWrite = connector.ErrStorage
	c := newTestConnector(&fakeDialer{conn: &fakeConn{dialogs: chatList()}})
	if err := c.Run(context.Background(), sink, signedIn(t), nil); !errors.Is(err, connector.ErrStorage) {
		t.Errorf("a full disk: %v", err)
	}
}

func TestSettings(t *testing.T) {
	fields := New().Settings()
	names := map[string]connector.Field{}
	for _, f := range fields {
		names[f.Name] = f
	}
	if f := names["age"]; f.Kind != connector.FieldChoice || f.Default != "All history" {
		t.Errorf("age = %+v", f)
	}
	if f := names["speed"]; f.Kind != connector.FieldChoice || f.Default != "Normal" || len(f.Choices) != 3 {
		t.Errorf("speed = %+v", f)
	}
	for _, name := range []string{"paused", "keep_ephemeral"} {
		if f := names[name]; f.Kind != connector.FieldBool || f.Default != "" {
			t.Errorf("%s = %+v, want an unticked checkbox", name, f)
		}
	}
	o := readOptions(map[string]string{"age": "Since a date", "since": "2024-03-01", "speed": "Fast", "keep_ephemeral": "true"}, time.Now)
	if !o.keepEphemeral || o.pause != 500*time.Millisecond || !o.oldest.Equal(time.Date(2024, 3, 1, 0, 0, 0, 0, time.UTC)) {
		t.Errorf("options = %+v", o)
	}
	now := func() time.Time { return time.Date(2026, 10, 13, 0, 0, 0, 0, time.UTC) }
	if o := readOptions(map[string]string{"age": "Last 3 months"}, now); !o.oldest.Equal(time.Date(2026, 7, 13, 0, 0, 0, 0, time.UTC)) || o.pause != 2*time.Second {
		t.Errorf("default speed and a 3-month limit = %+v", o)
	}
	if o := readOptions(map[string]string{"age": "Since a date", "since": "nonsense"}, now); !o.oldest.IsZero() {
		t.Errorf("a bad date limits history: %+v", o)
	}
}
