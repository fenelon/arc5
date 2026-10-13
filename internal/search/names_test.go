package search

import (
	"context"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/fenelon/arc5/internal/model"
)

// namesFixture has chats and people in three languages. Every chat listed
// here holds at least one message; "Alice Empty" holds none.
func namesFixture(t *testing.T) *fixture {
	t.Helper()
	f := newFixture(t, "en", "ru", "es")
	f.chat("c1", model.ChatPrivate, "Alice Smith")
	f.chat("fam", model.ChatGroup, "Семья")
	f.chat("work", model.ChatGroup, "Trabajo España")
	f.chat("empty", model.ChatPrivate, "Alice Empty")
	for _, s := range []model.Sender{
		{SourceID: "u1", DisplayName: "Alice Smith", Username: "alice_w"},
		{SourceID: "u2", DisplayName: "Борис Ёлкин"},
		{SourceID: "u3", DisplayName: "José Núñez", Username: "pepe"},
	} {
		if err := f.store.UpsertSender(f.acc, s); err != nil {
			t.Fatal(err)
		}
	}
	f.say("hello")
	f.say("привет", func(m *model.Message) { m.ChatSourceID, m.SenderSourceID = "fam", "u2" })
	f.say("hola", func(m *model.Message) { m.ChatSourceID, m.SenderSourceID = "work", "u3" })
	f.say("who is this", func(m *model.Message) { m.ChatSourceID, m.SenderSourceID = "work", "stranger" })
	return f
}

func (f *fixture) names(query string) Names {
	f.t.Helper()
	names, err := f.search.Names(context.Background(), query, nil, 0)
	if err != nil {
		f.t.Fatalf("Names(%q): %v", query, err)
	}
	return names
}

func chatTitles(n Names) []string {
	var out []string
	for _, c := range n.Chats {
		out = append(out, render(c.Title))
	}
	return out
}

func peopleNames(n Names) []string {
	var out []string
	for _, p := range n.People {
		out = append(out, render(p.Name))
	}
	return out
}

func TestNamesFindsChatsAndPeople(t *testing.T) {
	f := namesFixture(t)
	for _, c := range []struct {
		query  string
		chats  []string
		people []string
	}{
		{"alice", []string{"[Alice] Smith"}, []string{"[Alice] Smith"}},
		{"ALI", []string{"[Alice] Smith"}, []string{"[Alice] Smith"}},        // a prefix, any case
		{"smi al", []string{"[Alice] [Smith]"}, []string{"[Alice] [Smith]"}}, // every word, any order
		{"alice bob", nil, nil}, // every word must match
		{"сем", []string{"[Семья]"}, nil},
		{"espana", []string{"Trabajo [España]"}, nil}, // accents folded
		{"елкин", nil, []string{"Борис [Ёлкин]"}},     // ё and е are one letter
		{"nunez", nil, []string{"José [Núñez]"}},
		{"@alice_w", nil, []string{"[Alice] Smith"}}, // by username; "alice" is also in the name
		{"pepe", nil, []string{"José Núñez"}},        // by username only: nothing in the name to highlight
		{"", nil, nil},
		{"   ", nil, nil},
		{"!!!", nil, nil},
		{"zzz", nil, nil},
	} {
		got := f.names(c.query)
		if !slices.Equal(chatTitles(got), c.chats) {
			t.Errorf("%q chats = %q, want %q", c.query, chatTitles(got), c.chats)
		}
		if !slices.Equal(peopleNames(got), c.people) {
			t.Errorf("%q people = %q, want %q", c.query, peopleNames(got), c.people)
		}
	}
}

func TestNamesCarriesWhatARowNeeds(t *testing.T) {
	f := namesFixture(t)
	got := f.names("alice")
	if len(got.Chats) != 1 || len(got.People) != 1 {
		t.Fatalf("names = %+v", got)
	}
	chat, person := got.Chats[0], got.People[0]
	if chat.ChatID != f.id("chats", "c1") || chat.AccountID != f.acc || chat.Kind != model.ChatPrivate ||
		!chat.LastAt.Equal(day.Add(time.Minute)) {
		t.Errorf("chat = %+v", chat)
	}
	if person.SenderID != f.id("senders", "u1") || person.AccountID != f.acc || person.Username != "alice_w" {
		t.Errorf("person = %+v", person)
	}
}

func TestNamesSkipsEmptyChatsAndNamelessPeople(t *testing.T) {
	f := namesFixture(t)
	if got := chatTitles(f.names("empty")); len(got) != 0 {
		t.Errorf("a chat with no messages was listed: %q", got)
	}
	if got := peopleNames(f.names("stranger")); len(got) != 0 {
		t.Errorf("a sender with no name was listed: %q", got)
	}
}

