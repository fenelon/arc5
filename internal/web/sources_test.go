package web

import (
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/fenelon/arc5/internal/connector"
)

func TestSourcesPageShowsTheChosenSource(t *testing.T) {
	t.Parallel()
	s := newSite(t, "")
	s.signIn()
	personal, work := s.fakeAccount("Personal"), s.fakeAccount("Work")
	s.say(personal, "Dan", "1", "hello", noon)
	s.say(personal, "Ana", "1", "hola", noon)

	body := s.page("/sources")
	has(t, body, "Settings for Personal", fmt.Sprintf(`href="/sources/%d"`, work),
		fmt.Sprintf(`href="/s/%d">Open its chats`, personal),
		`name="name" value="Personal"`,
		fmt.Sprintf(`href="/sources/%d/chats">Choose which of the 2 chats are archived`, personal),
		`<select id="field-speed" name="speed">`, `<option value="Normal" selected>Normal</option>`,
		`<input id="field-keep" name="keep" type="checkbox" value="true">`,
		`<input id="field-months" name="months" type="number" value="12">`,
		`<input id="field-token" name="token" type="password" value="" autocomplete="off"`,
		"Sign this source out", "Remove this source and keep its archive")
	if c := strings.Count(body, "tile--current"); c != 1 {
		t.Errorf("%d tiles marked current", c)
	}
	has(t, s.page(fmt.Sprintf("/sources/%d", work)), "Settings for Work", `name="name" value="Work"`)

	s.page(fmt.Sprintf("/s/%d", work))
	has(t, s.page("/sources"), "Settings for Work")

	for _, path := range []string{"/sources/999", "/sources/banana"} {
		if rec := s.get(path); rec.Code != http.StatusNotFound {
			t.Errorf("GET %s: %d, want 404", path, rec.Code)
		}
	}
}

func TestSourceTilesSayWhatTheyAreDoing(t *testing.T) {
	t.Parallel()
	s := newSite(t, "")
	s.signIn()
	live, catching := s.fakeAccount("Personal"), s.fakeAccount("Work")
	s.say(live, "Dan", "1", "hello", noon)
	s.srv.deps.Now = func() time.Time { return noon.Add(2 * time.Minute) }
	s.runner.status = []connector.Status{
		{AccountID: live, State: connector.StateRunning, Progress: connector.Progress{Phase: connector.PhaseLive}},
		{AccountID: catching, State: connector.StateRunning, Progress: connector.Progress{Phase: connector.PhaseBackfill, ChatsDone: 37, ChatsTotal: 112}},
	}
	has(t, s.page("/sources"), "Fake, last message 2 minutes ago", "Fake, catching up on history, 37 of 112 chats")
	has(t, s.page(fmt.Sprintf("/sources/%d", catching)), `<progress class="progress" max="112" value="37">`, "37 of 112 chats")
}

func TestRenameSource(t *testing.T) {
	t.Parallel()
	s := newSite(t, "")
	s.signIn()
	acc := s.fakeAccount("Personal")
	rec := s.post(fmt.Sprintf("/sources/%d/name", acc), url.Values{"name": {"  Home phone "}})
	if !redirectsTo(rec, fmt.Sprintf("/sources/%d?saved=name", acc)) {
		t.Fatalf("rename: %d to %q", rec.Code, rec.Header().Get("Location"))
	}
	if got, _ := s.store.Account(acc); got.Label != "Home phone" {
		t.Errorf("label = %q", got.Label)
	}
	bad := s.post(fmt.Sprintf("/sources/%d/name", acc), url.Values{"name": {"   "}})
	if bad.Code != http.StatusUnprocessableEntity || !strings.Contains(bad.Body.String(), `role="alert"`) {
		t.Errorf("an empty name: %d", bad.Code)
	}
}

