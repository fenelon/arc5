package web

import (
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"

	"github.com/fenelon/arc5/internal/archive"
	"github.com/fenelon/arc5/internal/indexer"
	"github.com/fenelon/arc5/internal/search"
)

// maxLimitMB is the largest attachment limit the form accepts, about 100 GB.
const maxLimitMB = 100000

// LanguageBox is one language's checkbox.
type LanguageBox struct {
	Code, Name string
	Checked    bool
}

type settingsPage struct {
	Page
	Saved       string // which card to confirm, from the "saved" parameter
	Problem     string // why the last form was refused
	ProblemCard string // which card the problem belongs to
	Languages   []LanguageBox
	IndexNote   string
	LimitMB     string
	EnvPassword bool
}

// languageList joins language names for a sentence: "English, Russian and Spanish".
func languageList(codes []string) string {
	names := make([]string, 0, len(codes))
	for _, code := range codes {
		for _, l := range search.Languages() {
			if l.Code == code {
				names = append(names, l.Name)
			}
		}
	}
	switch len(names) {
	case 0:
		return ""
	case 1:
		return names[0]
	}
	return strings.Join(names[:len(names)-1], ", ") + " and " + names[len(names)-1]
}

// indexNote says what the search index is doing.
func (s *Server) indexNote() string {
	st := s.deps.Index.Status()
	langs := languageList(st.Languages)
	switch {
	case st.Rebuilding && st.Total > 0:
		return fmt.Sprintf("Rebuilding the index: %d of %d messages. Search keeps working and finds more as it goes.", st.Done, st.Total)
	case st.Rebuilding:
		return "Rebuilding the index. Search keeps working and finds more as it goes."
	case st.Err != "":
		return "The last rebuild failed: " + st.Err + ". Save again to retry."
	case langs == "":
		return "No languages are ticked, so search finds exact words only."
	}
	return "The index is up to date for " + langs + "."
}

func (s *Server) settingsData(r *http.Request, sess archive.Session) (settingsPage, error) {
	all, err := s.sources()
	if err != nil {
		return settingsPage{}, err
	}
	data := settingsPage{Page: s.chrome(r, sess, "Settings", all, ""), Saved: r.URL.Query().Get("saved"), EnvPassword: s.auth.usesEnv()}
	chosen := s.deps.Index.Status().Languages
	for _, l := range search.Languages() {
		data.Languages = append(data.Languages, LanguageBox{Code: l.Code, Name: l.Name, Checked: containsString(chosen, l.Code)})
	}
	sort.Slice(data.Languages, func(i, j int) bool { return data.Languages[i].Name < data.Languages[j].Name })
	data.IndexNote = s.indexNote()
	limit, err := s.deps.Store.MediaCap()
	if err != nil {
		return settingsPage{}, err
	}
	data.LimitMB = strconv.FormatInt((limit+(1<<20)-1)>>20, 10)
	return data, nil
}

func containsString(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

func (s *Server) settings(w http.ResponseWriter, r *http.Request, sess archive.Session) {
	data, err := s.settingsData(r, sess)
	if err != nil {
		s.fail(w, err)
		return
	}
	s.render(w, http.StatusOK, "settings", data)
}

// refuse shows the settings page again with a problem in one card.
func (s *Server) refuse(w http.ResponseWriter, r *http.Request, sess archive.Session, card, problem string) {
	data, err := s.settingsData(r, sess)
	if err != nil {
		s.fail(w, err)
		return
	}
	data.Saved, data.Problem, data.ProblemCard = "", problem, card
	s.render(w, http.StatusUnprocessableEntity, "settings", data)
}

func (s *Server) saveAppearance(w http.ResponseWriter, r *http.Request, _ archive.Session) {
	c := &http.Cookie{Name: "arc5_theme", Path: "/", MaxAge: 365 * 24 * 3600, SameSite: http.SameSiteLaxMode, Secure: isSecure(r)}
	switch choice := r.PostFormValue("theme"); choice {
	case "light", "dark":
		c.Value = choice
	default:
		c.MaxAge = -1 // follow the device
	}
	http.SetCookie(w, c)
	redirect(w, r, "/settings?saved=appearance")
}

func (s *Server) saveLanguages(w http.ResponseWriter, r *http.Request, sess archive.Session) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "The form could not be read.", http.StatusBadRequest)
		return
	}
	err := s.deps.Index.SetLanguages(r.PostForm["lang"])
	switch {
	case errors.Is(err, indexer.ErrUnknownLanguage):
		s.refuse(w, r, sess, "languages", "One of those languages is not available. Tick only languages from the list.")
	case err != nil:
		s.fail(w, err)
	default:
		redirect(w, r, "/settings?saved=languages#languages")
	}
}

func (s *Server) saveAttachments(w http.ResponseWriter, r *http.Request, sess archive.Session) {
	mb, err := strconv.ParseInt(strings.TrimSpace(r.PostFormValue("limit_mb")), 10, 64)
	if err != nil || mb < 0 || mb > maxLimitMB {
		s.refuse(w, r, sess, "attachments", fmt.Sprintf("Type a whole number of megabytes from 0 to %d.", maxLimitMB))
		return
	}
	if err := s.deps.Store.SetMediaCap(mb << 20); err != nil {
		s.fail(w, err)
		return
	}
	redirect(w, r, "/settings?saved=attachments#attachments")
}

func (s *Server) savePassword(w http.ResponseWriter, r *http.Request, sess archive.Session) {
	if s.auth.usesEnv() {
		s.refuse(w, r, sess, "password", "The password comes from ARC5_PASSWORD and can only be changed there.")
		return
	}
	ok, wait, err := s.auth.login(r.Context(), r.PostFormValue("current"))
	switch {
	case errors.Is(err, errLoginBusy) || wait > 0:
		s.refuse(w, r, sess, "password", "Too many wrong passwords. Wait a little and try again.")
		return
	case err != nil:
		s.fail(w, err)
		return
	case !ok:
		s.refuse(w, r, sess, "password", "The current password is not right.")
		return
	}
	switch err := s.auth.setPassword(r.PostFormValue("new")); {
	case errors.Is(err, errShortPassword):
		s.refuse(w, r, sess, "password", fmt.Sprintf("The new password is too short. Use at least %d characters.", minPasswordLen))
		return
	case err != nil:
		s.fail(w, err)
		return
	}
	// Changing the password signed every browser out; sign this one back in.
	if err := s.auth.startSession(w, r); err != nil {
		s.fail(w, err)
		return
	}
	redirect(w, r, "/settings?saved=password#password")
}
