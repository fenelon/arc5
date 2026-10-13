package web

import (
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"testing"

	"github.com/fenelon/arc5/internal/model"
)

func TestChooseArchivedChats(t *testing.T) {
	s := newSite(t, "")
	s.signIn()
	acc := s.fakeAccount("Personal")
	other := s.fakeAccount("Work")
	s.say(acc, "Dan", "1", "hello", noon)
	for _, c := range []model.Chat{{SourceID: "news", Kind: model.ChatChannel, Title: "Rail news"}, {SourceID: "g1", Kind: model.ChatGroup, Title: "Hikers"}} {
		if err := s.store.UpsertChat(acc, c); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.store.UpsertChat(other, model.Chat{SourceID: "x", Kind: model.ChatPrivate, Title: "Elsewhere"}); err != nil {
		t.Fatal(err)
	}
	chats, _ := s.store.AccountChats(acc)
	id := map[string]int64{}
	for _, c := range chats {
		id[c.SourceID] = c.ID
	}
	elsewhere, _ := s.store.AccountChats(other)
	path := fmt.Sprintf("/sources/%d/chats", acc)

	body := s.page(path)
	has(t, body, "<h1>Chats of Personal</h1>", fmt.Sprintf(`href="/sources/%d">Back to Personal`, acc),
		"Private chats", "Groups", "Channels",
		fmt.Sprintf(`name="archived" value="%d" checked`, id["Dan"]),
		fmt.Sprintf(`name="archived" value="%d" checked`, id["g1"]),
		fmt.Sprintf(`name="archived" value="%d">`, id["news"]),
		"1 message, the last on 28 Sep", "Nothing archived yet")

	form := url.Values{"archived": {strconv.FormatInt(id["news"], 10)}}
	for _, c := range append(chats, elsewhere...) {
		form.Add("shown", strconv.FormatInt(c.ID, 10))
	}
	rec := s.post(path, form)
	if !redirectsTo(rec, path+"?saved=1") {
		t.Fatalf("save: %d to %q", rec.Code, rec.Header().Get("Location"))
	}
	for source, want := range map[string]bool{"Dan": false, "g1": false, "news": true} {
		if got, _ := s.store.ChatArchived(acc, source); got != want {
			t.Errorf("chat %s archived = %v, want %v", source, got, want)
		}
	}
	if got, _ := s.store.ChatArchived(other, "x"); !got {
		t.Error("a chat of another source was switched off")
	}
	if !slices.Contains(s.runner.restarted, acc) {
		t.Error("the connector was not restarted")
	}
	has(t, s.page(path+"?saved=1"), "Saved.")

	if rec := s.post(path, url.Values{"shown": {"banana"}}); rec.Code != http.StatusBadRequest {
		t.Errorf("a made-up chat ID: %d", rec.Code)
	}
	if rec := s.get("/sources/999/chats"); rec.Code != http.StatusNotFound {
		t.Errorf("unknown source: %d", rec.Code)
	}
	s.post(fmt.Sprintf("/sources/%d/remove", acc), nil)
	if rec := s.post(path, form); rec.Code != http.StatusConflict {
		t.Errorf("changing a removed source's chats: %d", rec.Code)
	}
}
