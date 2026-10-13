package telegram

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgerr"

	"github.com/fenelon/arc5/internal/connector"
)

// inputSourceID is the source ID of the chat a request names.
func inputSourceID(p tg.InputPeerClass) string {
	switch p := p.(type) {
	case *tg.InputPeerUser:
		return fmt.Sprintf("user:%d", p.UserID)
	case *tg.InputPeerChat:
		return fmt.Sprintf("chat:%d", p.ChatID)
	case *tg.InputPeerChannel:
		return fmt.Sprintf("channel:%d", p.ChannelID)
	}
	return ""
}

// history is Telegram's store of messages for the fake: per chat, message
// IDs 1 to n, one a day ending on day 0.
type history struct {
	mu       sync.Mutex
	counts   map[string]int
	calls    []string // "chat offset min limit"
	failNext error
}

var day0 = time.Date(2026, time.October, 1, 0, 0, 0, 0, time.UTC)

func (h *history) peerOf(chat string) tg.PeerClass {
	p, _ := sourcePeer(chat)
	return p
}

func (h *history) messageDate(chat string, id int) int {
	return int(day0.AddDate(0, 0, id-h.counts[chat]).Unix())
}

func (h *history) fetch(peer tg.InputPeerClass, offsetID, minID, limit int) (historyPage, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	chat := inputSourceID(peer)
	h.calls = append(h.calls, fmt.Sprintf("%s %d %d %d", chat, offsetID, minID, limit))
	if err := h.failNext; err != nil {
		h.failNext = nil
		return historyPage{}, err
	}
	top := h.counts[chat]
	if offsetID > 0 {
		top = offsetID - 1
	}
	var page historyPage
	for id := top; id > minID && id > 0 && len(page.Messages) < limit; id-- {
		page.Messages = append(page.Messages, &tg.Message{ID: id, PeerID: h.peerOf(chat), FromID: &tg.PeerUser{UserID: 1},
			Date: h.messageDate(chat, id), Message: fmt.Sprintf("m%d", id)})
	}
	e := fixture()
	page.Users = []tg.UserClass{e.users[1]}
	page.Chats = []tg.ChatClass{e.chats[10], e.channels[20], e.channels[21]}
	return page, nil
}

func (h *history) callsFor(chat string) []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	var out []string
	for _, c := range h.calls {
		if strings.HasPrefix(c, chat+" ") {
			out = append(out, c)
		}
	}
	return out
}

// sleeps records the connector's waits instead of waiting.
type sleeps struct {
	mu  sync.Mutex
	got []time.Duration
}

func (s *sleeps) sleep(ctx context.Context, d time.Duration) error {
	s.mu.Lock()
	s.got = append(s.got, d)
	s.mu.Unlock()
	return ctx.Err()
}

func (s *sleeps) all() []time.Duration {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.got)
}

// backfilled runs the connector until it reports it is live, and stops it.
func backfilled(t *testing.T, h *history, sink *memSink, state *memState, settings map[string]string) *sleeps {
	t.Helper()
	sink.mu.Lock()
	sink.progress = nil // wait for this run's report, not an earlier run's
	sink.mu.Unlock()
	sl := &sleeps{}
	c := newTestConnector(&fakeDialer{conn: &fakeConn{dialogs: chatList(), history: h.fetch}})
	c.sleep = sl.sleep
	c.now = func() time.Time { return day0 }
	stop := running(t, c, sink, state, settings)
	eventually(t, "the end of the catch-up", func() bool { return sink.lastProgress().Phase == connector.PhaseLive })
	stop()
	return sl
}

func TestBackfillPagesAndCheckpoints(t *testing.T) {
	h := &history{counts: map[string]int{"user:1": 250, "chat:10": 3}}
	sink, state := newMemSink(), signedIn(t)
	sl := backfilled(t, h, sink, state, nil)

	if got := sink.texts("user:1"); len(got) != 250 || got[0] != "1:m1" || got[249] != "250:m250" {
		t.Fatalf("stored %d messages of Dan's chat: %v…", len(got), got[:min(3, len(got))])
	}
	if got := h.callsFor("user:1"); !slices.Equal(got, []string{"user:1 0 0 100", "user:1 151 0 100", "user:1 51 0 100", "user:1 1 0 100"}) {
		t.Errorf("history requests = %v", got)
	}
	if cp, _ := loadCheckpoint(state, "user:1"); cp.Oldest != 1 || !cp.Start {
		t.Errorf("checkpoint = %+v", cp)
	}
	if len(sink.texts("chat:10")) != 3 {
		t.Errorf("the group's history = %v", sink.texts("chat:10"))
	}
	if calls := h.callsFor("channel:21"); len(calls) != 0 {
		t.Errorf("a channel not archived was fetched: %v", calls)
	}
	if calls := h.callsFor("user:4"); len(calls) != 0 {
		t.Errorf("a bot not archived was fetched: %v", calls)
	}
	// Most recently active first: Dan, then the group.
	if first := h.calls[0]; !strings.HasPrefix(first, "user:1 ") {
		t.Errorf("first request = %q", first)
	}
	for _, d := range sl.all() {
		if d != 2*time.Second {
			t.Errorf("a pause of %v at the normal speed", d)
		}
	}
	if len(sl.all()) < 6 {
		t.Errorf("only %d pauses between %d requests", len(sl.all()), len(h.calls))
	}
	var counted bool
	for _, p := range sink.progress {
		if p.Phase == connector.PhaseBackfill && p.ChatsTotal == 3 && p.ChatsDone == 1 {
			counted = true
		}
	}
	if !counted {
		t.Errorf("progress never counted chats: %+v", sink.progress)
	}
	if top, _ := loadTop(state, "user:1"); top != 250 {
		t.Errorf("top = %d", top)
	}
}

