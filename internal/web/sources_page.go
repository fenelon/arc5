package web

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/fenelon/arc5/internal/archive"
	"github.com/fenelon/arc5/internal/connector"
)

// maxFieldRunes caps a text setting.
const maxFieldRunes = 1000

// FieldView is one of a connector's settings, as a form shows it.
type FieldView struct {
	ID, Name, Label, Help, Kind, Value string
	Checked                            bool
	Kept                               bool // a secret is stored, and an empty field keeps it
	Choices                            []Choice
}

type Choice struct {
	Value    string
	Selected bool
}

// ProgressView is how far a source has caught up on its history.
type ProgressView struct {
	Done, Total int
}

// SourcePanel is the chosen source's settings on the Sources page.
type SourcePanel struct {
	ID                  int64
	Name, Service, Logo string
	OpenHref, ChatsHref string
	Chats               int
	Removed             string // the day it was removed, or ""
	Status              string
	Progress            *ProgressView
	Fields              []FieldView
	CanSignOut          bool
	LoginHref           string // "Log in again", when the source needs it
	LoginLabel          string
}

type sourcesPage struct {
	Page
	Groups  []PickerGroup
	Panel   *SourcePanel
	Saved   string
	Problem string
}

// lowerFirst makes a label fit after a comma: "Needs login" → "needs login".
func lowerFirst(s string) string {
	r, size := utf8.DecodeRuneInString(s)
	return string(unicode.ToLower(r)) + s[size:]
}

// ago says how long ago t was, for "last message 2 minutes ago".
func (s *Server) ago(t time.Time) string {
	d := s.deps.Now().Sub(t)
	switch {
	case d < time.Minute:
		return "less than a minute ago"
	case d < time.Hour:
		return plural(int(d/time.Minute), "minute", "minutes") + " ago"
	case d < 24*time.Hour:
		return plural(int(d/time.Hour), "hour", "hours") + " ago"
	}
	return "on " + s.when(t)
}

// newConnector builds a connector to read its settings or sign it out. A
// connector's factory is not this package's code; a panic in it must not
// break the page.
func (s *Server) newConnector(name string) (c connector.Connector, ok bool) {
	defer func() {
		if recover() != nil {
			c, ok = nil, false
		}
	}()
	c, ok = s.deps.Registry.New(name)
	return c, ok && c != nil
}

// sourceLine is a source's line on its tile: service and what it is doing.
func (s *Server) sourceLine(src source, last map[int64]time.Time) string {
	detail := src.Detail
	if src.State == stateLive {
		if t, ok := last[src.Account.ID]; ok {
			detail = "last message " + s.ago(t)
		}
	}
	return src.Info.Title + ", " + lowerFirst(detail)
}

func (s *Server) sourcesData(r *http.Request, sess archive.Session, chosen string) (sourcesPage, error) {
	all, err := s.sources()
	if err != nil {
		return sourcesPage{}, err
	}
	data := sourcesPage{Page: s.chrome(r, sess, "Sources", all, ""), Saved: r.URL.Query().Get("saved")}
	if len(all) == 0 {
		return data, nil
	}
	last, err := s.deps.Store.LastMessageTimes()
	if err != nil {
		return sourcesPage{}, err
	}
	var picked *source
	if chosen == "" {
		chosen = s.remembered(r, all)
	}
	for i := range all {
		if all[i].key() == chosen {
			picked = &all[i]
		}
	}
	if picked == nil {
		picked = &all[0]
		for i := range all {
			if all[i].Account.RemovedAt.IsZero() {
				picked = &all[i]
				break
			}
		}
	}
	tile := func(src source) Tile {
		t := src.tile("")
		t.Href, t.Current, t.Detail = "/sources/"+src.key(), src.key() == picked.key(), s.sourceLine(src, last)
		return t
	}
	for _, k := range kinds {
		group := PickerGroup{Title: k.sourcesTitle}
		for _, src := range all {
			if src.Info.Display == k.display && src.Account.RemovedAt.IsZero() {
				group.Tiles = append(group.Tiles, tile(src))
			}
		}
		if len(group.Tiles) > 0 {
			data.Groups = append(data.Groups, group)
		}
	}
	removed := PickerGroup{Title: "Removed, archive kept"}
	for _, src := range all {
		if !src.Account.RemovedAt.IsZero() {
			removed.Tiles = append(removed.Tiles, tile(src))
		}
	}
	if len(removed.Tiles) > 0 {
		data.Groups = append(data.Groups, removed)
	}
	data.Panel, err = s.panel(*picked)
	return data, err
}

