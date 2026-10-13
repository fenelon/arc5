package web

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/fenelon/arc5/internal/archive"
	"github.com/fenelon/arc5/internal/connector"
	"github.com/fenelon/arc5/internal/model"
)

const (
	threadsPerPage = 50
	maxThreadPages = 200
	snippetRunes   = 160
)

// mailPage is a mail source: its folders beside a conversation list, a
// thread, or search results.
type mailPage struct {
	Page
	Box       MailBox
	Folders   []FolderLink
	Heading   string
	Checks    []CheckLink
	Rows      []ThreadRow
	OlderHref string
	Empty     string
	Results   *Results
	Thread    *ThreadView
}

// MailBox heads the folder column.
type MailBox struct{ Name, Address string }

// FolderLink is one folder in the folder column.
type FolderLink struct {
	Label, Href, Icon string // Icon: inbox, sent, all or folder
	Current           bool
}

// CheckLink is a filter drawn as a checkbox; following it turns it on or off.
type CheckLink struct {
	Label, Href string
	On          bool
}

// ThreadRow is one conversation in the list.
type ThreadRow struct {
	Href, Initials, Name, Detail, Count, Subject, Snippet, When string
	Files, Deleted                                              bool
}

// mailFolder is the folder a mail page shows: a role across the selection,
// or one folder of one mailbox.
type mailFolder struct {
	param  string // its value of the folder parameter
	label  string
	filter archive.ThreadFilter
}

var roleFolders = []struct {
	param, label, icon string
	role               model.FolderRole
}{
	{"inbox", "Inbox", "inbox", model.FolderInbox},
	{"sent", "Sent", "sent", model.FolderSent},
	{"all", "All mail", "all", ""},
}

var errNoSuchFolder = errors.New("no such folder here")

// mailFolders lists the folder column's links and finds the folder the
// request asks for (the Inbox when it names none).
func (s *Server) mailFolders(r *http.Request, sel selection) ([]FolderLink, mailFolder, error) {
	asked := r.URL.Query().Get("folder")
	if asked == "" {
		asked = "inbox"
	}
	base := "/s/" + sel.Key + "?folder="
	var (
		links   []FolderLink
		current mailFolder
		found   bool
	)
	for _, rf := range roleFolders {
		on := asked == rf.param
		links = append(links, FolderLink{Label: rf.label, Href: base + rf.param, Icon: rf.icon, Current: on})
		if on {
			current, found = mailFolder{param: rf.param, label: rf.label, filter: archive.ThreadFilter{Role: rf.role}}, true
		}
	}
	if sel.one() {
		folders, err := s.deps.Store.AccountFolders(sel.AccountIDs[0])
		if err != nil {
			return nil, mailFolder{}, err
		}
		for _, f := range folders {
			if !f.Archived || f.Role == model.FolderInbox || f.Role == model.FolderSent || f.Role == model.FolderAll {
				continue
			}
			param := "f" + strconv.FormatInt(f.ID, 10)
			on := asked == param
			links = append(links, FolderLink{Label: f.Name, Href: base + param, Icon: "folder", Current: on})
			if on {
				current, found = mailFolder{param: param, label: f.Name, filter: archive.ThreadFilter{FolderID: f.ID}}, true
			}
		}
	}
	if !found {
		return nil, mailFolder{}, errNoSuchFolder
	}
	return links, current, nil
}

// mailRoute answers a mail source's pages; it reports false for a source
// shown as chats, which the caller then serves.
func (s *Server) mailRoute(w http.ResponseWriter, r *http.Request, sess archive.Session, fragment bool) bool {
	all, err := s.sources()
	if err != nil {
		s.fail(w, err)
		return true
	}
	sel, ok := s.selection(r.PathValue("source"), all)
	if !ok || sel.Display != connector.DisplayMail {
		return false
	}
	data := mailPage{Page: s.chrome(r, sess, sel.Name, all, sel.Key)}
	data.Search = &SearchBox{Action: "/s/" + sel.Key, Results: "/s/" + sel.Key + "/results", Query: searchQuery(r),
		Placeholder: "Search mail by word, sender, address or subject"}
	data.Box = MailBox{Name: sel.Name}
	if sel.one() {
		if acc, err := s.deps.Store.Account(sel.AccountIDs[0]); err == nil {
			data.Box.Address = acc.Address
		}
	} else {
		data.Box.Address = plural(len(sel.AccountIDs), "mailbox", "mailboxes")
	}
	folders, folder, err := s.mailFolders(r, sel)
	switch {
	case errors.Is(err, errNoSuchFolder):
		http.NotFound(w, r)
		return true
	case err != nil:
		s.fail(w, err)
		return true
	}
	data.Folders, data.Heading = folders, folder.label

	results, err := s.results(r.Context(), r, sel)
	switch {
	case errors.Is(err, errNoSuchPerson):
		http.NotFound(w, r)
		return true
	case err != nil:
		s.fail(w, err)
		return true
	}
	chatID := int64(0)
	if c := r.PathValue("chat"); c != "" {
		if chatID, err = strconv.ParseInt(c, 10, 64); err != nil {
			http.NotFound(w, r)
			return true
		}
	}
	switch {
	case chatID != 0 && !fragment:
		thread, ok, err := s.threadView(r, sel, folder, chatID)
		if err != nil {
			s.fail(w, err)
			return true
		}
		if !ok {
			http.NotFound(w, r)
			return true
		}
		data.Thread, data.Title = thread, thread.Title
		data.Results = results
	case results != nil:
		results.Mail = true
		data.Results = results
	default:
		if err := s.threadRows(r, sel, folder, &data); err != nil {
			s.fail(w, err)
			return true
		}
	}
	if data.Results != nil {
		data.Results.Mail = true
	}
	remember(w, r, sel.Key)
	name := "mail"
	if fragment {
		name = "mail-main"
	}
	s.render(w, http.StatusOK, name, data)
	return true
}

