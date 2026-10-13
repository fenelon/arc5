// Package web serves arc5's interface.
package web

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"embed"
	"encoding/hex"
	"errors"
	"html/template"
	"io"
	"io/fs"
	"log"
	"net/http"
	"time"

	"github.com/fenelon/arc5/internal/archive"
	"github.com/fenelon/arc5/internal/connector"
	"github.com/fenelon/arc5/internal/indexer"
	"github.com/fenelon/arc5/internal/search"
)

//go:embed static templates
var files embed.FS

// csp lets a page load scripts, styles, fonts and media from arc5 only, and
// nothing inline. Message text can never run as code.
const csp = "default-src 'none'; script-src 'self'; style-src 'self'; img-src 'self'; media-src 'self'; font-src 'self'; connect-src 'self'; form-action 'self'; base-uri 'none'; frame-ancestors 'none'"

// Index is what the interface needs from the search index: a searcher, the
// state of the index, and a way to change its languages.
type Index interface {
	Searcher() *search.Searcher
	Status() indexer.Status
	SetLanguages(codes []string) error
}

// Runner is what the interface needs from the connector runner.
type Runner interface {
	Status() []connector.Status
	RequestMedia(attachmentID int64) error
	// Restart stops an account's connector and starts it again if the
	// account is enabled, after its settings change.
	Restart(accountID int64) error
	// SignOut has an account's connector end its session at the source.
	SignOut(ctx context.Context, accountID int64) error
}

type Deps struct {
	Store       *archive.Store
	Runner      Runner
	Registry    *connector.Registry
	Index       Index
	EnvPassword string           // ARC5_PASSWORD; empty lets the owner choose one
	Now         func() time.Time // defaults to time.Now
	Location    *time.Location   // times are shown in this zone; defaults to time.Local
}

type Server struct {
	deps    Deps
	auth    *auth
	tmpl    *template.Template
	static  fs.FS
	version string
	mux     *http.ServeMux
	flows   flows        // logins in progress on the Sources page
	logins  accountLocks // one login, sign-out or removal of a source at a time
}

func New(deps Deps) (*Server, error) {
	if deps.Store == nil || deps.Runner == nil || deps.Registry == nil || deps.Index == nil {
		return nil, errors.New("web: Deps needs a store, a runner, a registry and an index")
	}
	if deps.Now == nil {
		deps.Now = time.Now
	}
	if deps.Location == nil {
		deps.Location = time.Local
	}
	s := &Server{deps: deps, auth: &auth{store: deps.Store, envPassword: deps.EnvPassword, now: deps.Now}}
	if err := s.auth.syncEnv(); err != nil {
		return nil, err
	}
	var err error
	if s.static, err = fs.Sub(files, "static"); err != nil {
		return nil, err
	}
	if s.version, err = fingerprint(s.static); err != nil {
		return nil, err
	}
	s.tmpl, err = template.New("").Funcs(template.FuncMap{"asset": s.asset, "dict": dict}).ParseFS(files, "templates/*.html")
	if err != nil {
		return nil, err
	}
	s.routes()
	return s, nil
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) { s.mux.ServeHTTP(w, r) }

func (s *Server) routes() {
	s.mux = http.NewServeMux()
	s.mux.HandleFunc("GET /healthz", s.health)
	s.mux.HandleFunc("GET /static/{version}/{path...}", s.serveStatic)
	s.mux.HandleFunc("GET /setup", s.setupForm)
	s.mux.HandleFunc("POST /setup", s.setup)
	s.mux.HandleFunc("GET /login", s.loginForm)
	s.mux.HandleFunc("POST /login", s.login)
	s.mux.Handle("POST /logout", s.signedIn(s.logout))
	s.mux.Handle("GET /{$}", s.signedIn(s.home))
	s.mux.Handle("GET /s/{source}", s.signedIn(s.sourceView))
	s.mux.Handle("GET /s/{source}/c/{chat}", s.signedIn(s.conversationView))
	s.mux.Handle("GET /s/{source}/results", s.signedIn(s.resultsFragment))
	s.mux.Handle("GET /sources", s.signedIn(s.sourcesList))
	s.mux.Handle("GET /sources/new", s.signedIn(s.newSourceForm))
	s.mux.Handle("POST /sources/new", s.signedIn(s.addSource))
	s.mux.Handle("GET /sources/{id}", s.signedIn(s.sourceSettings))
	s.mux.Handle("GET /sources/{id}/login", s.signedIn(s.loginStep))
	s.mux.Handle("POST /sources/{id}/login", s.signedIn(s.submitStep))
	s.mux.Handle("POST /sources/{id}/login/cancel", s.signedIn(s.cancelLogin))
	s.mux.Handle("POST /sources/{id}/name", s.signedIn(s.renameSource))
	s.mux.Handle("POST /sources/{id}/settings", s.signedIn(s.saveSourceSettings))
	s.mux.Handle("POST /sources/{id}/signout", s.signedIn(s.signOutSource))
	s.mux.Handle("POST /sources/{id}/remove", s.signedIn(s.removeSource))
	s.mux.Handle("GET /sources/{id}/chats", s.signedIn(s.archivedChats))
	s.mux.Handle("POST /sources/{id}/chats", s.signedIn(s.saveArchivedChats))
	s.mux.Handle("GET /settings", s.signedIn(s.settings))
	s.mux.Handle("POST /settings/appearance", s.signedIn(s.saveAppearance))
	s.mux.Handle("POST /settings/languages", s.signedIn(s.saveLanguages))
	s.mux.Handle("POST /settings/attachments", s.signedIn(s.saveAttachments))
	s.mux.Handle("POST /settings/password", s.signedIn(s.savePassword))
	s.mux.Handle("GET /media/{id}", s.signedIn(s.serveMedia))
	s.mux.Handle("POST /media/{id}/fetch", s.signedIn(s.fetchMedia))
}

