package web

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/fenelon/arc5/internal/archive"
	"github.com/fenelon/arc5/internal/connector"
	"github.com/fenelon/arc5/internal/indexer"
)

type fakeRunner struct {
	mu         sync.Mutex
	status     []connector.Status
	requested  []int64
	restarted  []int64
	signedOut  []int64
	err        error
	signOutErr error
}

func (f *fakeRunner) Status() []connector.Status {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]connector.Status(nil), f.status...)
}

func (f *fakeRunner) RequestMedia(id int64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.requested = append(f.requested, id)
	return f.err
}

func (f *fakeRunner) Restart(id int64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.restarted = append(f.restarted, id)
	return nil
}

func (f *fakeRunner) SignOut(_ context.Context, id int64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.signedOut = append(f.signedOut, id)
	return f.signOutErr
}

// site is a server with a browser's memory: it keeps cookies and the CSRF
// token from the last page it saw.
type site struct {
	t       *testing.T
	store   *archive.Store
	runner  *fakeRunner
	reg     *connector.Registry
	index   *indexer.Indexer
	srv     *Server
	cookies map[string]*http.Cookie
	csrf    string
}

func newSite(t *testing.T, envPassword string) *site {
	t.Helper()
	store, err := archive.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	index, err := indexer.Load(store)
	if err != nil {
		t.Fatal(err)
	}
	index.Logf = func(string, ...any) {}
	t.Cleanup(index.Wait) // registered after the store's cleanup, so it runs first
	s := &site{t: t, store: store, runner: &fakeRunner{}, reg: connector.NewRegistry(), index: index, cookies: map[string]*http.Cookie{}}
	s.srv, err = New(Deps{Store: store, Runner: s.runner, Registry: s.reg, Index: index, EnvPassword: envPassword, Location: time.UTC})
	if err != nil {
		t.Fatal(err)
	}
	return s
}

var csrfField = regexp.MustCompile(`name="csrf" (?:value|content)="([^"]+)"`)

func (s *site) do(req *http.Request) *httptest.ResponseRecorder {
	s.t.Helper()
	for _, c := range s.cookies {
		req.AddCookie(c)
	}
	rec := httptest.NewRecorder()
	s.srv.ServeHTTP(rec, req)
	for _, c := range rec.Result().Cookies() {
		if c.MaxAge < 0 {
			delete(s.cookies, c.Name)
		} else {
			s.cookies[c.Name] = c
		}
	}
	if m := csrfField.FindStringSubmatch(rec.Body.String()); m != nil {
		s.csrf = m[1]
	}
	return rec
}

func (s *site) get(path string) *httptest.ResponseRecorder {
	s.t.Helper()
	return s.do(httptest.NewRequest(http.MethodGet, path, nil))
}

// post sends a form. It adds the CSRF token unless the form already has one.
func (s *site) post(path string, form url.Values) *httptest.ResponseRecorder {
	s.t.Helper()
	if form == nil {
		form = url.Values{}
	}
	if _, set := form["csrf"]; !set && s.csrf != "" {
		form.Set("csrf", s.csrf)
	}
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	return s.do(req)
}

const testPassword = "correct horse"

// signIn sets the password on a new archive and leaves the site signed in.
func (s *site) signIn() {
	s.t.Helper()
	rec := s.post("/setup", url.Values{"password": {testPassword}, "password_again": {testPassword}})
	if rec.Code != http.StatusSeeOther {
		s.t.Fatalf("setup: status %d, body %s", rec.Code, rec.Body)
	}
	s.get("/") // pick up the CSRF token from a page
	if s.csrf == "" {
		s.t.Fatal("no CSRF token on the home page")
	}
}

func redirectsTo(rec *httptest.ResponseRecorder, path string) bool {
	return rec.Code == http.StatusSeeOther && rec.Header().Get("Location") == path
}

func TestHealthIsPublic(t *testing.T) {
	rec := newSite(t, "").get("/healthz")
	if rec.Code != http.StatusOK || strings.TrimSpace(rec.Body.String()) != `{"status":"ok"}` {
		t.Fatalf("healthz = %d %q", rec.Code, rec.Body)
	}
}