func TestBackfillResumesFromItsCheckpoint(t *testing.T) {
	h := &history{counts: map[string]int{"user:1": 250}}
	sink, state := newMemSink(), signedIn(t)
	if err := saveCheckpoint(state, "user:1", checkpoint{Oldest: 101, OldestDate: int64(h.messageDate("user:1", 101))}); err != nil {
		t.Fatal(err)
	}
	backfilled(t, h, sink, state, nil)
	if got := h.callsFor("user:1"); len(got) == 0 || got[0] != "user:1 101 0 100" {
		t.Errorf("history requests = %v", got)
	}
	if got := sink.texts("user:1"); len(got) != 100 || got[99] != "100:m100" {
		t.Errorf("stored %d: %v", len(got), got)
	}
	// A finished chat is not fetched again.
	h.calls = nil
	backfilled(t, h, newMemSink(), state, nil)
	if got := h.callsFor("user:1"); len(got) != 0 {
		t.Errorf("a finished chat was fetched again: %v", got)
	}
}

func TestBackfillAgeLimit(t *testing.T) {
	h := &history{counts: map[string]int{"user:1": 400}}
	sink, state := newMemSink(), signedIn(t)
	// day0 is message 400; "Last 3 months" reaches back to 1 July, message 308.
	backfilled(t, h, sink, state, map[string]string{"age": "Last 3 months"})
	got := sink.texts("user:1")
	if len(got) != 93 || got[0] != "308:m308" {
		t.Fatalf("within three months: %d messages from %v", len(got), got[:min(2, len(got))])
	}
	cp, _ := loadCheckpoint(state, "user:1")
	if cp.Start || cp.Oldest != 308 {
		t.Errorf("checkpoint after the limit = %+v", cp)
	}
	h.calls = nil
	backfilled(t, h, sink, state, map[string]string{"age": "Last 3 months"})
	if got := h.callsFor("user:1"); len(got) != 0 {
		t.Errorf("a chat at its limit was fetched again: %v", got)
	}
	backfilled(t, h, sink, state, map[string]string{"age": "Last year"})
	if got := h.callsFor("user:1"); len(got) == 0 || got[0] != "user:1 308 0 100" {
		t.Errorf("a longer limit did not resume from the checkpoint: %v", got)
	}
	if got := sink.texts("user:1"); len(got) != 366 {
		t.Errorf("within a year: %d messages", len(got))
	}
}

func TestBackfillObeysFloodWait(t *testing.T) {
	h := &history{counts: map[string]int{"user:1": 5}, failNext: tgerr.New(420, "FLOOD_WAIT_37")}
	sink, state := newMemSink(), signedIn(t)
	sl := backfilled(t, h, sink, state, map[string]string{"speed": "Fast"})
	if got := sl.all(); len(got) == 0 || got[0] != 37*time.Second {
		t.Errorf("waits = %v, want the flood wait first", got)
	}
	var limited bool
	for _, p := range sink.progress {
		if p.Phase == connector.PhaseRateLimited && strings.Contains(p.Detail, "until") {
			limited = true
		}
	}
	if !limited {
		t.Errorf("progress never said it was rate limited: %+v", sink.progress)
	}
	if len(sink.texts("user:1")) != 5 {
		t.Errorf("after the wait: %v", sink.texts("user:1"))
	}
}

func TestBackfillPaused(t *testing.T) {
	h := &history{counts: map[string]int{"user:1": 5}}
	sink := newMemSink()
	backfilled(t, h, sink, signedIn(t), map[string]string{"paused": "true"})
	if len(h.calls) != 0 {
		t.Errorf("a paused catch-up fetched %v", h.calls)
	}
	if p := sink.lastProgress(); !strings.Contains(p.Detail, "paused") {
		t.Errorf("progress = %+v", p)
	}
}
