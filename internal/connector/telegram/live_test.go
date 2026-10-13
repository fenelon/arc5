package telegram

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/gotd/td/tg"

	"github.com/fenelon/arc5/internal/connector"
	"github.com/fenelon/arc5/internal/model"
)

// liveRun starts the connector with the chat list and history given, waits
// until it is live, and returns its dialer, whose listener the test drives.
func liveRun(t *testing.T, h *history, sink *memSink, state *memState) (*fakeDialer, func() error) {
	t.Helper()
	d := &fakeDialer{conn: &fakeConn{dialogs: chatList(), history: h.fetch}}
	c := newTestConnector(d)
	c.sleep = (&sleeps{}).sleep
	c.now = func() time.Time { return day0 }
	stop := running(t, c, sink, state, nil)
	eventually(t, "live", func() bool { return sink.lastProgress().Phase == connector.PhaseLive })
	return d, stop
}

func push(t *testing.T, d *fakeDialer, updates ...tg.UpdateClass) error {
	t.Helper()
	e := fixture()
	users := []tg.UserClass{e.users[1], e.users[2], e.users[4], e.users[self]}
	chats := []tg.ChatClass{e.chats[10], e.channels[20], e.channels[21]}
	return d.listener.handler.Handle(context.Background(), &tg.Updates{Updates: updates, Users: users, Chats: chats})
}

func TestLiveStoresNewMessagesAndEdits(t *testing.T) {
	sink := newMemSink()
	d, stop := liveRun(t, &history{counts: map[string]int{}}, sink, signedIn(t))
	defer stop()
	msg := func(peer tg.PeerClass, id int, text string) *tg.Message {
		return &tg.Message{ID: id, PeerID: peer, FromID: &tg.PeerUser{UserID: 1}, Date: int(day0.Unix()), Message: text}
	}
	if err := push(t, d,
		&tg.UpdateNewMessage{Message: msg(&tg.PeerUser{UserID: 1}, 60, "hello")},
		&tg.UpdateNewChannelMessage{Message: msg(&tg.PeerChannel{ChannelID: 20}, 901, "in the supergroup")},
		&tg.UpdateNewChannelMessage{Message: msg(&tg.PeerChannel{ChannelID: 21}, 801, "news nobody archives")},
		&tg.UpdateNewMessage{Message: msg(&tg.PeerUser{UserID: 2}, 61, "a new chat")},
	); err != nil {
		t.Fatal(err)
	}
	if err := push(t, d, &tg.UpdateEditMessage{Message: msg(&tg.PeerUser{UserID: 1}, 60, "hello, edited")}); err != nil {
		t.Fatal(err)
	}
	for chat, want := range map[string][]string{
		"user:1": {"60:hello, edited"}, "channel:20": {"901:in the supergroup"}, "channel:21": nil, "user:2": {"61:a new chat"},
	} {
		if got := sink.texts(chat); !slices.Equal(got, want) {
			t.Errorf("%s = %v, want %v", chat, got, want)
		}
	}
	if sink.chats["user:2"].Title != "@marina_s" {
		t.Errorf("the new chat = %+v", sink.chats["user:2"])
	}
	if sink.senders["user:1"].DisplayName != "Dan Whitfield" {
		t.Errorf("senders = %v", sink.senders)
	}
	renamed := &tg.Updates{Chats: []tg.ChatClass{&tg.Chat{ID: 10, Title: "Familia Ruiz", Photo: &tg.ChatPhotoEmpty{}}}}
	if err := d.listener.handler.Handle(context.Background(), renamed); err != nil {
		t.Fatal(err)
	}
	sink.mu.Lock()
	title := sink.chats["chat:10"].Title
	sink.mu.Unlock()
	if title != "Familia Ruiz" {
		t.Errorf("a renamed group = %q", title)
	}
}

func TestLiveDeletions(t *testing.T) {
	sink := newMemSink()
	d, stop := liveRun(t, &history{counts: map[string]int{"user:1": 5, "chat:10": 5, "channel:20": 5}}, sink, signedIn(t))
	defer stop()
	eventually(t, "the history", func() bool { return len(sink.texts("channel:20")) == 5 })

	// Message 5 exists in all three chats; a deletion without a chat names
	// only chats whose IDs are numbered across the account.
	if err := push(t, d, &tg.UpdateDeleteMessages{Messages: []int{5}}); err != nil {
		t.Fatal(err)
	}
	if !sink.deleted["user:1/5"] || !sink.deleted["chat:10/5"] || sink.deleted["channel:20/5"] {
		t.Errorf("deleted = %v", sink.deleted)
	}
	if err := push(t, d, &tg.UpdateDeleteChannelMessages{ChannelID: 20, Messages: []int{4, 5}}); err != nil {
		t.Fatal(err)
	}
	if !sink.deleted["channel:20/4"] || !sink.deleted["channel:20/5"] {
		t.Errorf("channel deletions = %v", sink.deleted)
	}
	if err := push(t, d, &tg.UpdateDeleteChannelMessages{ChannelID: 999, Messages: []int{1}}); err != nil {
		t.Errorf("a deletion in a chat the archive does not have: %v", err)
	}
}