func TestStaticFiles(t *testing.T) {
	s := newSite(t, "")
	css := s.get(s.srv.asset("app.css"))
	if css.Code != http.StatusOK || !strings.HasPrefix(css.Header().Get("Content-Type"), "text/css") ||
		!strings.Contains(css.Header().Get("Cache-Control"), "immutable") || !strings.Contains(css.Body.String(), "--accent") {
		t.Errorf("app.css: %d %v", css.Code, css.Header())
	}
	for _, name := range []string{"js/app.js", "js/stimulus.js", "js/theme_controller.js", "js/picker_controller.js", "js/search_controller.js",
		"fonts/geologica-latin-wght-normal.woff2", "fonts/geologica-cyrillic-wght-normal.woff2"} {
		if rec := s.get(s.srv.asset(name)); rec.Code != http.StatusOK || rec.Body.Len() == 0 {
			t.Errorf("%s: status %d, %d bytes", name, rec.Code, rec.Body.Len())
		}
	}
	for _, path := range []string{"/static/x/nope.css", "/static/x/", "/static/x/js/", "/static/x/fonts"} {
		if rec := s.get(path); rec.Code != http.StatusNotFound {
			t.Errorf("%s: status %d, want 404", path, rec.Code)
		}
	}
}

func TestFirstRunSetup(t *testing.T) {
	s := newSite(t, "")
	for _, path := range []string{"/", "/login"} {
		if rec := s.get(path); !redirectsTo(rec, "/setup") {
			t.Errorf("GET %s before setup: %d to %q, want a redirect to /setup", path, rec.Code, rec.Header().Get("Location"))
		}
	}
	form := s.get("/setup")
	if form.Code != http.StatusOK || !strings.Contains(form.Body.String(), `name="password_again"`) {
		t.Fatalf("setup form: %d", form.Code)
	}
	if got := form.Header().Get("Content-Security-Policy"); got != csp {
		t.Errorf("policy = %q", got)
	}

	for name, f := range map[string]url.Values{
		"too short": {"password": {"short"}, "password_again": {"short"}},
		"mismatch":  {"password": {testPassword}, "password_again": {"different horse"}},
	} {
		rec := s.post("/setup", f)
		if rec.Code != http.StatusUnprocessableEntity || !strings.Contains(rec.Body.String(), `role="alert"`) {
			t.Errorf("%s: status %d", name, rec.Code)
		}
		if strings.Contains(rec.Body.String(), "different horse") || strings.Contains(rec.Body.String(), testPassword) {
			t.Errorf("%s: the page echoes a password", name)
		}
	}
	s.signIn()
	if rec := s.get("/"); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "No sources yet") ||
		!strings.Contains(rec.Body.String(), `href="/sources/new"`) {
		t.Fatalf("home after setup: %d", rec.Code)
	}
}

// Review Focus 3: setup cannot be replayed.
func TestSetupIsClosedOnceAPasswordExists(t *testing.T) {
	s := newSite(t, "")
	s.signIn()
	stranger := &site{t: t, store: s.store, srv: s.srv, cookies: map[string]*http.Cookie{}}
	if rec := stranger.get("/setup"); !redirectsTo(rec, "/login") {
		t.Errorf("GET /setup: %d to %q", rec.Code, rec.Header().Get("Location"))
	}
	rec := stranger.post("/setup", url.Values{"password": {"attacker pass"}, "password_again": {"attacker pass"}})
	if !redirectsTo(rec, "/login") || len(stranger.cookies) != 0 {
		t.Fatalf("POST /setup with a password set: %d, cookies %v", rec.Code, stranger.cookies)
	}
	if ok, _ := s.srv.auth.check("attacker pass"); ok {
		t.Fatal("the password was replaced through /setup")
	}
	if ok, _ := s.srv.auth.check(testPassword); !ok {
		t.Fatal("the original password no longer works")
	}

	env := newSite(t, "from-env")
	if rec := env.post("/setup", url.Values{"password": {"attacker pass"}, "password_again": {"attacker pass"}}); !redirectsTo(rec, "/login") {
		t.Errorf("POST /setup with ARC5_PASSWORD set: %d", rec.Code)
	}
}

