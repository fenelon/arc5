package archive

import (
	"bytes"
	"errors"
	"fmt"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/fenelon/arc5/internal/model"
)

var t0 = time.Unix(1_700_000_000, 0).UTC()

// seedChat returns an account that has a private chat "c1" and a sender "u1".
func seedChat(t *testing.T, s *Store) int64 {
	t.Helper()
	acc := newAccount(t, s)
	if err := s.UpsertChat(acc, model.Chat{SourceID: "c1", Kind: model.ChatPrivate, Title: "Alice"}); err != nil {
		t.Fatal(err)
	}
	if err := s.UpsertSender(acc, model.Sender{SourceID: "u1", DisplayName: "Alice"}); err != nil {
		t.Fatal(err)
	}
	return acc
}

func textMsg(id, text string) model.Message {
	return model.Message{ChatSourceID: "c1", SourceID: id, SenderSourceID: "u1", SentAt: t0, Text: text}
}

func mustUpsert(t *testing.T, s *Store, acc int64, m model.Message) []model.AttachmentRef {
	t.Helper()
	refs, err := s.UpsertMessage(acc, m)
	if err != nil {
		t.Fatal(err)
	}
	return refs
}

func mustMessage(t *testing.T, s *Store, acc int64, id string) StoredMessage {
	t.Helper()
	m, err := s.Message(acc, "c1", id)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func TestUpsertMessageIsIdempotent(t *testing.T) {
	s := openTemp(t)
	acc := seedChat(t, s)
	m := textMsg("1", "hello")
	m.ReplyToSourceID, m.ForwardedFrom = "0", "Bob"
	mustUpsert(t, s, acc, m)
	mustUpsert(t, s, acc, m)

	got := mustMessage(t, s, acc, "1")
	if got.Text != "hello" || got.SenderSourceID != "u1" || got.Kind != model.KindMessage ||
		!got.SentAt.Equal(t0) || got.ReplyToSourceID != "0" || got.ForwardedFrom != "Bob" ||
		!got.EditedAt.IsZero() || !got.DeletedAt.IsZero() {
		t.Fatalf("message = %+v", got)
	}
	if n, _ := s.MessageCount(acc, "c1"); n != 1 {
		t.Fatalf("message count = %d, want 1", n)
	}
	if v, _ := s.Versions(got.ID); len(v) != 0 {
		t.Fatalf("versions = %v, want none", v)
	}
}

func TestEditKeepsEarlierVersion(t *testing.T) {
	s := openTemp(t)
	acc := seedChat(t, s)
	mustUpsert(t, s, acc, textMsg("1", "hello"))
	edit := textMsg("1", "hello, world")
	edit.EditedAt = t0.Add(time.Minute)
	mustUpsert(t, s, acc, edit)

	got := mustMessage(t, s, acc, "1")
	if got.Text != "hello, world" || !got.EditedAt.Equal(t0.Add(time.Minute)) {
		t.Fatalf("message = %+v", got)
	}
	versions, err := s.Versions(got.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(versions) != 1 || versions[0].Text != "hello" {
		t.Fatalf("versions = %+v", versions)
	}
}

// Review Focus 1: a connector replaying the original after a crash must not undo the edit.
func TestStaleReplayDoesNotRevertEdit(t *testing.T) {
	s := openTemp(t)
	acc := seedChat(t, s)
	mustUpsert(t, s, acc, textMsg("1", "hello"))
	edit := textMsg("1", "hello, world")
	edit.EditedAt = t0.Add(time.Minute)
	mustUpsert(t, s, acc, edit)
	mustUpsert(t, s, acc, textMsg("1", "hello")) // replayed original, no edit time

	got := mustMessage(t, s, acc, "1")
	if got.Text != "hello, world" {
		t.Fatalf("text reverted to %q", got.Text)
	}
	if v, _ := s.Versions(got.ID); len(v) != 1 {
		t.Fatalf("versions = %d, want 1", len(v))
	}
}

func TestSameTextWithNewEditTimeAddsNoVersion(t *testing.T) {
	s := openTemp(t)
	acc := seedChat(t, s)
	mustUpsert(t, s, acc, textMsg("1", "hello"))
	same := textMsg("1", "hello")
	same.EditedAt = t0.Add(time.Minute)
	mustUpsert(t, s, acc, same)

	got := mustMessage(t, s, acc, "1")
	if v, _ := s.Versions(got.ID); len(v) != 0 {
		t.Fatalf("versions = %d, want 0", len(v))
	}
	if !got.EditedAt.Equal(t0.Add(time.Minute)) {
		t.Fatalf("edited at = %v", got.EditedAt)
	}
}

func TestEditWithoutSourceTimestampUsesCaptureTime(t *testing.T) {
	s := openTemp(t)
	s.now = func() time.Time { return t0.Add(time.Hour) }
	acc := seedChat(t, s)
	mustUpsert(t, s, acc, textMsg("1", "hello"))
	mustUpsert(t, s, acc, textMsg("1", "changed"))

	got := mustMessage(t, s, acc, "1")
	if got.Text != "changed" || !got.EditedAt.Equal(t0.Add(time.Hour)) {
		t.Fatalf("message = %+v", got)
	}
}

// Review Focus 2: deletions are marks, tolerate unknown messages, and survive re-ingest.
func TestMarkDeletedIsAMarkThatSurvivesReupsert(t *testing.T) {
	s := openTemp(t)
	s.now = func() time.Time { return t0.Add(time.Hour) }
	acc := seedChat(t, s)
	mustUpsert(t, s, acc, textMsg("1", "hello"))

	if err := s.MarkDeleted(acc, "c1", []string{"1", "never-captured"}); err != nil {
		t.Fatalf("MarkDeleted with an unknown ID: %v", err)
	}
	if err := s.MarkDeleted(acc, "unknown-chat", []string{"1"}); err != nil {
		t.Fatalf("MarkDeleted in an unknown chat: %v", err)
	}
	mustUpsert(t, s, acc, textMsg("1", "hello")) // backfill sees it again

	got := mustMessage(t, s, acc, "1")
	if got.Text != "hello" || !got.DeletedAt.Equal(t0.Add(time.Hour)) {
		t.Fatalf("message = %+v", got)
	}
}

func TestSecondDeletionKeepsFirstTimestamp(t *testing.T) {
	s := openTemp(t)
	acc := seedChat(t, s)
	mustUpsert(t, s, acc, textMsg("1", "hello"))
	s.now = func() time.Time { return t0.Add(time.Hour) }
	if err := s.MarkDeleted(acc, "c1", []string{"1"}); err != nil {
		t.Fatal(err)
	}
	s.now = func() time.Time { return t0.Add(2 * time.Hour) }
	if err := s.MarkDeleted(acc, "c1", []string{"1"}); err != nil {
		t.Fatal(err)
	}
	if got := mustMessage(t, s, acc, "1"); !got.DeletedAt.Equal(t0.Add(time.Hour)) {
		t.Fatalf("deleted at = %v", got.DeletedAt)
	}
}

func TestUpsertMessageRejectsBadInput(t *testing.T) {
	s := openTemp(t)
	acc := seedChat(t, s)

	orphan := textMsg("1", "x")
	orphan.ChatSourceID = "nope"
	if _, err := s.UpsertMessage(acc, orphan); !errors.Is(err, ErrUnknownChat) {
		t.Errorf("unknown chat error = %v, want ErrUnknownChat", err)
	}
	if _, err := s.UpsertMessage(acc, textMsg("", "x")); !errors.Is(err, ErrInvalid) {
		t.Errorf("empty source ID error = %v, want ErrInvalid", err)
	}
	undated := textMsg("2", "x")
	undated.SentAt = time.Time{}
	if _, err := s.UpsertMessage(acc, undated); !errors.Is(err, ErrInvalid) {
		t.Errorf("zero sent time error = %v, want ErrInvalid", err)
	}
	badAttachment := textMsg("3", "x")
	badAttachment.Attachments = []model.Attachment{{Kind: "hologram"}}
	if _, err := s.UpsertMessage(acc, badAttachment); !errors.Is(err, ErrInvalid) {
		t.Errorf("bad attachment kind error = %v, want ErrInvalid", err)
	}
	if n, _ := s.MessageCount(acc, "c1"); n != 0 {
		t.Errorf("rejected messages left %d rows", n)
	}
}

func TestUnknownSenderIsCreatedNotRejected(t *testing.T) {
	s := openTemp(t)
	acc := seedChat(t, s)
	m := textMsg("1", "hi")
	m.SenderSourceID = "stranger"
	mustUpsert(t, s, acc, m)
	if got := mustMessage(t, s, acc, "1"); got.SenderSourceID != "stranger" {
		t.Fatalf("sender = %q", got.SenderSourceID)
	}
}

func TestServiceMessageWithNoSender(t *testing.T) {
	s := openTemp(t)
	acc := seedChat(t, s)
	m := model.Message{ChatSourceID: "c1", SourceID: "9", Kind: model.KindService, SentAt: t0, Text: "Alice joined"}
	mustUpsert(t, s, acc, m)
	got := mustMessage(t, s, acc, "9")
	if got.Kind != model.KindService || got.SenderSourceID != "" {
		t.Fatalf("message = %+v", got)
	}
}

func TestInvalidUTF8InTextIsReplaced(t *testing.T) {
	s := openTemp(t)
	acc := seedChat(t, s)
	mustUpsert(t, s, acc, textMsg("1", "bad\xfftext"))
	if got := mustMessage(t, s, acc, "1"); got.Text != "bad�text" {
		t.Fatalf("text = %q", got.Text)
	}
}

func TestRawPayloadRoundTrips(t *testing.T) {
	s := openTemp(t)
	acc := seedChat(t, s)
	payload := []byte(`{"id":1,"reactions":["👍"]}`)
	m := textMsg("1", "hello")
	m.Raw = payload
	mustUpsert(t, s, acc, m)
	got, err := s.Raw(mustMessage(t, s, acc, "1").ID)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, payload) {
		t.Fatalf("raw = %s", got)
	}

	mustUpsert(t, s, acc, textMsg("2", "no payload"))
	if got, err := s.Raw(mustMessage(t, s, acc, "2").ID); err != nil || got != nil {
		t.Fatalf("raw of message without payload = %v, %v; want nil, nil", got, err)
	}
}

func TestAttachmentStatusFollowsCapAndNoFetch(t *testing.T) {
	s := openTemp(t)
	acc := seedChat(t, s)
	if err := s.SetMediaCap(100); err != nil {
		t.Fatal(err)
	}
	m := textMsg("1", "files")
	m.Attachments = []model.Attachment{
		{Kind: model.AttachPhoto, FileName: "small.jpg", Size: 100},
		{Kind: model.AttachVideo, FileName: "big.mp4", Size: 101},
		{Kind: model.AttachPhoto, FileName: "once.jpg", Size: 10, Ephemeral: true, NoFetch: true, NoFetchReason: "self-destructing media is not archived"},
	}
	refs := mustUpsert(t, s, acc, m)
	want := []model.AttachmentRef{{ChatSourceID: "c1", MessageSourceID: "1", Position: 0}}
	if !slices.Equal(refs, want) {
		t.Fatalf("pending refs = %+v, want %+v", refs, want)
	}

	atts, err := s.Attachments(mustMessage(t, s, acc, "1").ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(atts) != 3 {
		t.Fatalf("attachments = %d, want 3", len(atts))
	}
	if atts[0].Status != model.StatusPending {
		t.Errorf("at the cap: status = %s, want pending", atts[0].Status)
	}
	if atts[1].Status != model.StatusSkipped || atts[1].Reason != "over size cap" {
		t.Errorf("over the cap: %+v", atts[1])
	}
	if atts[2].Status != model.StatusSkipped || !atts[2].Ephemeral || atts[2].Reason != "self-destructing media is not archived" {
		t.Errorf("no-fetch: %+v", atts[2])
	}

	// Replaying the message returns the still-pending attachment again and adds no rows.
	if again := mustUpsert(t, s, acc, m); !slices.Equal(again, want) {
		t.Fatalf("pending refs on replay = %+v", again)
	}
	if atts, _ := s.Attachments(mustMessage(t, s, acc, "1").ID); len(atts) != 3 {
		t.Fatalf("attachments after replay = %d, want 3", len(atts))
	}
}

func TestLookupChatsFiltersByKindAndAccount(t *testing.T) {
	s := openTemp(t)
	acc := seedChat(t, s)
	other := newAccount(t, s)
	for _, c := range []struct {
		acc  int64
		chat model.Chat
	}{
		{acc, model.Chat{SourceID: "g1", Kind: model.ChatGroup, Title: "Group"}},
		{acc, model.Chat{SourceID: "ch1", Kind: model.ChatChannel, Title: "Channel"}},
		{other, model.Chat{SourceID: "c1", Kind: model.ChatPrivate, Title: "Other account"}},
	} {
		if err := s.UpsertChat(c.acc, c.chat); err != nil {
			t.Fatal(err)
		}
	}
	for _, chat := range []string{"c1", "g1", "ch1"} {
		m := textMsg("42", "x")
		m.ChatSourceID = chat
		mustUpsert(t, s, acc, m)
	}
	mustUpsert(t, s, other, textMsg("42", "x"))

	got, err := s.LookupChats(acc, "42", []model.ChatKind{model.ChatPrivate, model.ChatGroup})
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"c1", "g1"}; !slices.Equal(got, want) {
		t.Fatalf("chats = %v, want %v", got, want)
	}
	if got, err := s.LookupChats(acc, "42", nil); err != nil || len(got) != 0 {
		t.Fatalf("no kinds: %v, %v", got, err)
	}
	if got, err := s.LookupChats(acc, "missing", []model.ChatKind{model.ChatPrivate}); err != nil || len(got) != 0 {
		t.Fatalf("missing message: %v, %v", got, err)
	}
}

// Review Focus 3: backfill and live capture write at the same time.
func TestConcurrentWritersDoNotFail(t *testing.T) {
	s := openTemp(t)
	acc := seedChat(t, s)
	const writers, each = 8, 50
	var wg sync.WaitGroup
	errs := make(chan error, writers*each)
	for w := range writers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range each {
				if _, err := s.UpsertMessage(acc, textMsg(fmt.Sprintf("%d-%d", w, i), "x")); err != nil {
					errs <- err
				}
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("concurrent upsert failed: %v", err)
	}
	if n, _ := s.MessageCount(acc, "c1"); n != writers*each {
		t.Fatalf("message count = %d, want %d", n, writers*each)
	}
}

func TestMessageNotFound(t *testing.T) {
	s := openTemp(t)
	acc := seedChat(t, s)
	if _, err := s.Message(acc, "c1", "nope"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("error = %v, want ErrNotFound", err)
	}
}

// A source that does not date its edits can still edit a message more than once.
func TestUndatedEditsKeepApplying(t *testing.T) {
	s := openTemp(t)
	acc := seedChat(t, s)
	for _, text := range []string{"a", "b", "c"} {
		mustUpsert(t, s, acc, textMsg("1", text))
	}
	got := mustMessage(t, s, acc, "1")
	if got.Text != "c" {
		t.Fatalf("text = %q, want c", got.Text)
	}
	versions, err := s.Versions(got.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(versions) != 2 || versions[0].Text != "a" || versions[1].Text != "b" {
		t.Fatalf("versions = %+v, want a then b", versions)
	}
}

func TestUndatedReplayOfReplacedTextIsIgnored(t *testing.T) {
	s := openTemp(t)
	acc := seedChat(t, s)
	for _, text := range []string{"a", "b", "a"} {
		mustUpsert(t, s, acc, textMsg("1", text))
	}
	got := mustMessage(t, s, acc, "1")
	if got.Text != "b" {
		t.Fatalf("text = %q, want b", got.Text)
	}
	if v, _ := s.Versions(got.ID); len(v) != 1 {
		t.Fatalf("versions = %d, want 1", len(v))
	}
}

// The capture clock stamps an undated edit; it must not outrank the source's own clock.
func TestDatedEditAppliesAfterUndatedEdit(t *testing.T) {
	s := openTemp(t)
	s.now = func() time.Time { return t0.Add(time.Hour) }
	acc := seedChat(t, s)
	mustUpsert(t, s, acc, textMsg("1", "a"))
	mustUpsert(t, s, acc, textMsg("1", "b"))
	dated := textMsg("1", "c")
	dated.EditedAt = t0.Add(time.Minute) // earlier than the capture clock
	mustUpsert(t, s, acc, dated)

	got := mustMessage(t, s, acc, "1")
	if got.Text != "c" || !got.EditedAt.Equal(t0.Add(time.Minute)) {
		t.Fatalf("message = %+v", got)
	}
}

// Review Focus 2, second half: an edit that arrives after the deletion keeps the mark.
func TestEditAfterDeletionKeepsTheMark(t *testing.T) {
	s := openTemp(t)
	acc := seedChat(t, s)
	mustUpsert(t, s, acc, textMsg("1", "hello"))
	if err := s.MarkDeleted(acc, "c1", []string{"1", "never-captured"}); err != nil {
		t.Fatal(err)
	}
	if n, _ := s.MessageCount(acc, "c1"); n != 1 {
		t.Fatalf("deleting an unknown message changed the count to %d", n)
	}
	edit := textMsg("1", "hello, world")
	edit.EditedAt = t0.Add(time.Minute)
	mustUpsert(t, s, acc, edit)

	got := mustMessage(t, s, acc, "1")
	if got.Text != "hello, world" || got.DeletedAt.IsZero() {
		t.Fatalf("message = %+v; want the new text and the deletion mark", got)
	}
}