func TestLiveStorageFailureKeepsTheUpdateForLater(t *testing.T) {
	sink, state := newMemSink(), signedIn(t)
	d, stop := liveRun(t, &history{counts: map[string]int{}}, sink, state)
	st := d.listener.storage
	if err := st.SetState(context.Background(), self, stateAt(100)); err != nil {
		t.Fatal(err)
	}
	sink.mu.Lock()
	sink.failWrite = connector.ErrStorage
	sink.mu.Unlock()
	err := push(t, d, &tg.UpdateNewMessage{Message: &tg.Message{ID: 70, PeerID: &tg.PeerUser{UserID: 1}, Date: int(day0.Unix()), Message: "lost?"}})
	if !errors.Is(err, connector.ErrStorage) {
		t.Errorf("Handle = %v", err)
	}
	// gotd saves the new position after a failed handler; the storage must refuse.
	if err := st.SetPts(context.Background(), self, 101); err == nil {
		t.Error("the update position moved past an update that was not stored")
	}
	if got, _, _ := st.GetState(context.Background(), self); got.Pts != 100 {
		t.Errorf("stored pts = %d", got.Pts)
	}
	if err := stop(); !errors.Is(err, connector.ErrStorage) {
		t.Errorf("Run = %v, want the storage failure", err)
	}
}

func TestCatchUpAfterALongDisconnection(t *testing.T) {
	h := &history{counts: map[string]int{"user:1": 3, "chat:10": 2, "channel:20": 1}}
	sink, state := newMemSink(), signedIn(t)
	d, stop := liveRun(t, h, sink, state)
	defer stop()
	eventually(t, "the history", func() bool { return len(sink.texts("user:1")) == 3 })
	h.mu.Lock()
	h.counts["user:1"], h.counts["chat:10"] = 250, 4
	h.calls = nil
	h.mu.Unlock()

	d.listener.tooLong()
	eventually(t, "the catch-up", func() bool { return len(sink.texts("user:1")) == 250 && len(sink.texts("chat:10")) == 4 })
	if got := h.callsFor("user:1"); len(got) < 3 || got[0] != "user:1 0 3 100" {
		t.Errorf("catch-up requests = %v", got)
	}
	if top, _ := loadTop(state, "user:1"); top != 250 {
		t.Errorf("top = %d", top)
	}
	eventually(t, "the channel's history", func() bool { return len(sink.texts("channel:20")) == 1 })
	h.mu.Lock()
	h.counts["channel:20"] = 3
	h.mu.Unlock()
	d.listener.channelTooLong(20)
	eventually(t, "the channel's catch-up", func() bool { return len(sink.texts("channel:20")) == 3 })
	_ = model.ChatGroup
}

// Review finding: a catch-up fetched from the newest stored message when it
// ran, so a live message arriving first hid the gap; and the queue lived in
// memory, so a restart lost it.
func TestCatchUpSurvivesLiveMessagesAndRestarts(t *testing.T) {
	h := &history{counts: map[string]int{"user:1": 3}}
	sink, state := newMemSink(), signedIn(t)
	d, stop := liveRun(t, h, sink, state)
	eventually(t, "the history", func() bool { return len(sink.texts("user:1")) == 3 })
	h.mu.Lock()
	h.counts["user:1"] = 251
	h.failGaps = errors.New("network down")
	h.mu.Unlock()

	d.listener.tooLong()
	// A new message arrives live before the catch-up can run.
	if err := push(t, d, &tg.UpdateNewMessage{Message: &tg.Message{ID: 251, PeerID: &tg.PeerUser{UserID: 1}, Date: int(day0.Unix()), Message: "m251"}}); err != nil {
		t.Fatal(err)
	}
	stop() // a restart, such as saving a setting

	h.mu.Lock()
	h.failGaps = nil
	h.mu.Unlock()
	_, stop = liveRun(t, h, sink, state)
	defer stop()
	eventually(t, "the gap filled", func() bool { return len(sink.texts("user:1")) == 251 })
	eventually(t, "the gap marker gone", func() bool { g, _ := loadGaps(state); return len(g) == 0 })
}
