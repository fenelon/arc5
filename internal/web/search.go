package web

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/fenelon/arc5/internal/archive"
	"github.com/fenelon/arc5/internal/model"
	"github.com/fenelon/arc5/internal/search"
)

const (
	resultsPerPage = search.DefaultLimit
	maxResultPages = 40  // 2,000 results; narrow the search to see more
	maxQueryRunes  = 500 // longer text is cut before it is searched
)

// errNoSuchPerson is a person parameter that names nobody in the selection.
var errNoSuchPerson = errors.New("no such person here")

// SearchBox is the search field in the top bar.
type SearchBox struct {
	Action  string // where the form goes: the source's page
	Results string // where the search controller fetches results from
	Query   string
}

// Piece is a run of text, marked when the search matched it.
type Piece struct {
	Text string
	Mark bool
}

func pieces(fragments []search.Fragment) []Piece {
	out := make([]Piece, len(fragments))
	for i, f := range fragments {
		out[i] = Piece{Text: f.Text, Mark: f.Match}
	}
	return out
}

// NameRow is a chat whose title matched.
type NameRow struct {
	Href, Initials, Detail string
	Avatar                 string // the chat's picture, or "" for initials
	Title                  []Piece
}

// PersonRow is a person whose name matched.
type PersonRow struct {
	Href   string
	Avatar string // their picture, or ""
	Name   []Piece
}

// HitRow is a message that matched.
type HitRow struct {
	Href, Chat, When string
	Prefix           string // "You: " or the sender's name, as in the chat list
	Snippet          []Piece
	Files            [][]Piece
	Deleted, Current bool
}

// Results replace the chat list while there is a search.
type Results struct {
	Query     string
	Chats     []NameRow
	People    []PersonRow
	Person    string // whose messages these are, when one person was chosen
	AllHref   string // back to every result, from one person's messages
	Hits      []HitRow
	MoreHref  string
	FirstHref string
	Filters   *FilterBox // nil for one person's messages
	Empty     bool
	Note      string
}

// searchQuery is the search text of a request, cut to a sane length.
func searchQuery(r *http.Request) string {
	q := r.URL.Query().Get("q")
	if utf8.RuneCountInString(q) > maxQueryRunes {
		q = string([]rune(q)[:maxQueryRunes])
	}
	return q
}

// keepQuery is what links within a conversation add so the results stay.
func keepQuery(q string) string {
	if strings.TrimSpace(q) == "" {
		return ""
	}
	return "&q=" + url.QueryEscape(q)
}

func pageNumber(r *http.Request) int {
	n, err := strconv.Atoi(r.URL.Query().Get("page"))
	if err != nil || n < 1 {
		return 1
	}
	return min(n, maxResultPages)
}

// results runs the request's search within a selection. It returns nil when
// there is no search.
func (s *Server) results(ctx context.Context, r *http.Request, sel selection) (*Results, error) {
	q := searchQuery(r)
	if strings.TrimSpace(q) == "" {
		return nil, nil
	}
	plain := "/s/" + sel.Key + "?q=" + url.QueryEscape(q)
	filters := readFilters(r, s.deps.Location)
	keep := "&q=" + url.QueryEscape(q) + filters.suffix() // what links from the results carry
	base := plain + filters.suffix()
	res := &Results{Query: q}
	searcher := s.deps.Index.Searcher()
	page := pageNumber(r)
	query := search.Query{Text: q, AccountIDs: sel.AccountIDs, Limit: resultsPerPage, Offset: (page - 1) * resultsPerPage}
	filters.apply(&query, s.deps.Location)
	pageBase := base

	if p := r.URL.Query().Get("person"); p != "" {
		id, err := strconv.ParseInt(p, 10, 64)
		if err != nil {
			return nil, errNoSuchPerson
		}
		person, err := s.deps.Store.Sender(id)
		if errors.Is(err, archive.ErrNotFound) || (err == nil && !containsID(sel.AccountIDs, person.AccountID)) {
			return nil, errNoSuchPerson
		}
		if err != nil {
			return nil, err
		}
		res.Person = person.DisplayName
		if res.Person == "" {
			res.Person = person.Username
		}
		res.AllHref = base
		query.Text, query.SenderID, query.Sort = "", id, search.ByNewest
		pageBase = fmt.Sprintf("%s&person=%d", base, id)
	} else {
		res.Filters = filters.box("/s/"+sel.Key, q, plain)
	}
	if res.Person == "" && page == 1 {
		names, err := searcher.Names(ctx, q, sel.AccountIDs, 0)
		if err != nil {
			return nil, err
		}
		for _, c := range names.Chats {
			res.Chats = append(res.Chats, NameRow{
				Href:     fmt.Sprintf("/s/%s/c/%d?%s", sel.Key, c.ChatID, keep[1:]),
				Initials: initials(fragmentText(c.Title)), Avatar: avatarURL(c.AvatarSHA), Title: pieces(c.Title),
				Detail: kindLabel(c.Kind) + ", last message " + s.when(c.LastAt),
			})
		}
		for _, p := range names.People {
			res.People = append(res.People, PersonRow{Href: fmt.Sprintf("%s&person=%d", base, p.SenderID), Avatar: avatarURL(p.AvatarSHA), Name: pieces(p.Name)})
		}
	}

	found, err := searcher.Search(ctx, query)
	if err != nil {
		return nil, err
	}
	at, _ := strconv.ParseInt(r.URL.Query().Get("at"), 10, 64)
	for _, h := range found.Hits {
		row := HitRow{
			Href: fmt.Sprintf("/s/%s/c/%d?at=%d%s", sel.Key, h.ChatID, h.MessageID, keep),
			Chat: h.ChatTitle, When: s.when(h.SentAt), Snippet: pieces(h.Snippet),
			Deleted: h.Deleted, Current: h.MessageID == at,
		}
		switch {
		case h.Outgoing:
			row.Prefix = "You: "
		case h.ChatKind != model.ChatPrivate && h.SenderName != "":
			row.Prefix = h.SenderName + ": "
		}
		for _, f := range h.Files {
			row.Files = append(row.Files, pieces(f))
		}
		res.Hits = append(res.Hits, row)
	}
	if found.More && page < maxResultPages {
		res.MoreHref = fmt.Sprintf("%s&page=%d", pageBase, page+1)
	}
	if page > 1 {
		res.FirstHref = pageBase
	}
	res.Empty = len(res.Chats) == 0 && len(res.People) == 0 && len(res.Hits) == 0
	if s.deps.Index.Status().Rebuilding {
		res.Note = "The search index is being rebuilt, so some messages are not found yet."
	}
	return res, nil
}

func fragmentText(fragments []search.Fragment) string {
	var b strings.Builder
	for _, f := range fragments {
		b.WriteString(f.Text)
	}
	return b.String()
}

// resultsFragment answers the search controller: the left column's content
// for the typed text, or the chat list when the text is empty.
func (s *Server) resultsFragment(w http.ResponseWriter, r *http.Request, sess archive.Session) {
	data, _, ok := s.sourceShell(w, r, sess, 0)
	if !ok {
		return
	}
	s.render(w, http.StatusOK, "list", data)
}