func TestLoginAndLogout(t *testing.T) {
	s := newSite(t, "")
	s.signIn()
	if rec := s.post("/logout", nil); !redirectsTo(rec, "/login") {
		t.Fatalf("logout: %d to %q", rec.Code, rec.Header().Get("Location"))
	}
	if rec := s.get("/"); !redirectsTo(rec, "/login") {
		t.Fatalf("home after logout: %d", rec.Code)
	}
	wrong := s.post("/login", url.Values{"password": {"wrong horse"}})
	if wrong.Code != http.StatusUnauthorized || !strings.Contains(wrong.Body.String(), `role="alert"`) || len(s.cookies) != 0 {
		t.Fatalf("wrong password: %d, cookies %v", wrong.Code, s.cookies)
	}
	if rec := s.post("/login", url.Values{"password": {testPassword}}); !redirectsTo(rec, "/") {
		t.Fatalf("right password: %d", rec.Code)
	}
	if rec := s.get("/"); rec.Code != http.StatusOK {
		t.Fatalf("home after login: %d", rec.Code)
	}
	if rec := s.get("/login"); !redirectsTo(rec, "/") {
		t.Errorf("login page while signed in: %d", rec.Code)
	}
}

func TestLoginIsThrottled(t *testing.T) {
	s := newSite(t, "from-env")
	for range 5 {
		s.post("/login", url.Values{"password": {"wrong"}})
	}
	rec := s.post("/login", url.Values{"password": {"from-env"}})
	if rec.Code != http.StatusTooManyRequests || len(s.cookies) != 0 {
		t.Fatalf("login while locked out: %d, cookies %v", rec.Code, s.cookies)
	}
	if !strings.Contains(rec.Body.String(), "Try again in") {
		t.Errorf("the page does not say when to try again: %s", rec.Body)
	}
}

// Review Focus 2: no page without a session, no change without the CSRF token.
func TestPagesNeedASessionAndPostsNeedTheToken(t *testing.T) {
	s := newSite(t, "")
	s.signIn()
	token := s.csrf

	stranger := &site{t: t, store: s.store, srv: s.srv, cookies: map[string]*http.Cookie{}}
	if rec := stranger.get("/"); !redirectsTo(rec, "/login") {
		t.Errorf("home without a session: %d", rec.Code)
	}
	if rec := stranger.post("/logout", url.Values{"csrf": {token}}); !redirectsTo(rec, "/login") {
		t.Errorf("logout without a session: %d", rec.Code)
	}

	for name, f := range map[string]url.Values{"no token": {"csrf": {""}}, "wrong token": {"csrf": {"made-up"}}} {
		if rec := s.post("/logout", f); rec.Code != http.StatusForbidden {
			t.Errorf("logout with %s: %d, want 403", name, rec.Code)
		}
	}
	if rec := s.get("/"); rec.Code != http.StatusOK {
		t.Fatal("a refused logout signed the owner out")
	}
}

func TestPagesCarryThemeAndHeaders(t *testing.T) {
	s := newSite(t, "")
	s.signIn()
	plain := s.get("/")
	if strings.Contains(plain.Body.String(), "data-theme=") {
		t.Error("a theme is forced although none was chosen")
	}
	for header, want := range map[string]string{
		"Content-Security-Policy": csp, "X-Content-Type-Options": "nosniff",
		"Referrer-Policy": "same-origin", "Cache-Control": "no-store",
	} {
		if got := plain.Header().Get(header); got != want {
			t.Errorf("%s = %q, want %q", header, got, want)
		}
	}
	if strings.Contains(plain.Body.String(), " style=") || strings.Contains(plain.Body.String(), "<style") {
		t.Error("the page has inline styles, which the policy blocks")
	}

	s.cookies["arc5_theme"] = &http.Cookie{Name: "arc5_theme", Value: "dark"}
	if !strings.Contains(s.get("/").Body.String(), `data-theme="dark"`) {
		t.Error("the dark theme cookie is ignored")
	}
	s.cookies["arc5_theme"] = &http.Cookie{Name: "arc5_theme", Value: `"><script>`}
	if body := s.get("/").Body.String(); strings.Contains(body, "data-theme=") || strings.Contains(body, "<script>") {
		t.Error("an invalid theme cookie reached the page")
	}
}

