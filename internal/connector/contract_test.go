package connector_test

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/fenelon/arc5/internal/archive"
	"github.com/fenelon/arc5/internal/connector"
	"github.com/fenelon/arc5/internal/connector/connectortest"
	"github.com/fenelon/arc5/internal/model"
)

var t0 = time.Unix(1_700_000_000, 0).UTC()

func msg(id, text string, at time.Time) model.Message {
	return model.Message{ChatSourceID: "c1", SourceID: id, SenderSourceID: "u1", SentAt: at, Text: text}
}

// script is what a real connector does on a bad day: duplicates, out-of-order
// delivery, an edit, a stale replay, deletions with and without a chat, a
// deletion of something never captured, and a media download.
func script() []connectortest.Step {
	edited := msg("1", "hello, world", t0)
	edited.EditedAt = t0.Add(time.Minute)
	withFile := msg("4", "see attached", t0.Add(4*time.Second))
	withFile.Attachments = []model.Attachment{{Kind: model.AttachDocument, FileName: "a.txt", Size: 5}}

	return []connectortest.Step{
		connectortest.Chat(model.Chat{SourceID: "c1", Kind: model.ChatPrivate, Title: "Alice"}),
		connectortest.Sender(model.Sender{SourceID: "u1", DisplayName: "Alice"}),
		connectortest.Message(msg("1", "hello", t0)),
		connectortest.Message(msg("1", "hello", t0)),                    // duplicate
		connectortest.Message(msg("3", "third", t0.Add(3*time.Second))), // arrives before 2
		connectortest.Message(msg("2", "second", t0.Add(2*time.Second))),
		connectortest.Message(edited),
		connectortest.Message(msg("1", "hello", t0)), // stale replay of the original
		connectortest.Delete("c1", "2"),
		connectortest.DeleteWithoutChat([]model.ChatKind{model.ChatPrivate, model.ChatGroup}, "3"),
		connectortest.Delete("c1", "999"), // never captured
		connectortest.Message(withFile),
		connectortest.Media(model.AttachmentRef{ChatSourceID: "c1", MessageSourceID: "4", Position: 0}, []byte("hello")),
	}
}

// runScript drives the steps through a real runner, sink and archive.
func runScript(t *testing.T, store *archive.Store, steps []connectortest.Step) {
	t.Helper()
	fake := connectortest.New(steps...)
	reg := connector.NewRegistry()
	reg.Register(connector.Info{Name: "fake", Title: "Fake", Display: connector.DisplayChats}, func() connector.Connector { return fake })
	runner := connector.NewRunner(store, reg)
	ctx, cancel := context.WithCancel(context.Background())
	defer func() { cancel(); runner.Wait() }()
	if err := runner.Start(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case <-fake.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("fake connector did not finish its script")
	}
	if err := fake.Err(); err != nil {
		t.Fatalf("script step failed: %v", err)
	}
}

func assertArchive(t *testing.T, store *archive.Store, acc int64) {
	t.Helper()
	if n, err := store.MessageCount(acc, "c1"); err != nil || n != 4 {
		t.Fatalf("message count = %d, %v; want 4", n, err)
	}

	first, err := store.Message(acc, "c1", "1")
	if err != nil {
		t.Fatal(err)
	}
	if first.Text != "hello, world" || !first.EditedAt.Equal(t0.Add(time.Minute)) || !first.DeletedAt.IsZero() {
		t.Errorf("message 1 = %+v", first)
	}
	versions, err := store.Versions(first.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(versions) != 1 || versions[0].Text != "hello" {
		t.Errorf("message 1 versions = %+v, want one holding the original", versions)
	}

	for _, id := range []string{"2", "3"} {
		m, err := store.Message(acc, "c1", id)
		if err != nil {
			t.Fatal(err)
		}
		if m.DeletedAt.IsZero() {
			t.Errorf("message %s is not marked deleted", id)
		}
		if m.Text == "" {
			t.Errorf("message %s lost its text when deleted at the source", id)
		}
	}

	fourth, err := store.Message(acc, "c1", "4")
	if err != nil {
		t.Fatal(err)
	}
	atts, err := store.Attachments(fourth.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(atts) != 1 || atts[0].Status != model.StatusStored {
		t.Fatalf("message 4 attachments = %+v", atts)
	}
	content, err := os.ReadFile(store.MediaPath(atts[0].SHA256))
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != "hello" {
		t.Errorf("stored media = %q", content)
	}
}

func newStore(t *testing.T) (*archive.Store, int64) {
	t.Helper()
	store, err := archive.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	acc, err := store.CreateAccount("fake", "Fake", nil)
	if err != nil {
		t.Fatal(err)
	}
	return store, acc
}

func TestIngestContract(t *testing.T) {
	store, acc := newStore(t)
	runScript(t, store, script())
	assertArchive(t, store, acc)
}

// A connector that crashes and replays everything must leave the archive unchanged.
func TestReplayIsLossless(t *testing.T) {
	store, acc := newStore(t)
	runScript(t, store, script())
	runScript(t, store, script())
	assertArchive(t, store, acc)
}
