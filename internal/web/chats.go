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
	"github.com/fenelon/arc5/internal/model"
)

const (
	chatsPerPage = 100
	maxChatRows  = 5000
	previewRunes = 120
)

// ChatRow is one chat in the list.
type ChatRow struct {
	Href     string
	Title    string
	Initials string
	Avatar   string // the chat's picture, or "" for initials
	Preview  string
	When     string
	Current  bool
}

type sourcePage struct {
	Page
	Chats         []ChatRow
	MoreChatsHref string
	Results       *Results // replaces the chat list while there is a search
	Conversation  *Conversation
}

// initials are up to two letters that stand for a name in its avatar.
func initials(name string) string {
	var out []rune
	for _, word := range strings.Fields(name) {
		r, _ := utf8.DecodeRuneInString(word)
		if unicode.IsLetter(r) || unicode.IsNumber(r) {
			out = append(out, unicode.ToUpper(r))
		}
		if len(out) == 2 {
			break
		}
	}
	if len(out) == 0 {
		return "?"
	}
	return string(out)
}

// preview is one line that stands for a chat's last message.
func preview(c archive.ChatSummary) string {
	text := strings.Join(strings.Fields(c.LastText), " ")
	if text == "" {
		switch {
		case c.LastFiles == 1:
			text = "1 attachment"
		case c.LastFiles > 1:
			text = fmt.Sprintf("%d attachments", c.LastFiles)
		}
	}
	if utf8.RuneCountInString(text) > previewRunes {
		text = string([]rune(text)[:previewRunes]) + "…"
	}
	switch {
	case c.LastKind == model.KindService:
		return text
	case c.LastOutgoing:
		return "You: " + text
	case c.Kind != model.ChatPrivate && c.LastSender != "":
		return c.LastSender + ": " + text
	}
	return text
}

// when shows a moment briefly: the time today, the day this year, and the
// year as well before that.
func (s *Server) when(t time.Time) string {
	t, now := t.In(s.deps.Location), s.deps.Now().In(s.deps.Location)
	switch {
	case t.Year() == now.Year() && t.YearDay() == now.YearDay():
		return t.Format("15:04")
	case t.Year() == now.Year():
		return t.Format("2 Jan")
	}
	return t.Format("2 Jan 2006")
}

// chatRows reads the first count chats of a selection.
func (s *Server) chatRows(sel selection, currentChat int64, count int) ([]ChatRow, bool, error) {
	list, err := s.deps.Store.ChatList(sel.AccountIDs, count+1, 0)
	if err != nil {
		return nil, false, err
	}
	more := len(list) > count
	if more {
		list = list[:count]
	}
	rows := make([]ChatRow, len(list))
	for i, c := range list {
		rows[i] = ChatRow{
			Href: fmt.Sprintf("/s/%s/c/%d", sel.Key, c.ID), Title: c.Title, Initials: initials(c.Title), Avatar: avatarURL(c.AvatarSHA),
			Preview: preview(c), When: s.when(c.LastAt), Current: c.ID == currentChat,
		}
	}
	return rows, more, nil
}

// chatCount reads how many chats the list should show, from the "chats" parameter.
func chatCount(r *http.Request) int {
	n, err := strconv.Atoi(r.URL.Query().Get("chats"))
	if err != nil || n < chatsPerPage {
		return chatsPerPage
	}
	return min(n, maxChatRows)
}

// sourceShell loads what every page of a source shows: the picker and the
// chat list. It answers the request itself when the source is unknown.
func (s *Server) sourceShell(w http.ResponseWriter, r *http.Request, sess archive.Session, currentChat int64) (sourcePage, selection, bool) {
	all, err := s.sources()
	if err != nil {
		s.fail(w, err)
		return sourcePage{}, selection{}, false
	}
	sel, ok := s.selection(r.PathValue("source"), all)
	if !ok {
		http.NotFound(w, r)
		return sourcePage{}, selection{}, false
	}
	if sel.Display != connector.DisplayChats {
		http.Error(w, "This source is shown as mail, which this version of arc5 cannot display yet.", http.StatusNotImplemented)
		return sourcePage{}, selection{}, false
	}
	data := sourcePage{Page: s.chrome(r, sess, sel.Name, all, sel.Key)}
	data.Search = &SearchBox{Action: "/s/" + sel.Key, Results: "/s/" + sel.Key + "/results", Query: searchQuery(r)}
	results, err := s.results(r.Context(), r, sel)
	switch {
	case errors.Is(err, errNoSuchPerson):
		http.NotFound(w, r)
		return sourcePage{}, selection{}, false
	case err != nil:
		s.fail(w, err)
		return sourcePage{}, selection{}, false
	case results != nil:
		data.Results = results
	default:
		count := chatCount(r)
		rows, more, err := s.chatRows(sel, currentChat, count)
		if err != nil {
			s.fail(w, err)
			return sourcePage{}, selection{}, false
		}
		data.Chats = rows
		if more {
			path := r.URL.Path // the chat list grows in place, on a conversation's page too
			if r.PathValue("chat") == "" {
				path = "/s/" + sel.Key
			}
			data.MoreChatsHref = fmt.Sprintf("%s?chats=%d", path, count+chatsPerPage)
		}
	}
	remember(w, r, sel.Key)
	return data, sel, true
}

func (s *Server) sourceView(w http.ResponseWriter, r *http.Request, sess archive.Session) {
	if s.mailRoute(w, r, sess, false) {
		return
	}
	data, _, ok := s.sourceShell(w, r, sess, 0)
	if !ok {
		return
	}
	s.render(w, http.StatusOK, "source", data)
}