func TestNamesRanksWholeWordsThenRecentChats(t *testing.T) {
	f := newFixture(t, "en")
	f.chat("old", model.ChatPrivate, "Alice Old")
	f.chat("new", model.ChatPrivate, "Alice New")
	f.chat("al", model.ChatPrivate, "Al")
	f.say("first", func(m *model.Message) { m.ChatSourceID = "al" }) // the oldest chat
	f.say("second", func(m *model.Message) { m.ChatSourceID = "old" })
	f.say("third", func(m *model.Message) { m.ChatSourceID = "new" })

	if got := chatTitles(f.names("al")); !slices.Equal(got, []string{"[Al]", "[Alice] New", "[Alice] Old"}) {
		t.Errorf("order = %q; want the whole-word match first, then the most recent", got)
	}
}

func TestNamesLimit(t *testing.T) {
	f := newFixture(t, "en")
	for i, id := range []string{"a", "b", "c"} {
		f.chat(id, model.ChatPrivate, "Team "+id)
		f.say("x", func(m *model.Message) { m.ChatSourceID = id })
		_ = i
	}
	got, err := f.search.Names(context.Background(), "team", nil, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Chats) != 2 {
		t.Errorf("%d chats with limit 2", len(got.Chats))
	}
	if got := f.names("team"); len(got.Chats) != 3 {
		t.Errorf("%d chats with the default limit, want 3", len(got.Chats))
	}
}

func TestNamesInUnspacedScripts(t *testing.T) {
	f := newFixture(t, "en")
	f.chat("jp", model.ChatPrivate, "田中太郎")
	f.say("こんにちは", func(m *model.Message) { m.ChatSourceID = "jp" })
	for _, query := range []string{"田中", "太郎", "田", "郎"} {
		if got := f.names(query); len(got.Chats) != 1 {
			t.Errorf("%q found %d chats, want 1", query, len(got.Chats))
		}
	}
}

func TestNamesKeepToTheChosenAccounts(t *testing.T) {
	f := namesFixture(t)
	other, err := f.store.CreateAccount("test", "Other", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.store.UpsertChat(other, model.Chat{SourceID: "x", Kind: model.ChatPrivate, Title: "Alice Elsewhere"}); err != nil {
		t.Fatal(err)
	}
	if err := f.store.UpsertSender(other, model.Sender{SourceID: "ux", DisplayName: "Alice Elsewhere"}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.store.UpsertMessage(other, model.Message{ChatSourceID: "x", SourceID: "1", SenderSourceID: "ux", SentAt: day, Text: "hi"}); err != nil {
		t.Fatal(err)
	}
	for name, c := range map[string]struct {
		accounts      []int64
		chats, people []string
	}{
		"every account":    {nil, []string{"[Alice] Elsewhere", "[Alice] Smith"}, []string{"[Alice] Elsewhere", "[Alice] Smith"}},
		"the first only":   {[]int64{f.acc}, []string{"[Alice] Smith"}, []string{"[Alice] Smith"}},
		"the second only":  {[]int64{other}, []string{"[Alice] Elsewhere"}, []string{"[Alice] Elsewhere"}},
		"an empty account": {[]int64{9999}, nil, nil},
	} {
		got, err := f.search.Names(context.Background(), "alice", c.accounts, 0)
		if err != nil {
			t.Fatal(err)
		}
		chats, people := chatTitles(got), peopleNames(got)
		slices.Sort(chats)
		slices.Sort(people)
		if !slices.Equal(chats, c.chats) || !slices.Equal(people, c.people) {
			t.Errorf("%s: chats %q, people %q; want %q and %q", name, chats, people, c.chats, c.people)
		}
	}
}

func TestNamesCarryPictures(t *testing.T) {
	f := namesFixture(t)
	if err := f.store.PutAvatar(f.acc, model.AvatarRef{Kind: model.AvatarSender, SourceID: "u1", AvatarID: "p1"}, strings.NewReader("picture")); err != nil {
		t.Fatal(err)
	}
	got, err := f.search.Names(context.Background(), "alice", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	var pictured int
	for _, p := range got.People {
		if p.AvatarSHA != "" {
			pictured++
		}
	}
	if pictured != 1 {
		t.Errorf("people with pictures = %d, want 1: %+v", pictured, got.People)
	}
	for _, c := range got.Chats {
		if c.AvatarSHA != "" {
			t.Errorf("a chat without a picture has one: %+v", c)
		}
	}
}
