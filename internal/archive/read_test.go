package archive

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/fenelon/arc5/internal/model"
)

// conversation stores n messages in chat c1, one minute apart starting at t0,
// with source IDs "1".."n", and returns their database IDs in that order.
func conversation(t *testing.T, s *Store, acc int64, n int) []int64 {
	t.Helper()
	ids := make([]int64, n)
	for i := range n {
		m := textMsg(fmt.Sprint(i+1), fmt.Sprintf("message %d", i+1))
		m.SentAt = t0.Add(time.Duration(i) * time.Minute)
		mustUpsert(t, s, acc, m)
		ids[i] = mustMessage(t, s, acc, m.SourceID).ID
	}
	return ids
}

func chatDBID(t *testing.T, s *Store, acc int64, sourceID string) int64 {
	t.Helper()
	var id int64
	if err := s.r.QueryRow(`SELECT id FROM chats WHERE account_id = ? AND source_id = ?`, acc, sourceID).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

func texts(w Window) string {
	parts := make([]string, len(w.Messages))
	for i, m := range w.Messages {
		parts[i] = strings.TrimPrefix(m.Text, "message ")
	}
	return strings.Join(parts, " ")
}

func TestChatListIsNewestFirstWithLastMessage(t *testing.T) {
	s := openTemp(t)
	acc := seedChat(t, s)
	other := newAccount(t, s)
	for _, c := range []model.Chat{
		{SourceID: "g1", Kind: model.ChatGroup, Title: "Group"},
		{SourceID: "empty", Kind: model.ChatPrivate, Title: "Nobody wrote"},
	} {
		if err := s.UpsertChat(acc, c); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.UpsertChat(other, model.Chat{SourceID: "x", Kind: model.ChatPrivate, Title: "Elsewhere"}); err != nil {
		t.Fatal(err)
	}

	old := textMsg("1", "an old one")
	mustUpsert(t, s, acc, old)
	last := textMsg("2", "the latest in c1")
	last.SentAt, last.Outgoing = t0.Add(time.Hour), true
	last.Attachments = []model.Attachment{{Kind: model.AttachPhoto, FileName: "a.jpg", Size: 1}, {Kind: model.AttachPhoto, FileName: "b.jpg", Size: 1}}
	mustUpsert(t, s, acc, last)
	inGroup := textMsg("3", "newest overall")
	inGroup.ChatSourceID, inGroup.SentAt = "g1", t0.Add(2*time.Hour)
	mustUpsert(t, s, acc, inGroup)
	elsewhere := model.Message{ChatSourceID: "x", SourceID: "9", SentAt: t0.Add(3 * time.Hour), Text: "another account"}
	mustUpsert(t, s, other, elsewhere)

	got, err := s.ChatList([]int64{acc}, 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Title != "Group" || got[1].Title != "Alice" {
		t.Fatalf("chat list = %+v; want Group then Alice, without the empty chat or the other account", got)
	}
	c1 := got[1]
	if c1.LastText != "the latest in c1" || !c1.LastOutgoing || c1.LastFiles != 2 || c1.LastSender != "Alice" ||
		!c1.LastAt.Equal(t0.Add(time.Hour)) || c1.Kind != model.ChatPrivate || c1.AccountID != acc {
		t.Errorf("summary = %+v", c1)
	}

	both, err := s.ChatList([]int64{acc, other}, 10, 0)
	if err != nil || len(both) != 3 || both[0].Title != "Elsewhere" {
		t.Errorf("two accounts = %+v, %v", both, err)
	}
	if page, _ := s.ChatList([]int64{acc, other}, 1, 1); len(page) != 1 || page[0].Title != "Group" {
		t.Errorf("limit 1 offset 1 = %+v", page)
	}
	if none, err := s.ChatList(nil, 10, 0); err != nil || len(none) != 0 {
		t.Errorf("no accounts = %+v, %v", none, err)
	}
}

func TestChatInfo(t *testing.T) {
	s := openTemp(t)
	acc := seedChat(t, s)
	conversation(t, s, acc, 3)
	got, err := s.Chat(chatDBID(t, s, acc, "c1"))
	if err != nil {
		t.Fatal(err)
	}
	if got.Title != "Alice" || got.SourceID != "c1" || got.AccountID != acc || got.Kind != model.ChatPrivate ||
		!got.Archived || got.Messages != 3 || !got.FirstAt.Equal(t0) {
		t.Fatalf("chat = %+v", got)
	}
	if _, err := s.Chat(9999); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown chat error = %v, want ErrNotFound", err)
	}
}

func TestChatMessagesWindows(t *testing.T) {
	s := openTemp(t)
	acc := seedChat(t, s)
	ids := conversation(t, s, acc, 10)
	chat := chatDBID(t, s, acc, "c1")

	for _, c := range []struct {
		name             string
		anchor           Anchor
		limit            int
		want             string
		older, newerSide bool
	}{
		{"newest", Anchor{}, 4, "7 8 9 10", true, false},
		{"everything", Anchor{}, 50, "1 2 3 4 5 6 7 8 9 10", false, false},
		{"before 7", Anchor{Before: ids[6]}, 4, "3 4 5 6", true, true},
		{"before 3", Anchor{Before: ids[2]}, 4, "1 2", false, true},
		{"after 7", Anchor{After: ids[6]}, 2, "8 9", true, true},
		{"after 9", Anchor{After: ids[8]}, 4, "10", true, false},
		{"around 5", Anchor{Around: ids[4]}, 4, "3 4 5 6", true, true},
		{"around 1", Anchor{Around: ids[0]}, 4, "1 2", false, true},
		{"around 10", Anchor{Around: ids[9]}, 4, "8 9 10", true, false},
		{"from the fourth minute", Anchor{From: t0.Add(3 * time.Minute)}, 3, "4 5 6", true, true},
		{"from before the start", Anchor{From: t0.Add(-time.Hour)}, 3, "1 2 3", false, true},
		{"from after the end falls back to the newest", Anchor{From: t0.Add(24 * time.Hour)}, 3, "8 9 10", true, false},
	} {
		w, err := s.ChatMessages(chat, c.anchor, c.limit)
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if got := texts(w); got != c.want || w.HasOlder != c.older || w.HasNewer != c.newerSide {
			t.Errorf("%s: got %q older=%v newer=%v; want %q older=%v newer=%v",
				c.name, got, w.HasOlder, w.HasNewer, c.want, c.older, c.newerSide)
		}
	}
}

// Review Focus 4: paging edges.
func TestChatMessagesEdges(t *testing.T) {
	s := openTemp(t)
	acc := seedChat(t, s)
	if err := s.UpsertChat(acc, model.Chat{SourceID: "g1", Kind: model.ChatGroup, Title: "Group"}); err != nil {
		t.Fatal(err)
	}
	chat, group := chatDBID(t, s, acc, "c1"), chatDBID(t, s, acc, "g1")

	if w, err := s.ChatMessages(chat, Anchor{}, 10); err != nil || len(w.Messages) != 0 || w.HasOlder || w.HasNewer {
		t.Errorf("empty chat = %+v, %v", w, err)
	}
	ids := conversation(t, s, acc, 1)
	if w, err := s.ChatMessages(chat, Anchor{Around: ids[0]}, 10); err != nil || texts(w) != "1" || w.HasOlder || w.HasNewer {
		t.Errorf("one message = %+v, %v", w, err)
	}
	// A message of one chat is no anchor in another, and neither is a missing one.
	for name, a := range map[string]Anchor{
		"another chat's message": {Around: ids[0]},
		"a missing message":      {Before: 987654},
		"a missing later one":    {After: 987654},
	} {
		w, err := s.ChatMessages(group, a, 10)
		if err != nil || len(w.Messages) != 0 {
			t.Errorf("%s: %+v, %v; want an empty window", name, w, err)
		}
	}
	if w, err := s.ChatMessages(chat, Anchor{}, 0); err != nil || len(w.Messages) != 1 {
		t.Errorf("limit 0 = %+v, %v; want it treated as 1", w, err)
	}
}

func TestChatMessagesCarryWhatAPageShows(t *testing.T) {
	s := openTemp(t)
	acc := seedChat(t, s)
	m := textMsg("1", "first wording")
	m.ForwardedFrom, m.ReplyToSourceID, m.Outgoing = "Bob", "0", true
	m.Attachments = []model.Attachment{{Kind: model.AttachDocument, FileName: "a.pdf", MimeType: "application/pdf", Size: 5}}
	mustUpsert(t, s, acc, m)
	edit := textMsg("1", "second wording")
	edit.EditedAt = t0.Add(time.Minute)
	mustUpsert(t, s, acc, edit)
	if err := s.MarkDeleted(acc, "c1", []string{"1"}); err != nil {
		t.Fatal(err)
	}
	service := model.Message{ChatSourceID: "c1", SourceID: "2", Kind: model.KindService, SentAt: t0.Add(time.Hour), Text: "Alice pinned a message"}
	mustUpsert(t, s, acc, service)

	w, err := s.ChatMessages(chatDBID(t, s, acc, "c1"), Anchor{}, 10)
	if err != nil || len(w.Messages) != 2 {
		t.Fatalf("window = %+v, %v", w, err)
	}
	got := w.Messages[0]
	if got.Text != "second wording" || got.SenderName != "Alice" || !got.Outgoing || got.Versions != 1 ||
		got.ForwardedFrom != "Bob" || got.ReplyToSourceID != "0" || got.SourceID != "1" ||
		!got.EditedAt.Equal(t0.Add(time.Minute)) || got.DeletedAt.IsZero() || !got.SentAt.Equal(t0) {
		t.Errorf("message = %+v", got)
	}
	if len(got.Attachments) != 1 || got.Attachments[0].FileName != "a.pdf" || got.Attachments[0].Status != model.StatusPending {
		t.Errorf("attachments = %+v", got.Attachments)
	}
	if svc := w.Messages[1]; svc.Kind != model.KindService || svc.SenderName != "" || len(svc.Attachments) != 0 {
		t.Errorf("service message = %+v", svc)
	}
}

func TestChatMonths(t *testing.T) {
	s := openTemp(t)
	acc := seedChat(t, s)
	at := func(id string, year int, month time.Month, day int) {
		m := textMsg(id, "x")
		m.SentAt = time.Date(year, month, day, 12, 0, 0, 0, time.UTC)
		mustUpsert(t, s, acc, m)
	}
	at("1", 2024, time.January, 15)
	at("2", 2024, time.January, 20)
	at("3", 2024, time.April, 2)
	at("4", 2025, time.December, 1)
	at("5", 2025, time.December, 9)
	at("6", 2025, time.December, 30)
	chat := chatDBID(t, s, acc, "c1")

	got, err := s.ChatMonths(chat, time.UTC)
	if err != nil {
		t.Fatal(err)
	}
	want := []Month{{2025, time.December, 3}, {2024, time.April, 1}, {2024, time.January, 2}}
	if !slices.Equal(got, want) {
		t.Fatalf("months = %v, want %v", got, want)
	}
	if none, err := s.ChatMonths(9999, time.UTC); err != nil || len(none) != 0 {
		t.Errorf("an empty chat = %v, %v", none, err)
	}

	// Counts between moments, such as the weeks of a month.
	weeks := []time.Time{
		time.Date(2025, time.December, 1, 0, 0, 0, 0, time.UTC), time.Date(2025, time.December, 8, 0, 0, 0, 0, time.UTC),
		time.Date(2025, time.December, 15, 0, 0, 0, 0, time.UTC), time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC),
	}
	if got, err := s.ChatCounts(chat, weeks); err != nil || !slices.Equal(got, []int{1, 1, 1}) {
		t.Errorf("counts = %v, %v; want 1 1 1", got, err)
	}
}

// Months are counted in the time zone the reader sees dates in.
func TestChatMonthsFollowTheTimeZone(t *testing.T) {
	s := openTemp(t)
	acc := seedChat(t, s)
	la, err := time.LoadLocation("America/Los_Angeles")
	if err != nil {
		t.Fatal(err)
	}
	newYearsEve := textMsg("1", "x")
	newYearsEve.SentAt = time.Date(2025, time.December, 31, 20, 0, 0, 0, la) // already 2026 in UTC
	mustUpsert(t, s, acc, newYearsEve)
	chat := chatDBID(t, s, acc, "c1")

	if got, _ := s.ChatMonths(chat, la); !slices.Equal(got, []Month{{2025, time.December, 1}}) {
		t.Errorf("in Los Angeles: %v, want December 2025", got)
	}
	if got, _ := s.ChatMonths(chat, time.UTC); !slices.Equal(got, []Month{{2026, time.January, 1}}) {
		t.Errorf("in UTC: %v, want January 2026", got)
	}
}

func TestAttachmentFile(t *testing.T) {
	s := openTemp(t)
	acc := seedChat(t, s)
	ref := withAttachment(t, s, acc, "1", 5)
	att := attachment(t, s, acc, "1")

	pending, err := s.AttachmentFile(att.ID)
	if err != nil {
		t.Fatal(err)
	}
	if pending.Path != "" || pending.Status != model.StatusPending || pending.MessageID == 0 {
		t.Errorf("pending file = %+v", pending)
	}
	if err := s.PutMedia(acc, ref, strings.NewReader("hello")); err != nil {
		t.Fatal(err)
	}
	stored, err := s.AttachmentFile(att.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Path != s.MediaPath(stored.SHA256) || stored.Status != model.StatusStored || stored.FileName != "a.bin" {
		t.Errorf("stored file = %+v", stored)
	}
	if _, err := s.AttachmentFile(9999); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown attachment error = %v, want ErrNotFound", err)
	}
}

// Messages sent in the same second, such as a batch of forwards, read in the
// source's order however they were stored: the catch-up stores newest first.
// Numeric source IDs compare as numbers.
func TestChatMessagesInTheSameSecondKeepTheSourcesOrder(t *testing.T) {
	s := openTemp(t)
	acc := seedChat(t, s)
	ids := map[string]int64{}
	for _, src := range []string{"12", "11", "10", "9"} {
		m := textMsg(src, "message "+src)
		m.SentAt = t0
		mustUpsert(t, s, acc, m)
		ids[src] = mustMessage(t, s, acc, src).ID
	}
	later := textMsg("13", "message 13")
	later.SentAt = t0.Add(time.Minute)
	mustUpsert(t, s, acc, later)
	ids["13"] = mustMessage(t, s, acc, "13").ID
	chat := chatDBID(t, s, acc, "c1")

	for _, c := range []struct {
		name   string
		anchor Anchor
		limit  int
		want   string
	}{
		{"newest", Anchor{}, 50, "9 10 11 12 13"},
		{"newest two", Anchor{}, 2, "12 13"},
		{"before 11", Anchor{Before: ids["11"]}, 50, "9 10"},
		{"after 10", Anchor{After: ids["10"]}, 2, "11 12"},
		{"around 11", Anchor{Around: ids["11"]}, 3, "10 11 12"},
		{"from the second", Anchor{From: t0}, 3, "9 10 11"},
	} {
		w, err := s.ChatMessages(chat, c.anchor, c.limit)
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if got := texts(w); got != c.want {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
	}
}

// A reply carries what it answers, when that is archived in the same chat.
func TestChatMessagesQuoteWhatTheyReplyTo(t *testing.T) {
	s := openTemp(t)
	acc := seedChat(t, s)
	first := textMsg("1", "where are the keys?")
	mustUpsert(t, s, acc, first)
	photo := textMsg("2", "")
	photo.SentAt = t0.Add(time.Minute)
	photo.Attachments = []model.Attachment{{Kind: model.AttachPhoto, FileName: "box.jpg", Size: 1}}
	mustUpsert(t, s, acc, photo)
	reply := textMsg("3", "in the box")
	reply.SentAt, reply.Outgoing, reply.ReplyToSourceID = t0.Add(2*time.Minute), true, "1"
	mustUpsert(t, s, acc, reply)
	toPhoto := textMsg("4", "that one")
	toPhoto.SentAt, toPhoto.ReplyToSourceID = t0.Add(3*time.Minute), "2"
	mustUpsert(t, s, acc, toPhoto)
	lost := textMsg("5", "about something older")
	lost.SentAt, lost.ReplyToSourceID = t0.Add(4*time.Minute), "999"
	mustUpsert(t, s, acc, lost)

	w, err := s.ChatMessages(chatDBID(t, s, acc, "c1"), Anchor{After: mustMessage(t, s, acc, "2").ID}, 10)
	if err != nil {
		t.Fatal(err)
	}
	q := w.Messages[0].Reply
	if q == nil || q.ID != mustMessage(t, s, acc, "1").ID || q.SenderName != "Alice" || q.Text != "where are the keys?" || q.Outgoing {
		t.Errorf("reply to text: %+v", q)
	}
	if q := w.Messages[1].Reply; q == nil || q.Text != "" || q.File != "box.jpg" || q.FileKind != model.AttachPhoto {
		t.Errorf("reply to a photo: %+v", q)
	}
	if q := w.Messages[2].Reply; q != nil || w.Messages[2].ReplyToSourceID != "999" {
		t.Errorf("reply to a message not archived: %+v", q)
	}
}