// Wrong passwords sent all at once are checked one at a time, so the throttle
// holds and the server never runs many password checks side by side.
func TestParallelLoginsAreThrottled(t *testing.T) {
	s := newSite(t, "")
	s.signIn()
	const tries = 40
	codes := make(chan int, tries)
	var wg sync.WaitGroup
	for range tries {
		wg.Add(1)
		go func() {
			defer wg.Done()
			req := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader("password=wrong+horse"))
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			rec := httptest.NewRecorder()
			s.srv.ServeHTTP(rec, req)
			codes <- rec.Code
		}()
	}
	wg.Wait()
	close(codes)
	checked, refused := 0, 0
	for code := range codes {
		switch code {
		case http.StatusUnauthorized:
			checked++
		case http.StatusTooManyRequests:
			refused++
		default:
			t.Errorf("unexpected status %d", code)
		}
	}
	if checked > 5 || checked+refused != tries {
		t.Fatalf("%d wrong passwords were checked and %d refused; want at most 5 checked", checked, refused)
	}
}

// Review Focus 2, for every route: no page without a session, and no change
// without the session's token.
func TestEveryRouteNeedsASessionAndEveryChangeTheToken(t *testing.T) {
	s := newSite(t, "")
	s.signIn()
	acc := s.fakeAccount("Personal")
	s.say(acc, "Dan", "1", "hello", noon)
	chat := s.chatID(acc, "Dan")
	gets := []string{"/", "/s/chats", fmt.Sprintf("/s/%d", acc), fmt.Sprintf("/s/%d/c/%d", acc, chat), fmt.Sprintf("/s/%d/results?q=x", acc),
		"/media/1", "/sources", "/sources/new", fmt.Sprintf("/sources/%d", acc), fmt.Sprintf("/sources/%d/chats", acc),
		fmt.Sprintf("/sources/%d/login", acc), "/settings"}
	posts := []string{"/logout", "/media/1/fetch", "/sources/new", fmt.Sprintf("/sources/%d/name", acc), fmt.Sprintf("/sources/%d/settings", acc),
		fmt.Sprintf("/sources/%d/signout", acc), fmt.Sprintf("/sources/%d/remove", acc), fmt.Sprintf("/sources/%d/chats", acc),
		fmt.Sprintf("/sources/%d/login", acc), fmt.Sprintf("/sources/%d/login/cancel", acc),
		"/settings/appearance", "/settings/languages", "/settings/attachments", "/settings/password"}

	stranger := &site{t: t, store: s.store, srv: s.srv, cookies: map[string]*http.Cookie{}}
	for _, path := range gets {
		if rec := stranger.get(path); !redirectsTo(rec, "/login") {
			t.Errorf("GET %s without a session: %d to %q", path, rec.Code, rec.Header().Get("Location"))
		}
	}
	for _, path := range posts {
		if rec := stranger.post(path, url.Values{"csrf": {s.csrf}}); !redirectsTo(rec, "/login") {
			t.Errorf("POST %s without a session: %d", path, rec.Code)
		}
		if rec := s.post(path, url.Values{"csrf": {"made-up"}}); rec.Code != http.StatusForbidden {
			t.Errorf("POST %s with a wrong token: %d, want 403", path, rec.Code)
		}
	}
	if got, _ := s.store.Account(acc); !got.RemovedAt.IsZero() || got.Label != "Personal" {
		t.Error("a refused request changed the source")
	}
}