func (s *Server) panel(src source) (*SourcePanel, error) {
	acc := src.Account
	p := &SourcePanel{
		ID: acc.ID, Name: acc.Label, Service: src.Info.Title, Logo: logoFor(src.Info.Name),
		OpenHref: "/s/" + src.key(), ChatsHref: fmt.Sprintf("/sources/%d/chats", acc.ID), Status: src.Detail,
	}
	chats, err := s.deps.Store.AccountChats(acc.ID)
	if err != nil {
		return nil, err
	}
	p.Chats = len(chats)
	if !acc.RemovedAt.IsZero() {
		p.Removed = acc.RemovedAt.In(s.deps.Location).Format("2 January 2006")
		return p, nil
	}
	for _, st := range s.deps.Runner.Status() {
		if st.AccountID == acc.ID && st.Progress.ChatsTotal > 0 && st.Progress.Phase == connector.PhaseBackfill {
			p.Progress = &ProgressView{Done: st.Progress.ChatsDone, Total: st.Progress.ChatsTotal}
		}
	}
	c, ok := s.newConnector(acc.Connector)
	if !ok {
		return p, nil
	}
	switch {
	case src.State == stateNeedsLogin:
		p.LoginHref, p.LoginLabel = fmt.Sprintf("/sources/%d/login", acc.ID), "Log in again"
	case !acc.Enabled:
		p.LoginHref, p.LoginLabel = fmt.Sprintf("/sources/%d/login", acc.ID), "Log in"
	}
	_, p.CanSignOut = c.(connector.SignOuter)
	p.CanSignOut = p.CanSignOut && acc.Enabled && src.State != stateNeedsLogin
	for _, f := range c.Settings() {
		value, set := acc.Settings[f.Name]
		if !set {
			value = f.Default
		}
		v := FieldView{ID: "field-" + f.Name, Name: f.Name, Label: f.Label, Help: f.Help, Kind: string(f.Kind), Value: value}
		switch f.Kind {
		case connector.FieldSecret:
			v.Value, v.Kept = "", value != "" // never sent back to the browser
		case connector.FieldBool:
			v.Checked = value == "true"
		case connector.FieldChoice:
			for _, choice := range f.Choices {
				v.Choices = append(v.Choices, Choice{Value: choice, Selected: choice == value})
			}
		}
		p.Fields = append(p.Fields, v)
	}
	return p, nil
}

func (s *Server) sourcesList(w http.ResponseWriter, r *http.Request, sess archive.Session) {
	data, err := s.sourcesData(r, sess, "")
	if err != nil {
		s.fail(w, err)
		return
	}
	s.render(w, http.StatusOK, "sources", data)
}

// account reads the {id} of a /sources/{id} address. It answers the request
// itself when there is no such account.
func (s *Server) account(w http.ResponseWriter, r *http.Request) (archive.Account, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return archive.Account{}, false
	}
	acc, err := s.deps.Store.Account(id)
	if errors.Is(err, archive.ErrNotFound) {
		http.NotFound(w, r)
		return archive.Account{}, false
	}
	if err != nil {
		s.fail(w, err)
		return archive.Account{}, false
	}
	return acc, true
}

// live is account for actions that make no sense on a removed source.
func (s *Server) live(w http.ResponseWriter, r *http.Request) (archive.Account, bool) {
	acc, ok := s.account(w, r)
	if ok && !acc.RemovedAt.IsZero() {
		http.Error(w, "This source was removed. Its archive is kept, but it has no settings any more.", http.StatusConflict)
		return archive.Account{}, false
	}
	return acc, ok
}

func (s *Server) sourceSettings(w http.ResponseWriter, r *http.Request, sess archive.Session) {
	acc, ok := s.account(w, r)
	if !ok {
		return
	}
	data, err := s.sourcesData(r, sess, strconv.FormatInt(acc.ID, 10))
	if err != nil {
		s.fail(w, err)
		return
	}
	s.render(w, http.StatusOK, "sources", data)
}

// refuseSource shows a source's panel again with a problem.
func (s *Server) refuseSource(w http.ResponseWriter, r *http.Request, sess archive.Session, acc archive.Account, problem string) {
	data, err := s.sourcesData(r, sess, strconv.FormatInt(acc.ID, 10))
	if err != nil {
		s.fail(w, err)
		return
	}
	data.Saved, data.Problem = "", problem
	s.render(w, http.StatusUnprocessableEntity, "sources", data)
}

