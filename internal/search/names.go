package search

import (
	"context"
	"database/sql"
	"sort"
	"strings"
	"time"

	"github.com/fenelon/arc5/internal/model"
)

// DefaultNameLimit is how many chats, and how many people, Names returns.
const DefaultNameLimit = 8

// ChatMatch is a chat whose title the query matched.
type ChatMatch struct {
	ChatID    int64
	AccountID int64
	Kind      model.ChatKind
	Title     []Fragment // the title, with the matched words marked
	LastAt    time.Time  // when its newest message was sent
	AvatarSHA string     // its picture in the media store, or ""
}

// PersonMatch is a sender whose name or username the query matched.
type PersonMatch struct {
	SenderID  int64
	AccountID int64
	Name      []Fragment // the display name, with the matched words marked
	Username  string
	AvatarSHA string // their picture in the media store, or ""
}

// Names is what a query found among chat titles and people's names.
type Names struct {
	Chats  []ChatMatch
	People []PersonMatch
}

// Names finds chats by title and people by display name or username, so the
// owner can get to a conversation by typing who it was with. Every word typed
// must begin a word of the name; case, accents and ё/е are ignored, as in
// message search. Chats come whole-word matches first, then most recent
// first. A chat with no messages and a person with no name are not listed.
// accountIDs limits the search to those accounts; nil means every account.
// limit caps each list; zero means DefaultNameLimit.
//
// Names are matched by reading every chat and sender, which is a few thousand
// rows for a personal archive, not through the message index.
func (s *Searcher) Names(ctx context.Context, text string, accountIDs []int64, limit int) (Names, error) {
	if limit <= 0 {
		limit = DefaultNameLimit
	}
	var words []string
	for _, t := range withoutTails(s.analyzer.Tokens(prepare(text))) {
		words = append(words, t.Exact)
	}
	if len(words) == 0 {
		return Names{}, nil
	}
	if len(words) > maxQueryTerms {
		words = words[:maxQueryTerms]
	}
	highlight := parsed{exact: map[string]bool{}, stems: map[string]bool{}, prefixes: words}
	scope, args := "", ids(accountIDs)
	if len(accountIDs) > 0 {
		scope = ` AND account_id IN (` + placeholders(len(accountIDs)) + `)`
	}

	type rankedChat struct {
		ChatMatch
		whole bool
	}
	var chats []rankedChat
	rows, err := s.db.QueryContext(ctx, `
		SELECT c.id, c.account_id, c.kind, c.title,
		       (SELECT MAX(m.sent_at) FROM messages m WHERE m.chat_id = c.id), c.avatar_sha256
		FROM chats c WHERE 1`+scope, args...)
	if err != nil {
		return Names{}, err
	}
	defer rows.Close()
	for rows.Next() {
		var (
			c     rankedChat
			kind  string
			title string
			last  sql.NullInt64
		)
		if err := rows.Scan(&c.ChatID, &c.AccountID, &kind, &title, &last, &c.AvatarSHA); err != nil {
			return Names{}, err
		}
		if !last.Valid {
			continue // nothing archived in this chat
		}
		whole, ok := s.analyzer.nameMatches(title, words)
		if !ok {
			continue
		}
		c.whole = whole
		c.Kind = model.ChatKind(kind)
		c.Title = s.analyzer.snippet(title, highlight)
		c.LastAt = time.Unix(last.Int64, 0).UTC()
		chats = append(chats, c)
	}
	if err := rows.Err(); err != nil {
		return Names{}, err
	}
	rows.Close()
	sort.SliceStable(chats, func(i, j int) bool {
		if chats[i].whole != chats[j].whole {
			return chats[i].whole
		}
		return chats[i].LastAt.After(chats[j].LastAt)
	})

	type rankedPerson struct {
		PersonMatch
		whole bool
		name  string
	}
	var people []rankedPerson
	rows, err = s.db.QueryContext(ctx, `
		SELECT id, account_id, display_name, username, avatar_sha256 FROM senders
		WHERE (display_name != '' OR username != '')`+scope, args...)
	if err != nil {
		return Names{}, err
	}
	defer rows.Close()
	for rows.Next() {
		var p rankedPerson
		if err := rows.Scan(&p.SenderID, &p.AccountID, &p.name, &p.Username, &p.AvatarSHA); err != nil {
			return Names{}, err
		}
		whole, ok := s.analyzer.nameMatches(p.name+" "+p.Username, words)
		if !ok {
			continue
		}
		p.whole = whole
		p.Name = s.analyzer.snippet(p.name, highlight)
		people = append(people, p)
	}
	if err := rows.Err(); err != nil {
		return Names{}, err
	}
	sort.SliceStable(people, func(i, j int) bool {
		if people[i].whole != people[j].whole {
			return people[i].whole
		}
		return people[i].name < people[j].name
	})

	var out Names
	for _, c := range chats[:min(limit, len(chats))] {
		out.Chats = append(out.Chats, c.ChatMatch)
	}
	for _, p := range people[:min(limit, len(people))] {
		out.People = append(out.People, p.PersonMatch)
	}
	return out, nil
}

// nameMatches reports whether every typed word begins a word of name, and
// whether each of them is a whole word of it.
func (a *Analyzer) nameMatches(name string, words []string) (whole, ok bool) {
	tokens := a.Tokens(prepare(name))
	whole = true
	for _, word := range words {
		found, exact := false, false
		for _, t := range tokens {
			if t.Exact == word {
				found, exact = true, true
				break
			}
			if strings.HasPrefix(t.Exact, word) {
				found = true
			}
		}
		if !found {
			return false, false
		}
		if !exact {
			whole = false
		}
	}
	return whole, true
}
