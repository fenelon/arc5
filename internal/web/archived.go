package web

import (
	"fmt"
	"net/http"
	"strconv"

	"github.com/fenelon/arc5/internal/archive"
	"github.com/fenelon/arc5/internal/model"
)

// ChatToggle is one chat with its archive switch.
type ChatToggle struct {
	ID       int64
	Title    string
	Detail   string
	Archived bool
}

// ChatGroup is the chats of one kind.
type ChatGroup struct {
	Title string
	Chats []ChatToggle
}

type archivedPage struct {
	Page
	AccountID int64
	Name      string
	Removed   bool
	Saved     bool
	Groups    []ChatGroup
}

var chatKindTitles = []struct {
	kind  model.ChatKind
	title string
}{
	{model.ChatPrivate, "Private chats"}, {model.ChatGroup, "Groups"}, {model.ChatChannel, "Channels"}, {model.ChatBot, "Bots"},
}

func (s *Server) archivedChats(w http.ResponseWriter, r *http.Request, sess archive.Session) {
	acc, ok := s.account(w, r)
	if !ok {
		return
	}
	all, err := s.sources()
	if err != nil {
		s.fail(w, err)
		return
	}
	chats, err := s.deps.Store.AccountChats(acc.ID)
	if err != nil {
		s.fail(w, err)
		return
	}
	data := archivedPage{
		Page: s.chrome(r, sess, "Chats of "+acc.Label, all, ""), AccountID: acc.ID, Name: acc.Label,
		Removed: !acc.RemovedAt.IsZero(), Saved: r.URL.Query().Get("saved") == "1",
	}
	for _, k := range chatKindTitles {
		group := ChatGroup{Title: k.title}
		for _, c := range chats {
			if c.Kind != k.kind {
				continue
			}
			detail := "Nothing archived yet"
			if c.Messages > 0 {
				detail = fmt.Sprintf("%s, the last on %s", plural(c.Messages, "message", "messages"), s.when(c.LastAt))
			}
			group.Chats = append(group.Chats, ChatToggle{ID: c.ID, Title: c.Title, Detail: detail, Archived: c.Archived})
		}
		if len(group.Chats) > 0 {
			data.Groups = append(data.Groups, group)
		}
	}
	s.render(w, http.StatusOK, "archived", data)
}

// saveArchivedChats switches archiving for the chats the form showed: ticked
// ones on, the others off. The connector restarts to pick up the change.
func (s *Server) saveArchivedChats(w http.ResponseWriter, r *http.Request, _ archive.Session) {
	acc, ok := s.live(w, r)
	if !ok {
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "The form could not be read.", http.StatusBadRequest)
		return
	}
	changes := map[int64]bool{}
	for _, name := range []string{"shown", "archived"} {
		for _, v := range r.PostForm[name] {
			id, err := strconv.ParseInt(v, 10, 64)
			if err != nil {
				http.Error(w, "The form named a chat that does not exist.", http.StatusBadRequest)
				return
			}
			changes[id] = name == "archived"
		}
	}
	if err := s.deps.Store.SetChatsArchived(acc.ID, changes); err != nil {
		s.fail(w, err)
		return
	}
	if err := s.deps.Runner.Restart(acc.ID); err != nil {
		s.fail(w, err)
		return
	}
	redirect(w, r, fmt.Sprintf("/sources/%d/chats?saved=1", acc.ID))
}
