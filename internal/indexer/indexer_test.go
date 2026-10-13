package indexer

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/fenelon/arc5/internal/archive"
	"github.com/fenelon/arc5/internal/model"
	"github.com/fenelon/arc5/internal/search"
)

func openStore(t *testing.T) *archive.Store {
	t.Helper()
	store, err := archive.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	return store
}

// seed stores n messages, each saying "running".
func seed(t *testing.T, store *archive.Store, n int) int64 {
	t.Helper()
	acc, err := store.CreateAccount("test", "Test", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertChat(acc, model.Chat{SourceID: "c", Kind: model.ChatPrivate, Title: "Chat"}); err != nil {
		t.Fatal(err)
	}
	for i := range n {
		m := model.Message{ChatSourceID: "c", SourceID: fmt.Sprint(i), SentAt: time.Unix(1_700_000_000+int64(i), 0), Text: "running"}
		if _, err := store.UpsertMessage(acc, m); err != nil {
			t.Fatal(err)
		}
	}
	return acc
}

// hits counts the messages a search for text finds.
func hits(t *testing.T, x *Indexer, text string) int {
	t.Helper()
	page, err := x.Searcher().Search(context.Background(), search.Query{Text: text + " ", Limit: search.MaxLimit})
	if err != nil {
		t.Fatal(err)
	}
	return len(page.Hits)
}

// settle waits for any rebuild to finish.
func settle(t *testing.T, x *Indexer) Status {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for x.Status().Rebuilding {
		if time.Now().After(deadline) {
			t.Fatal("the rebuild never finished")
		}
		time.Sleep(5 * time.Millisecond)
	}
	return x.Status()
}

func TestLoadUsesTheSavedLanguages(t *testing.T) {
	store := openStore(t)
	x, err := Load(store)
	if err != nil {
		t.Fatal(err)
	}
	if got := x.Status().Languages; !slices.Equal(got, []string{"en"}) {
		t.Fatalf("languages on a new archive = %v, want [en]", got)
	}
	if err := store.SetSetting(LanguagesKey, "ru,es"); err != nil {
		t.Fatal(err)
	}
	x, err = Load(store)
	if err != nil {
		t.Fatal(err)
	}
	if got := x.Status().Languages; !slices.Equal(got, []string{"es", "ru"}) && !slices.Equal(got, []string{"ru", "es"}) {
		t.Fatalf("languages = %v, want ru and es", got)
	}
	if err := store.SetSetting(LanguagesKey, "en,xx"); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(store); err == nil {
		t.Fatal("an unknown language in the setting was accepted")
	}
}

func TestStartRebuildsAStaleIndex(t *testing.T) {
	store := openStore(t)
	seed(t, store, 3) // stored with no analyzer, so not indexed
	x, err := Load(store)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer func() { cancel(); x.Wait() }()
	if err := x.Start(ctx); err != nil {
		t.Fatal(err)
	}
	if st := settle(t, x); st.Err != "" || st.Done != 3 || st.Total != 3 {
		t.Fatalf("status after the rebuild = %+v", st)
	}
	if n := hits(t, x, "run"); n != 3 {
		t.Fatalf("a stem search found %d messages, want 3", n)
	}
	if current, _ := store.IndexCurrent(); !current {
		t.Fatal("the index is not current after the rebuild")
	}

	// A current index is left alone.
	again, err := Load(store)
	if err != nil {
		t.Fatal(err)
	}
	if err := again.Start(ctx); err != nil {
		t.Fatal(err)
	}
	if st := again.Status(); st.Rebuilding || st.Total != 0 {
		t.Fatalf("a current index was rebuilt: %+v", st)
	}
	again.Wait()
}

func TestSetLanguagesSwitchesAndRebuilds(t *testing.T) {
	store := openStore(t)
	x, err := Load(store)
	if err != nil {
		t.Fatal(err)
	}
	acc := seed(t, store, 0)
	ctx, cancel := context.WithCancel(context.Background())
	defer func() { cancel(); x.Wait() }()
	if err := x.Start(ctx); err != nil {
		t.Fatal(err)
	}
	settle(t, x)
	m := model.Message{ChatSourceID: "c", SourceID: "ru", SentAt: time.Unix(1_700_000_000, 0), Text: "кошками"}
	if _, err := store.UpsertMessage(acc, m); err != nil {
		t.Fatal(err)
	}
	if n := hits(t, x, "кошка"); n != 0 {
		t.Fatalf("Russian word forms are found before Russian is chosen: %d", n)
	}

	if err := x.SetLanguages([]string{"ru", "en"}); err != nil {
		t.Fatal(err)
	}
	st := settle(t, x)
	if st.Err != "" || !slices.Contains(st.Languages, "ru") {
		t.Fatalf("status = %+v", st)
	}
	if saved, _, _ := store.Setting(LanguagesKey); saved != "en,ru" {
		t.Errorf("saved languages = %q, want en,ru", saved)
	}
	if n := hits(t, x, "кошка"); n != 1 {
		t.Fatalf("after choosing Russian, a word form found %d messages, want 1", n)
	}
	// New messages are indexed with the new languages too.
	m.SourceID, m.Text = "ru2", "кошкой"
	if _, err := store.UpsertMessage(acc, m); err != nil {
		t.Fatal(err)
	}
	if n := hits(t, x, "кошка"); n != 2 {
		t.Fatalf("a message stored after the switch is not found by word form: %d", n)
	}

	if err := x.SetLanguages([]string{"en", "zz"}); !errors.Is(err, ErrUnknownLanguage) {
		t.Fatalf("an unknown language: %v, want ErrUnknownLanguage", err)
	}
	if err := x.SetLanguages(nil); err != nil {
		t.Fatalf("no languages at all: %v", err)
	}
	settle(t, x)
	if saved, _, _ := store.Setting(LanguagesKey); saved != "" {
		t.Errorf("saved languages = %q, want none", saved)
	}
}

func TestSetLanguagesReplacesARebuildInFlight(t *testing.T) {
	store := openStore(t)
	seed(t, store, 50)
	x, err := Load(store)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer func() { cancel(); x.Wait() }()
	if err := x.Start(ctx); err != nil {
		t.Fatal(err)
	}
	for _, langs := range [][]string{{"es"}, {"ru"}, {"en"}} {
		if err := x.SetLanguages(langs); err != nil {
			t.Fatal(err)
		}
	}
	st := settle(t, x)
	if st.Err != "" || !slices.Equal(st.Languages, []string{"en"}) || st.Done != 50 {
		t.Fatalf("status = %+v", st)
	}
	if current, _ := store.IndexCurrent(); !current {
		t.Fatal("the index is not current after the last rebuild")
	}
}
