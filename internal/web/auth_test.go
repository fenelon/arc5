package web

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/fenelon/arc5/internal/archive"
)

func newAuth(t *testing.T, env string) (*auth, *archive.Store) {
	t.Helper()
	store, err := archive.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	return &auth{store: store, envPassword: env, now: time.Now}, store
}

func TestPasswordHashing(t *testing.T) {
	t.Parallel()
	h, err := hashPassword("correct horse")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(h, "argon2id$") || strings.Contains(h, "correct horse") {
		t.Fatalf("hash = %q", h)
	}
	again, _ := hashPassword("correct horse")
	if h == again {
		t.Error("two hashes of one password are identical; the salt is not random")
	}
	if !verifyPassword(h, "correct horse") {
		t.Error("the right password was rejected")
	}
	for _, wrong := range []string{"", "correct horsE", "correct horse "} {
		if verifyPassword(h, wrong) {
			t.Errorf("wrong password %q was accepted", wrong)
		}
	}
	for _, broken := range []string{"", "argon2id$", "plain$1$2$3$4$5", "argon2id$x$2$1$AAAA$AAAA", "argon2id$19456$2$1$***$AAAA",
		"argon2id$19456$0$1$AAAAAAAAAAAAAAAAAAAAAA$AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA", "argon2id$4294967295$2$1$AAAAAAAAAAAAAAAAAAAAAA$AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA",
		"argon2id$19456$2$255$AAAAAAAAAAAAAAAAAAAAAA$AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"} {
		if verifyPassword(broken, "correct horse") {
			t.Errorf("broken hash %q verified", broken)
		}
	}
}

