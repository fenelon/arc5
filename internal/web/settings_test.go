package web

import (
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestTopBarLinksToSettings(t *testing.T) {
	s := newSite(t, "")
	s.signIn()
	acc := s.account("Personal", "telegram")
	s.say(acc, "Dan Whitfield", "1", "hello", noon)
	for _, path := range []string{"/s/chats", "/sources", "/settings"} {
		body := s.page(path)
		has(t, body, `href="/settings"`)
		topbar := body[strings.Index(body, `<header class="topbar"`):strings.Index(body, "</header>")]
		if strings.Contains(topbar, `action="/logout"`) {
			t.Errorf("%s: the top bar still signs out; that moved to Settings", path)
		}
	}
}

func TestSettingsPage(t *testing.T) {
	s := newSite(t, "")
	s.signIn()
	body := s.page("/settings")
	has(t, body, "<h1>Settings</h1>", "Appearance", `name="theme" value="" checked`,
		"Search languages", `name="lang" value="en" checked`, `name="lang" value="ru">`, "Russian", "Turkish",
		"Save and rebuild the index", "The index is up to date for English.",
		"Largest automatic download, in MB", `name="limit_mb" type="number" min="0" max="100000" value="50"`,
		`name="current"`, `name="new"`, "Change password",
		`href="/sources">Manage sources`, `action="/logout"`)
	if n := strings.Count(body, `name="lang"`); n != 18 {
		t.Errorf("%d language checkboxes, want 18", n)
	}
	// Languages are listed by name, as people look for them.
	if strings.Index(body, ">Dutch<") > strings.Index(body, ">English<") || strings.Index(body, ">Spanish<") > strings.Index(body, ">Swedish<") {
		t.Error("languages are not in the order of their names")
	}
	// The theme applies as soon as it is chosen; "Save" is for browsers without scripts.
	has(t, body, `<form method="post" action="/settings/appearance" class="choices" data-controller="jump">`,
		`name="theme" value="dark" data-action="jump#go"`, `data-jump-target="button">Save</button>`)
}

func TestSettingsAppearance(t *testing.T) {
	s := newSite(t, "")
	s.signIn()
	if rec := s.post("/settings/appearance", url.Values{"theme": {"dark"}}); !redirectsTo(rec, "/settings?saved=appearance") {
		t.Fatalf("saving the theme: %d to %q", rec.Code, rec.Header().Get("Location"))
	}
	if c := s.cookies["arc5_theme"]; c == nil || c.Value != "dark" {
		t.Fatalf("theme cookie = %+v", c)
	}
	has(t, s.page("/settings?saved=appearance"), `data-theme="dark"`, `name="theme" value="dark" checked`, "Saved.")
	s.post("/settings/appearance", url.Values{"theme": {""}})
	if _, set := s.cookies["arc5_theme"]; set {
		t.Error("following the device kept the theme cookie")
	}
	s.post("/settings/appearance", url.Values{"theme": {`"><script>`}})
	if _, set := s.cookies["arc5_theme"]; set {
		t.Error("a made-up theme was stored")
	}
}

func TestSettingsLanguages(t *testing.T) {
	s := newSite(t, "")
	s.signIn()
	rec := s.post("/settings/languages", url.Values{"lang": {"ru", "en"}})
	if !redirectsTo(rec, "/settings?saved=languages#languages") {
		t.Fatalf("saving languages: %d to %q", rec.Code, rec.Header().Get("Location"))
	}
	waitForIndex(t, s)
	if got := s.index.Status().Languages; !slices.Equal(got, []string{"en", "ru"}) {
		t.Fatalf("languages = %v", got)
	}
	has(t, s.page("/settings"), `name="lang" value="ru" checked`, "The index is up to date for English and Russian.")

	bad := s.post("/settings/languages", url.Values{"lang": {"en", "klingon"}})
	if bad.Code != http.StatusUnprocessableEntity || !strings.Contains(bad.Body.String(), `role="alert"`) {
		t.Errorf("an unknown language: %d", bad.Code)
	}
	if got := s.index.Status().Languages; !slices.Equal(got, []string{"en", "ru"}) {
		t.Errorf("a refused change altered the languages: %v", got)
	}

	s.post("/settings/languages", nil)
	waitForIndex(t, s)
	has(t, s.page("/settings"), "No languages are ticked, so search finds exact words only.")
}

func TestSettingsAttachmentLimit(t *testing.T) {
	s := newSite(t, "")
	s.signIn()
	if rec := s.post("/settings/attachments", url.Values{"limit_mb": {"10"}}); !redirectsTo(rec, "/settings?saved=attachments#attachments") {
		t.Fatalf("saving the limit: %d to %q", rec.Code, rec.Header().Get("Location"))
	}
	if limit, _ := s.store.MediaCap(); limit != 10<<20 {
		t.Fatalf("media cap = %d", limit)
	}
	has(t, s.page("/settings"), `value="10"`)
	for _, bad := range []string{"", "ten", "-1", "1.5", "100001"} {
		rec := s.post("/settings/attachments", url.Values{"limit_mb": {bad}})
		if rec.Code != http.StatusUnprocessableEntity || !strings.Contains(rec.Body.String(), `role="alert"`) {
			t.Errorf("limit %q: %d", bad, rec.Code)
		}
	}
	if limit, _ := s.store.MediaCap(); limit != 10<<20 {
		t.Errorf("a refused limit changed the cap to %d", limit)
	}
}

func TestSettingsPassword(t *testing.T) {
	s := newSite(t, "")
	s.signIn()
	other := &site{t: t, store: s.store, srv: s.srv, cookies: map[string]*http.Cookie{}}
	if rec := other.post("/login", url.Values{"password": {testPassword}}); !redirectsTo(rec, "/") {
		t.Fatalf("second browser login: %d", rec.Code)
	}

	for name, f := range map[string]url.Values{
		"wrong current": {"current": {"wrong horse"}, "new": {"battery staple"}},
		"short new":     {"current": {testPassword}, "new": {"short"}},
	} {
		rec := s.post("/settings/password", f)
		if rec.Code != http.StatusUnprocessableEntity || !strings.Contains(rec.Body.String(), `role="alert"`) {
			t.Errorf("%s: %d", name, rec.Code)
		}
		if strings.Contains(rec.Body.String(), "battery staple") || strings.Contains(rec.Body.String(), "wrong horse") {
			t.Errorf("%s: the page echoes a password", name)
		}
	}

	rec := s.post("/settings/password", url.Values{"current": {testPassword}, "new": {"battery staple"}})
	if !redirectsTo(rec, "/settings?saved=password#password") {
		t.Fatalf("changing the password: %d to %q", rec.Code, rec.Header().Get("Location"))
	}
	if body := s.page("/settings?saved=password"); !strings.Contains(body, "Every other browser is signed out.") {
		t.Error("the page does not confirm the change")
	}
	if rec := other.get("/"); !redirectsTo(rec, "/login") {
		t.Errorf("the other browser is still signed in: %d", rec.Code)
	}
	if ok, _ := s.srv.auth.check("battery staple"); !ok {
		t.Error("the new password does not work")
	}
}

func TestSettingsPasswordFromTheEnvironment(t *testing.T) {
	s := newSite(t, "from-env")
	if rec := s.post("/login", url.Values{"password": {"from-env"}}); !redirectsTo(rec, "/") {
		t.Fatalf("login: %d", rec.Code)
	}
	body := s.page("/settings")
	has(t, body, "ARC5_PASSWORD")
	lacks(t, body, `name="current"`)
	rec := s.post("/settings/password", url.Values{"current": {"from-env"}, "new": {"battery staple"}})
	if rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("changing an environment password: %d", rec.Code)
	}
}

