package web

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"golang.org/x/crypto/argon2"

	"github.com/fenelon/arc5/internal/archive"
)

const (
	sessionCookie   = "arc5_session"
	sessionLifetime = 30 * 24 * time.Hour
	minPasswordLen  = 8

	passwordKey    = "password_hash"
	envPasswordKey = "env_password_hash"

	// argon2id at OWASP's minimum: 19 MiB, 2 passes, 1 thread. Small enough
	// for a modest server, slow enough to make guessing a stolen hash costly.
	argonMemory  = 19 * 1024
	argonPasses  = 2
	argonThreads = 1
	argonKeyLen  = 32

	throttleAfter = 5
	throttleMax   = 5 * time.Minute
)

var (
	errShortPassword = fmt.Errorf("the password needs at least %d characters", minPasswordLen)
	errEnvPassword   = errors.New("the password is set by ARC5_PASSWORD and cannot be changed here")
)

func hashPassword(password string) (string, error) {
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	key := argon2.IDKey([]byte(password), salt, argonPasses, argonMemory, argonThreads, argonKeyLen)
	return fmt.Sprintf("argon2id$%d$%d$%d$%s$%s", argonMemory, argonPasses, argonThreads,
		base64.RawStdEncoding.EncodeToString(salt), base64.RawStdEncoding.EncodeToString(key)), nil
}

func verifyPassword(encoded, password string) bool {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[0] != "argon2id" {
		return false
	}
	memory, err1 := strconv.ParseUint(parts[1], 10, 32)
	passes, err2 := strconv.ParseUint(parts[2], 10, 32)
	threads, err3 := strconv.ParseUint(parts[3], 10, 8)
	salt, err4 := base64.RawStdEncoding.DecodeString(parts[4])
	want, err5 := base64.RawStdEncoding.DecodeString(parts[5])
	if err1 != nil || err2 != nil || err3 != nil || err4 != nil || err5 != nil || len(want) == 0 || threads == 0 {
		return false
	}
	got := argon2.IDKey([]byte(password), salt, uint32(passes), uint32(memory), uint8(threads), uint32(len(want)))
	return subtle.ConstantTimeCompare(got, want) == 1
}

// auth owns the password, browser sessions and the login throttle.
type auth struct {
	store       *archive.Store
	envPassword string
	now         func() time.Time

	mu          sync.Mutex
	failures    int
	lockedUntil time.Time
}

func (a *auth) usesEnv() bool { return a.envPassword != "" }

func (a *auth) hasPassword() (bool, error) {
	if a.usesEnv() {
		return true, nil
	}
	hash, _, err := a.store.Setting(passwordKey)
	return hash != "", err
}

// setPassword stores a new password and signs every browser out.
func (a *auth) setPassword(password string) error {
	if a.usesEnv() {
		return errEnvPassword
	}
	if utf8.RuneCountInString(password) < minPasswordLen {
		return errShortPassword
	}
	hash, err := hashPassword(password)
	if err != nil {
		return err
	}
	if err := a.store.SetSetting(passwordKey, hash); err != nil {
		return err
	}
	return a.store.DeleteAllSessions()
}

func (a *auth) check(password string) (bool, error) {
	if a.usesEnv() {
		got, want := sha256.Sum256([]byte(password)), sha256.Sum256([]byte(a.envPassword))
		return subtle.ConstantTimeCompare(got[:], want[:]) == 1, nil
	}
	hash, _, err := a.store.Setting(passwordKey)
	if err != nil || hash == "" {
		return false, err
	}
	return verifyPassword(hash, password), nil
}

// syncEnv signs every browser out when ARC5_PASSWORD is not what it was at
// the last start: newly set, changed, or removed.
func (a *auth) syncEnv() error {
	seen, _, err := a.store.Setting(envPasswordKey)
	if err != nil {
		return err
	}
	if !a.usesEnv() && seen == "" {
		return nil
	}
	if a.usesEnv() && seen != "" && verifyPassword(seen, a.envPassword) {
		return nil
	}
	next := ""
	if a.usesEnv() {
		if next, err = hashPassword(a.envPassword); err != nil {
			return err
		}
	}
	if err := a.store.SetSetting(envPasswordKey, next); err != nil {
		return err
	}
	return a.store.DeleteAllSessions()
}

// wait is how long until another login attempt is allowed.
func (a *auth) wait() time.Duration {
	a.mu.Lock()
	defer a.mu.Unlock()
	return max(a.lockedUntil.Sub(a.now()), 0)
}

// failed records a wrong password. From the fifth on, each one doubles the
// wait before the next attempt, up to five minutes.
func (a *auth) failed() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.failures++
	if a.failures < throttleAfter {
		return
	}
	delay := throttleMax
	if shift := a.failures - throttleAfter; shift < 9 {
		delay = min(time.Second<<shift, throttleMax)
	}
	a.lockedUntil = a.now().Add(delay)
}

func (a *auth) succeeded() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.failures, a.lockedUntil = 0, time.Time{}
}

func randomToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

func tokenHash(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// isSecure reports whether the browser reached us over TLS, directly or
// through a proxy that says so.
func isSecure(r *http.Request) bool {
	return r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https")
}

func (a *auth) startSession(w http.ResponseWriter, r *http.Request) error {
	token, err := randomToken()
	if err != nil {
		return err
	}
	csrf, err := randomToken()
	if err != nil {
		return err
	}
	expires := a.now().Add(sessionLifetime)
	if err := a.store.CreateSession(tokenHash(token), csrf, expires); err != nil {
		return err
	}
	http.SetCookie(w, &http.Cookie{
		Name: sessionCookie, Value: token, Path: "/",
		Expires: expires, MaxAge: int(sessionLifetime / time.Second),
		HttpOnly: true, SameSite: http.SameSiteLaxMode, Secure: isSecure(r),
	})
	return nil
}

func (a *auth) session(r *http.Request) (archive.Session, bool) {
	c, err := r.Cookie(sessionCookie)
	if err != nil {
		return archive.Session{}, false
	}
	sess, err := a.store.Session(tokenHash(c.Value))
	return sess, err == nil
}

func (a *auth) endSession(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(sessionCookie); err == nil {
		a.store.DeleteSession(tokenHash(c.Value))
	}
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: "", Path: "/", MaxAge: -1, HttpOnly: true, SameSite: http.SameSiteLaxMode, Secure: isSecure(r)})
}