// health is unauthenticated and deliberately says nothing about accounts.
func (s *Server) health(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	io.WriteString(w, `{"status":"ok"}`+"\n")
}

// fingerprint hashes every static file, so asset addresses change whenever
// their content does and browsers can cache them for good.
func fingerprint(static fs.FS) (string, error) {
	h := sha256.New()
	err := fs.WalkDir(static, ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		body, err := fs.ReadFile(static, path)
		if err != nil {
			return err
		}
		io.WriteString(h, path)
		h.Write(body)
		return nil
	})
	return hex.EncodeToString(h.Sum(nil))[:12], err
}

// asset is the address of a static file.
func (s *Server) asset(name string) string { return "/static/" + s.version + "/" + name }

func (s *Server) serveStatic(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("path")
	info, err := fs.Stat(s.static, name)
	if !fs.ValidPath(name) || err != nil || info.IsDir() {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	http.ServeFileFS(w, r, s.static, name)
}

// dict builds a map in a template, to hand a partial more than one value.
func dict(pairs ...any) (map[string]any, error) {
	if len(pairs)%2 != 0 {
		return nil, errors.New("dict needs pairs of key and value")
	}
	m := make(map[string]any, len(pairs)/2)
	for i := 0; i < len(pairs); i += 2 {
		key, ok := pairs[i].(string)
		if !ok {
			return nil, errors.New("dict keys must be strings")
		}
		m[key] = pairs[i+1]
	}
	return m, nil
}

// render builds the whole page before sending any of it, so a template error
// becomes a clean 500 and never half a page.
func (s *Server) render(w http.ResponseWriter, status int, name string, data any) {
	var buf bytes.Buffer
	if err := s.tmpl.ExecuteTemplate(&buf, name, data); err != nil {
		log.Printf("web: render %s: %v", name, err)
		http.Error(w, "arc5 could not build this page. The reason is in the server log.", http.StatusInternalServerError)
		return
	}
	h := w.Header()
	h.Set("Content-Type", "text/html; charset=utf-8")
	h.Set("Content-Security-Policy", csp)
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Referrer-Policy", "same-origin")
	h.Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	buf.WriteTo(w)
}

func (s *Server) fail(w http.ResponseWriter, err error) {
	log.Printf("web: %v", err)
	http.Error(w, "arc5 could not finish this request. The reason is in the server log.", http.StatusInternalServerError)
}

// theme is the theme the owner chose, from its cookie; empty follows the device.
func theme(r *http.Request) string {
	if c, err := r.Cookie("arc5_theme"); err == nil && (c.Value == "light" || c.Value == "dark") {
		return c.Value
	}
	return ""
}

// page starts the data every signed-in page needs.
func (s *Server) page(r *http.Request, sess archive.Session, title string) Page {
	return Page{Title: title, Theme: theme(r), CSRF: sess.CSRF}
}

func redirect(w http.ResponseWriter, r *http.Request, to string) {
	http.Redirect(w, r, to, http.StatusSeeOther)
}

// signedIn guards a handler: no password yet sends the browser to first-run
// setup, no session to the login page, and a POST without the session's CSRF
// token is refused.
func (s *Server) signedIn(next func(http.ResponseWriter, *http.Request, archive.Session)) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		has, err := s.auth.hasPassword()
		if err != nil {
			s.fail(w, err)
			return
		}
		if !has {
			redirect(w, r, "/setup")
			return
		}
		sess, ok := s.auth.session(r)
		if !ok {
			redirect(w, r, "/login")
			return
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			got := r.PostFormValue("csrf")
			if got == "" || subtle.ConstantTimeCompare([]byte(got), []byte(sess.CSRF)) != 1 {
				http.Error(w, "This form has expired. Go back, reload the page and try again.", http.StatusForbidden)
				return
			}
		}
		next(w, r, sess)
	})
}

func (s *Server) home(w http.ResponseWriter, r *http.Request, sess archive.Session) {
	accounts, err := s.deps.Store.Accounts()
	if err != nil {
		s.fail(w, err)
		return
	}
	if len(accounts) == 0 {
		s.render(w, http.StatusOK, "empty", s.page(r, sess, "Welcome"))
		return
	}
	all, err := s.sources()
	if err != nil {
		s.fail(w, err)
		return
	}
	if key := s.remembered(r, all); key != "" {
		redirect(w, r, "/s/"+key)
		return
	}
	redirect(w, r, "/s/chats")
}

// sourceCookie remembers the last source the owner opened, so home, Sources
// and Settings come back to it.
const sourceCookie = "arc5_source"

func remember(w http.ResponseWriter, r *http.Request, key string) {
	http.SetCookie(w, &http.Cookie{Name: sourceCookie, Value: key, Path: "/", MaxAge: 365 * 24 * 3600,
		HttpOnly: true, SameSite: http.SameSiteLaxMode, Secure: isSecure(r)})
}

// remembered is the last source opened, if it still exists.
func (s *Server) remembered(r *http.Request, all []source) string {
	c, err := r.Cookie(sourceCookie)
	if err != nil {
		return ""
	}
	if _, ok := s.selection(c.Value, all); !ok {
		return ""
	}
	return c.Value
}