// The picker remembers the last source opened, for Settings, Sources and home.
func TestPickerRemembersTheSource(t *testing.T) {
	s := newSite(t, "")
	s.signIn()
	personal := s.account("Personal", "telegram")
	work := s.account("Work", "telegram")
	s.say(personal, "Dan", "1", "hello", noon)
	s.say(work, "Ana", "1", "hola", noon)
	if rec := s.get("/"); !redirectsTo(rec, "/s/chats") {
		t.Fatalf("home before any source was opened: %q", rec.Header().Get("Location"))
	}
	s.page(fmt.Sprintf("/s/%d", work))
	if rec := s.get("/"); !redirectsTo(rec, fmt.Sprintf("/s/%d", work)) {
		t.Errorf("home after opening Work: %q", rec.Header().Get("Location"))
	}
	has(t, s.page("/settings"), fmt.Sprintf(`class="tile tile--current" href="/s/%d"`, work))

	s.cookies["arc5_source"] = &http.Cookie{Name: "arc5_source", Value: "9999"}
	if rec := s.get("/"); !redirectsTo(rec, "/s/chats") {
		t.Errorf("home with a source that is gone: %q", rec.Header().Get("Location"))
	}
}

// waitForIndex waits until no rebuild is running.
func waitForIndex(t *testing.T, s *site) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for s.index.Status().Rebuilding {
		if time.Now().After(deadline) {
			t.Fatal("the index rebuild never finished")
		}
		time.Sleep(5 * time.Millisecond)
	}
}