func TestSetAndCheckPassword(t *testing.T) {
	t.Parallel()
	a, store := newAuth(t, "")
	if has, err := a.hasPassword(); err != nil || has {
		t.Fatalf("hasPassword on a new archive = %v, %v", has, err)
	}
	if ok, err := a.check("anything"); err != nil || ok {
		t.Fatalf("check with no password set = %v, %v", ok, err)
	}
	if err := a.setPassword("short"); !errors.Is(err, errShortPassword) {
		t.Fatalf("short password error = %v", err)
	}
	if err := store.CreateSession("old", "csrf", time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := a.setPassword("correct horse"); err != nil {
		t.Fatal(err)
	}
	if has, _ := a.hasPassword(); !has {
		t.Error("hasPassword is false after setPassword")
	}
	if ok, _ := a.check("correct horse"); !ok {
		t.Error("the new password was rejected")
	}
	if ok, _ := a.check("wrong horse"); ok {
		t.Error("a wrong password was accepted")
	}
	if _, err := store.Session("old"); !errors.Is(err, archive.ErrNotFound) {
		t.Error("changing the password left an old session signed in")
	}
}

func TestEnvironmentPassword(t *testing.T) {
	t.Parallel()
	a, store := newAuth(t, "from-env")
	if has, _ := a.hasPassword(); !has || !a.usesEnv() {
		t.Fatal("an environment password is not treated as set")
	}
	if ok, _ := a.check("from-env"); !ok {
		t.Error("the environment password was rejected")
	}
	if ok, _ := a.check("from-enV"); ok {
		t.Error("a wrong password was accepted")
	}
	if err := a.setPassword("correct horse"); !errors.Is(err, errEnvPassword) {
		t.Errorf("setPassword with an environment password = %v, want errEnvPassword", err)
	}

	// The first start with this password clears sessions; a restart with the same one keeps them.
	if err := store.CreateSession("s1", "csrf", time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := a.syncEnv(); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Session("s1"); !errors.Is(err, archive.ErrNotFound) {
		t.Error("a new environment password kept old sessions")
	}
	if err := store.CreateSession("s2", "csrf", time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := a.syncEnv(); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Session("s2"); err != nil {
		t.Errorf("an unchanged environment password cleared sessions: %v", err)
	}
	changed := &auth{store: store, envPassword: "another", now: time.Now}
	if err := changed.syncEnv(); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Session("s2"); !errors.Is(err, archive.ErrNotFound) {
		t.Error("a changed environment password kept old sessions")
	}
	removed := &auth{store: store, now: time.Now}
	if err := store.CreateSession("s3", "csrf", time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := removed.syncEnv(); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Session("s3"); !errors.Is(err, archive.ErrNotFound) {
		t.Error("removing the environment password kept old sessions")
	}
}

func TestThrottle(t *testing.T) {
	t.Parallel()
	a, _ := newAuth(t, "")
	now := time.Unix(1_700_000_000, 0)
	a.now = func() time.Time { return now }
	for range 4 {
		a.failed()
	}
	if w := a.wait(); w != 0 {
		t.Fatalf("locked after four failures: %v", w)
	}
	a.failed()
	if w := a.wait(); w != time.Second {
		t.Fatalf("wait after five failures = %v, want 1s", w)
	}
	a.failed()
	if w := a.wait(); w != 2*time.Second {
		t.Fatalf("wait after six failures = %v, want 2s", w)
	}
	now = now.Add(2 * time.Second)
	if w := a.wait(); w != 0 {
		t.Fatalf("still locked after the delay passed: %v", w)
	}
	for range 30 {
		a.failed()
	}
	if w := a.wait(); w != 5*time.Minute {
		t.Fatalf("wait after many failures = %v, want the 5m cap", w)
	}
	a.succeeded()
	if w := a.wait(); w != 0 {
		t.Fatalf("locked after a success: %v", w)
	}
}

func TestSessionCookie(t *testing.T) {
	t.Parallel()
	a, store := newAuth(t, "")
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/login", nil)
	req.Header.Set("X-Forwarded-Proto", "https")
	if err := a.startSession(rec, req); err != nil {
		t.Fatal(err)
	}
	cookies := rec.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("%d cookies set", len(cookies))
	}
	c := cookies[0]
	if c.Name != sessionCookie || !c.HttpOnly || !c.Secure || c.SameSite != http.SameSiteLaxMode || c.Path != "/" || c.MaxAge != 30*24*3600 {
		t.Errorf("cookie = %+v", c)
	}
	if _, err := store.Session(c.Value); err == nil {
		t.Error("the raw token is stored; only its hash may be")
	}

	next := httptest.NewRequest(http.MethodGet, "/", nil)
	next.AddCookie(c)
	sess, ok := a.session(next)
	if !ok || sess.CSRF == "" || sess.CSRF == c.Value {
		t.Fatalf("session = %+v, %v", sess, ok)
	}
	if _, ok := a.session(httptest.NewRequest(http.MethodGet, "/", nil)); ok {
		t.Error("a request without a cookie has a session")
	}
	forged := httptest.NewRequest(http.MethodGet, "/", nil)
	forged.AddCookie(&http.Cookie{Name: sessionCookie, Value: "made-up"})
	if _, ok := a.session(forged); ok {
		t.Error("a made-up token has a session")
	}

	out := httptest.NewRecorder()
	a.endSession(out, next)
	if _, ok := a.session(next); ok {
		t.Error("the session survived endSession")
	}
	if cleared := out.Result().Cookies(); len(cleared) != 1 || cleared[0].MaxAge >= 0 {
		t.Errorf("endSession did not clear the cookie: %+v", cleared)
	}

	plain := httptest.NewRecorder()
	if err := a.startSession(plain, httptest.NewRequest(http.MethodPost, "/login", nil)); err != nil {
		t.Fatal(err)
	}
	if plain.Result().Cookies()[0].Secure {
		t.Error("the cookie is Secure on a plain HTTP request, so it would never be sent back")
	}
}

// Two first-run forms sent at once cannot both choose the password.
func TestFirstPasswordIsSetOnce(t *testing.T) {
	t.Parallel()
	a, _ := newAuth(t, "")
	if err := a.setFirstPassword("first horse"); err != nil {
		t.Fatal(err)
	}
	if err := a.setFirstPassword("second horse"); !errors.Is(err, errPasswordExists) {
		t.Fatalf("a second first password = %v, want errPasswordExists", err)
	}
	if ok, _ := a.check("first horse"); !ok {
		t.Error("the first password was replaced")
	}
	if err := a.setFirstPassword("short"); !errors.Is(err, errShortPassword) {
		t.Errorf("a short first password = %v", err)
	}
}