func (s *Server) renameSource(w http.ResponseWriter, r *http.Request, sess archive.Session) {
	acc, ok := s.account(w, r)
	if !ok {
		return
	}
	switch err := s.deps.Store.RenameAccount(acc.ID, r.PostFormValue("name")); {
	case errors.Is(err, archive.ErrInvalid):
		s.refuseSource(w, r, sess, acc, "Give the source a name.")
	case err != nil:
		s.fail(w, err)
	default:
		redirect(w, r, fmt.Sprintf("/sources/%d?saved=name", acc.ID))
	}
}

// readSettings checks a settings form against the connector's fields and
// returns the account's settings with the form's values in place.
func readSettings(fields []connector.Field, current map[string]string, r *http.Request) (map[string]string, error) {
	next := make(map[string]string, len(current)+len(fields))
	for k, v := range current {
		next[k] = v
	}
	for _, f := range fields {
		value := strings.TrimSpace(r.PostFormValue(f.Name))
		switch f.Kind {
		case connector.FieldBool:
			value = map[bool]string{true: "true", false: ""}[value == "true"]
		case connector.FieldSecret:
			if value == "" {
				continue // left empty: keep the one stored
			}
		case connector.FieldChoice:
			if !containsString(f.Choices, value) {
				return nil, fmt.Errorf("Choose one of the offered values for %s.", f.Label)
			}
		case connector.FieldNumber:
			if _, err := strconv.ParseInt(value, 10, 64); err != nil && value != "" {
				return nil, fmt.Errorf("%s needs a whole number.", f.Label)
			}
		}
		if utf8.RuneCountInString(value) > maxFieldRunes {
			return nil, fmt.Errorf("%s is too long.", f.Label)
		}
		if value == "" {
			delete(next, f.Name)
		} else {
			next[f.Name] = value
		}
	}
	return next, nil
}

func (s *Server) saveSourceSettings(w http.ResponseWriter, r *http.Request, sess archive.Session) {
	acc, ok := s.live(w, r)
	if !ok {
		return
	}
	c, known := s.newConnector(acc.Connector)
	if !known {
		http.NotFound(w, r)
		return
	}
	next, err := readSettings(c.Settings(), acc.Settings, r)
	if err != nil {
		s.refuseSource(w, r, sess, acc, err.Error())
		return
	}
	if err := s.deps.Store.UpdateAccountSettings(acc.ID, next); err != nil {
		s.fail(w, err)
		return
	}
	if err := s.deps.Runner.Restart(acc.ID); err != nil {
		s.fail(w, err)
		return
	}
	redirect(w, r, fmt.Sprintf("/sources/%d?saved=settings", acc.ID))
}

func (s *Server) signOutSource(w http.ResponseWriter, r *http.Request, _ archive.Session) {
	acc, unlock, ok := s.liveLocked(w, r)
	if !ok {
		return
	}
	defer unlock()
	c, known := s.newConnector(acc.Connector)
	if _, can := c.(connector.SignOuter); !known || !can {
		http.NotFound(w, r)
		return
	}
	switch err := s.deps.Runner.SignOut(r.Context(), acc.ID); {
	case errors.Is(err, connector.ErrCannotSignOut):
		http.NotFound(w, r)
	case err != nil:
		s.fail(w, err)
	default:
		redirect(w, r, fmt.Sprintf("/sources/%d?saved=signout", acc.ID))
	}
}

// removeSource stops a source for good and forgets its login. Its messages
// stay in the archive.
func (s *Server) removeSource(w http.ResponseWriter, r *http.Request, _ archive.Session) {
	acc, unlock, ok := s.liveLocked(w, r)
	if !ok {
		return
	}
	defer unlock()
	// End a login in progress: a step in flight has finished by now, as it
	// holds the same lock, and none can start until the removal is done.
	s.flows.drop(acc.ID)
	if err := s.deps.Store.SetAccountEnabled(acc.ID, false); err != nil {
		s.fail(w, err)
		return
	}
	// Stop the connector before its state is deleted, so it cannot write it back.
	if err := s.deps.Runner.Restart(acc.ID); err != nil {
		s.fail(w, err)
		return
	}
	if err := s.deps.Store.RemoveAccount(acc.ID); err != nil {
		s.fail(w, err)
		return
	}
	redirect(w, r, "/sources?saved=removed")
}