// threadRows fills in the conversation list of a folder.
func (s *Server) threadRows(r *http.Request, sel selection, folder mailFolder, data *mailPage) error {
	q := r.URL.Query()
	filter := folder.filter
	filter.Files, filter.Deleted = q.Get("files") == "1", q.Get("deleted") == "1"
	page := 1
	if n, err := strconv.Atoi(q.Get("page")); err == nil && n > 1 {
		page = min(n, maxThreadPages)
	}
	list, err := s.deps.Store.Threads(sel.AccountIDs, filter, threadsPerPage+1, (page-1)*threadsPerPage)
	if err != nil {
		return err
	}
	link := func(files, deleted bool, page int) string {
		v := url.Values{"folder": {folder.param}}
		if files {
			v.Set("files", "1")
		}
		if deleted {
			v.Set("deleted", "1")
		}
		if page > 1 {
			v.Set("page", strconv.Itoa(page))
		}
		return "/s/" + sel.Key + "?" + encodeOrdered(v, "folder", "files", "deleted", "page")
	}
	data.Checks = []CheckLink{
		{Label: "With attachments", On: filter.Files, Href: link(!filter.Files, filter.Deleted, 1)},
		{Label: "Deleted at source", On: filter.Deleted, Href: link(filter.Files, !filter.Deleted, 1)},
	}
	if len(list) > threadsPerPage {
		list = list[:threadsPerPage]
		data.OlderHref = link(filter.Files, filter.Deleted, page+1)
	}
	keep := ""
	if folder.param != "inbox" {
		keep = "?folder=" + folder.param
	}
	for _, t := range list {
		row := ThreadRow{
			Href: fmt.Sprintf("/s/%s/c/%d%s", sel.Key, t.ID, keep), Initials: initials(t.Person), Name: t.Person,
			Subject: t.Title, Snippet: snippet(t.Snippet), When: s.when(t.LastAt), Files: t.HasFiles, Deleted: t.AllDeleted,
		}
		if row.Name == "" {
			row.Name = "Unknown sender"
		}
		row.Detail = participants(t)
		if t.Messages > 1 {
			row.Count = plural(t.Messages, "message", "messages")
		}
		data.Rows = append(data.Rows, row)
	}
	if len(data.Rows) == 0 {
		data.Empty = "No conversations here."
		if page == 1 && !filter.Files && !filter.Deleted && folder.param == "inbox" {
			data.Empty = "Nothing has been archived here yet."
		}
	}
	return nil
}

// encodeOrdered writes query parameters in the given order, so links read
// the same way every time.
func encodeOrdered(v url.Values, order ...string) string {
	var parts []string
	for _, k := range order {
		if val := v.Get(k); val != "" {
			parts = append(parts, url.QueryEscape(k)+"="+url.QueryEscape(val))
		}
	}
	return strings.Join(parts, "&")
}

// participants is the line after a conversation's person: their address,
// the others, and "you" when the owner took part.
func participants(t archive.ThreadSummary) string {
	line := t.PersonAddress
	if line == "" || line == t.Person {
		line = ""
	}
	others := strings.Join(t.Others, ", ")
	switch {
	case others != "" && t.WithOwner:
		others += " and you"
	case t.WithOwner:
		others = "and you"
	}
	if others == "" {
		return line
	}
	if line == "" {
		return others
	}
	return line + ", " + others
}

// snippet is the start of a message's own words: quoted text, and the line
// introducing it, are left out.
func snippet(text string) string {
	body, _, _ := splitQuote(text)
	out := strings.Join(strings.Fields(body), " ")
	if utf8.RuneCountInString(out) > snippetRunes {
		out = string([]rune(out)[:snippetRunes]) + "…"
	}
	return out
}

// splitQuote separates a message's own text from the quote of earlier mail
// at its end, as a top-posted reply has: lead is the "On … wrote:" line, if
// any. Text whose quote is followed by more of the writer's own words (an
// inline reply) is not split.
func splitQuote(text string) (body, lead, quote string) {
	lines := strings.Split(text, "\n")
	start := -1
	for i, line := range lines {
		if strings.HasPrefix(strings.TrimSpace(line), ">") {
			start = i
			break
		}
	}
	if start < 0 {
		return text, "", ""
	}
	for _, line := range lines[start:] {
		if t := strings.TrimSpace(line); t != "" && !strings.HasPrefix(t, ">") {
			return text, "", "" // an inline reply: keep it whole
		}
	}
	cut := start
	for cut > 0 && strings.TrimSpace(lines[cut-1]) == "" {
		cut--
	}
	if cut > 0 && strings.HasSuffix(strings.TrimSpace(lines[cut-1]), ":") {
		lead = strings.TrimSpace(lines[cut-1])
		cut--
	}
	return strings.TrimRight(strings.Join(lines[:cut], "\n"), "\n "), lead, strings.Join(lines[start:], "\n")
}
