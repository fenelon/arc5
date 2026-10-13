package web

import (
	"fmt"
	"net/http"
	"strconv"

	"github.com/fenelon/arc5/internal/archive"
	"github.com/fenelon/arc5/internal/connector"
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
	Mail      bool // the switches are a mailbox's folders
	Groups    []ChatGroup
}

// isMail reports whether an account's connector shows it as mail.
func (s *Server) isMail(acc archive.Account) bool {
	info, ok := s.deps.Registry.Info(acc.Connector)
	return ok && info.Display == connector.DisplayMail
}

// folderRoles names the folders of special use on the folder switches.
var folderRoles = map[model.FolderRole]string{
	model.FolderInbox: "Inbox", model.FolderSent: "Sent mail", model.FolderAll: "Every message, once",
	model.FolderDrafts: "Drafts", model.FolderJunk: "Junk", model.FolderTrash: "Trash",
	model.FolderFlagged: "Starred", model.FolderImportant: "Marked important",
}

// archivedFolders is the folder switches of a mailbox.
func (s *Server) archivedFolders(w http.ResponseWriter, r *http.Request, data archivedPage) {
	folders, err := s.deps.Store.AccountFolders(data.AccountID)
	if err != nil {
		s.fail(w, err)
		return
	}
	group := ChatGroup{Title: "Folders"}
	for _, f := range folders {
		detail := plural(f.Messages, "message", "messages")
		if f.Messages == 0 {
			detail = "Nothing archived yet"
		}
		if role, ok := folderRoles[f.Role]; ok && role != f.Name {
			detail = role + ", " + lowerFirst(detail)
		}
		group.Chats = append(group.Chats, ChatToggle{ID: f.ID, Title: f.Name, Detail: detail, Archived: f.Archived})
	}
	if len(group.Chats) > 0 {
		data.Groups = append(data.Groups, group)
	}
	data.Mail, data.Title = true, "Folders of "+data.Name
	s.render(w, http.StatusOK, "archived", data)
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
	if s.isMail(acc) {
		s.archivedFolders(w, r, data)
		return
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
	save := s.deps.Store.SetChatsArchived
	if s.isMail(acc) {
		save = s.deps.Store.SetFoldersArchived
	}
	if err := save(acc.ID, changes); err != nil {
		s.fail(w, err)
		return
	}
	if err := s.deps.Runner.Restart(acc.ID); err != nil {
		s.fail(w, err)
		return
	}
	redirect(w, r, fmt.Sprintf("/sources/%d/chats?saved=1", acc.ID))
}