func TestSaveSourceSettings(t *testing.T) {
	t.Parallel()
	s := newSite(t, "")
	s.signIn()
	acc := s.fakeAccount("Personal")
	if err := s.store.UpdateAccountSettings(acc, map[string]string{"token": "old-secret", "internal": "kept"}); err != nil {
		t.Fatal(err)
	}
	form := url.Values{"speed": {"Fast"}, "keep": {"true"}, "months": {"6"}, "note": {"hi"}, "token": {""}}
	rec := s.post(fmt.Sprintf("/sources/%d/settings", acc), form)
	if !redirectsTo(rec, fmt.Sprintf("/sources/%d?saved=settings", acc)) {
		t.Fatalf("save: %d to %q", rec.Code, rec.Header().Get("Location"))
	}
	got, _ := s.store.Account(acc)
	want := map[string]string{"speed": "Fast", "keep": "true", "months": "6", "note": "hi", "token": "old-secret", "internal": "kept"}
	for k, v := range want {
		if got.Settings[k] != v {
			t.Errorf("setting %s = %q, want %q", k, got.Settings[k], v)
		}
	}
	if !slices.Contains(s.runner.restarted, acc) {
		t.Error("the connector was not restarted with its new settings")
	}
	has(t, s.page(fmt.Sprintf("/sources/%d?saved=settings", acc)), `<option value="Fast" selected>`, `name="keep" type="checkbox" value="true" checked`, "Saved.")
	lacks(t, s.page(fmt.Sprintf("/sources/%d", acc)), "old-secret")

	s.post(fmt.Sprintf("/sources/%d/settings", acc), url.Values{"speed": {"Fast"}, "months": {"6"}, "token": {"new-secret"}})
	got, _ = s.store.Account(acc)
	if got.Settings["keep"] != "" || got.Settings["token"] != "new-secret" {
		t.Errorf("unticking and a new secret: %v", got.Settings)
	}

	for name, f := range map[string]url.Values{
		"a choice not offered": {"speed": {"Ludicrous"}, "months": {"6"}},
		"not a number":         {"speed": {"Fast"}, "months": {"six"}},
	} {
		rec := s.post(fmt.Sprintf("/sources/%d/settings", acc), f)
		if rec.Code != http.StatusUnprocessableEntity || !strings.Contains(rec.Body.String(), `role="alert"`) {
			t.Errorf("%s: %d", name, rec.Code)
		}
	}
}

func TestSignOutSource(t *testing.T) {
	t.Parallel()
	s := newSite(t, "")
	s.signIn()
	acc := s.fakeAccount("Personal")
	rec := s.post(fmt.Sprintf("/sources/%d/signout", acc), nil)
	if !redirectsTo(rec, fmt.Sprintf("/sources/%d?saved=signout", acc)) || !slices.Contains(s.runner.signedOut, acc) {
		t.Fatalf("sign out: %d to %q, signed out %v", rec.Code, rec.Header().Get("Location"), s.runner.signedOut)
	}
	plain := s.account("Old", "telegram") // no sign-out
	lacks(t, s.page(fmt.Sprintf("/sources/%d", plain)), "Sign this source out")
	if rec := s.post(fmt.Sprintf("/sources/%d/signout", plain), nil); rec.Code != http.StatusNotFound {
		t.Errorf("signing out a source that cannot: %d", rec.Code)
	}
}

func TestRemoveSourceKeepsItsArchive(t *testing.T) {
	t.Parallel()
	s := newSite(t, "")
	s.signIn()
	acc := s.fakeAccount("Personal")
	s.say(acc, "Dan", "1", "still here", noon)
	rec := s.post(fmt.Sprintf("/sources/%d/remove", acc), nil)
	if !redirectsTo(rec, "/sources?saved=removed") || !slices.Contains(s.runner.restarted, acc) {
		t.Fatalf("remove: %d to %q, restarted %v", rec.Code, rec.Header().Get("Location"), s.runner.restarted)
	}
	if got, _ := s.store.Account(acc); got.RemovedAt.IsZero() || got.Enabled {
		t.Fatalf("account after removal = %+v", got)
	}
	has(t, s.page("/sources?saved=removed"), "Removed, archive kept", "Fake, removed")
	panel := s.page(fmt.Sprintf("/sources/%d", acc))
	has(t, panel, "This source was removed on", `name="name" value="Personal"`)
	lacks(t, panel, "Sign this source out", "Remove this source", `name="speed"`)
	has(t, s.page(fmt.Sprintf("/s/%d", acc)), "still here")
	for _, action := range []string{"settings", "signout", "remove"} {
		if rec := s.post(fmt.Sprintf("/sources/%d/%s", acc, action), nil); rec.Code != http.StatusConflict {
			t.Errorf("%s on a removed source: %d, want 409", action, rec.Code)
		}
	}
}

// The tiles and the chosen source's state refresh themselves while the
// Sources page is open.
func TestSourcesPageKeepsItsStatesCurrent(t *testing.T) {
	t.Parallel()
	s := newSite(t, "")
	s.signIn()
	s.fakeAccount("Personal")
	has(t, s.page("/sources"),
		`<div id="source-tiles" data-controller="progress" data-progress-every-value="5000">`,
		`<div class="panel__cell" id="source-state" data-controller="progress" data-progress-every-value="5000">`)
	if rec := s.get(s.srv.asset("js/progress_controller.js")); rec.Code != http.StatusOK {
		t.Errorf("progress_controller.js: %d", rec.Code)
	}
}
