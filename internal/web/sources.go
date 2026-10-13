package web

import (
	"fmt"
	"net/http"
	"strconv"

	"github.com/fenelon/arc5/internal/archive"
	"github.com/fenelon/arc5/internal/connector"
)

const (
	stateLive       = "live"
	stateCatchingUp = "catching-up"
	stateNeedsLogin = "needs-login"
	stateOffline    = "offline"
)

// source is one account with what its connector and the runner say about it.
type source struct {
	Account    archive.Account
	Info       connector.Info
	Known      bool // its connector is built into this binary
	State      string
	StateLabel string
	Detail     string // StateLabel with the reason, for the Sources page
	Failing    bool   // enabled, and not archiving because something went wrong
}

func (s *Server) sources() ([]source, error) {
	accounts, err := s.deps.Store.Accounts()
	if err != nil {
		return nil, err
	}
	status := map[int64]connector.Status{}
	for _, st := range s.deps.Runner.Status() {
		status[st.AccountID] = st
	}
	out := make([]source, 0, len(accounts))
	for _, acc := range accounts {
		src := source{Account: acc}
		src.Info, src.Known = s.deps.Registry.Info(acc.Connector)
		if !src.Known {
			// An account whose connector is gone is still an archive to read.
			src.Info = connector.Info{Name: acc.Connector, Title: acc.Connector, Display: connector.DisplayChats}
		}
		st, running := status[acc.ID]
		describe(&src, st, running)
		out = append(out, src)
	}
	return out, nil
}

// describe turns the runner's status into a state mark and a sentence.
func describe(src *source, st connector.Status, running bool) {
	set := func(state, label string) { src.State, src.StateLabel, src.Detail = state, label, label }
	switch {
	case !src.Account.Enabled:
		set(stateOffline, "Switched off")
	case !running, st.State == connector.StateStopped:
		set(stateOffline, "Not running")
	case st.State == connector.StateNeedsLogin:
		set(stateNeedsLogin, "Needs login")
		src.Failing = true
	case st.State == connector.StatePaused:
		set(stateOffline, "Paused, the archive could not be written")
		src.Failing = true
	case st.State == connector.StateRunning && st.Progress.Phase == connector.PhaseLive:
		set(stateLive, "Live")
	case st.State == connector.StateRunning && st.Progress.Phase == connector.PhaseBackfill:
		set(stateCatchingUp, "Catching up on history")
		if st.Progress.ChatsTotal > 0 {
			src.Detail = fmt.Sprintf("Catching up on history, %d of %d chats", st.Progress.ChatsDone, st.Progress.ChatsTotal)
		}
	case st.State == connector.StateRunning && st.Progress.Phase == connector.PhaseRateLimited:
		set(stateCatchingUp, "Waiting, the source asked us to slow down")
	case st.State == connector.StateRunning:
		set(stateOffline, "Connecting")
	default:
		set(stateOffline, "Not connected")
		src.Failing = true
	}
	if st.LastError != "" && src.State != stateLive {
		src.Detail = src.StateLabel + ": " + st.LastError
	}
}

// logoFor is the logo drawn for a connector.
func logoFor(name string) string {
	switch name {
	case "telegram", "whatsapp":
		return name
	case "imap", "email":
		return "email"
	}
	return "generic"
}

func (src source) key() string { return strconv.FormatInt(src.Account.ID, 10) }

func (src source) tile(current string) Tile {
	return Tile{
		Name: src.Account.Label, Service: src.Info.Title, Logo: logoFor(src.Info.Name),
		State: src.State, StateLabel: src.StateLabel, Detail: src.Detail,
		Href: "/s/" + src.key(), Current: src.key() == current, Warn: src.Failing,
	}
}

// selection is what the picker has chosen: one source, or all of one kind.
type selection struct {
	Key        string // "chats", "mail", or an account ID
	Name       string
	Display    connector.Display
	AccountIDs []int64
}

var kinds = []struct {
	display connector.Display
	key     string
	title   string
	one     string
	many    string
}{
	{connector.DisplayChats, "chats", "Messengers", "messenger", "messengers"},
	{connector.DisplayMail, "mail", "Mail", "mailbox", "mailboxes"},
}

func (s *Server) selection(key string, all []source) (selection, bool) {
	for _, k := range kinds {
		if key != k.key {
			continue
		}
		sel := selection{Key: key, Name: "Everything at once", Display: k.display}
		for _, src := range all {
			if src.Info.Display == k.display {
				sel.AccountIDs = append(sel.AccountIDs, src.Account.ID)
			}
		}
		return sel, len(sel.AccountIDs) > 0
	}
	for _, src := range all {
		if src.key() == key {
			return selection{Key: key, Name: src.Account.Label, Display: src.Info.Display, AccountIDs: []int64{src.Account.ID}}, true
		}
	}
	return selection{}, false
}

// chrome is a Page with the picker and the outage alert filled in. current is
// the selection key the picker should mark, or "" for none.
func (s *Server) chrome(r *http.Request, sess archive.Session, title string, all []source, current string) Page {
	p := s.page(r, sess, title)
	if len(all) == 0 {
		return p
	}
	if current == "" {
		current = s.remembered(r, all)
	}
	picker := &Picker{}
	for _, k := range kinds {
		var tiles []Tile
		for _, src := range all {
			if src.Info.Display == k.display {
				tiles = append(tiles, src.tile(current))
			}
		}
		if len(tiles) == 0 {
			continue
		}
		count := fmt.Sprintf("%d %s", len(tiles), k.many)
		if len(tiles) == 1 {
			count = "1 " + k.one
		}
		everything := Tile{Name: "Everything at once", Service: count, Logo: "all", Href: "/s/" + k.key, Current: current == k.key}
		picker.Groups = append(picker.Groups, PickerGroup{Title: k.title, Tiles: append([]Tile{everything}, tiles...)})
		for _, t := range append([]Tile{everything}, tiles...) {
			if t.Current {
				picker.Current = t
			}
		}
	}
	if picker.Current.Name == "" {
		picker.Current = Tile{Name: "Sources", Service: "Choose one", Logo: "all"}
	}
	p.Picker = picker
	for _, src := range all {
		if src.Failing {
			p.Alert = &Alert{Name: src.Account.Label, Detail: src.Detail + ". New messages are not being saved, and ones that auto-delete cannot be recovered."}
			break
		}
	}
	return p
}

// sourcesPage lists every source with its state.
type sourcesPage struct {
	Page
	Groups []PickerGroup
}

func (s *Server) sourcesList(w http.ResponseWriter, r *http.Request, sess archive.Session) {
	all, err := s.sources()
	if err != nil {
		s.fail(w, err)
		return
	}
	data := sourcesPage{Page: s.chrome(r, sess, "Sources", all, "")}
	for _, k := range kinds {
		group := PickerGroup{Title: k.title}
		for _, src := range all {
			if src.Info.Display == k.display {
				group.Tiles = append(group.Tiles, src.tile(""))
			}
		}
		if len(group.Tiles) > 0 {
			data.Groups = append(data.Groups, group)
		}
	}
	s.render(w, http.StatusOK, "sources", data)
}
