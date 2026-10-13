package web

import (
	"errors"
	"fmt"
	"io/fs"
	"mime"
	"net/http"
	"os"
	"path"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/fenelon/arc5/internal/archive"
	"github.com/fenelon/arc5/internal/model"
)

// threadMessages is the most of a conversation one page shows.
const threadMessages = 300

// ThreadView is an open mail conversation.
type ThreadView struct {
	BackHref, BackLabel string
	Title, Summary      string
	Cards               []Card
	Self                string // this page, where the attachments' forms come back to
}

// Card is one message of a conversation, folded to one line or open.
type Card struct {
	Anchor, Initials, Name, Address string
	To, When, Position, Deleted     string
	Snippet                         string
	Text, Lead, Quote               string
	TextParts, QuoteParts           []TextPart // Text, and Lead with Quote, with links made short
	Marked                          []Piece    // Text with the search's words marked
	Files                           []MailFile
	OriginalHref                    string
	Open, Own, Target               bool
}

// MailFile is an attachment as a file card.
type MailFile struct {
	ID                 int64
	Name, Detail, Mode string // Mode as Attachment's
	URL                string
}

var countWords = [...]string{"", "one", "two", "three", "four", "five", "six", "seven", "eight", "nine", "ten"}

// foldedNote says how many messages are folded: the earliest ones, unless
// one of those was opened (the message a search led to).
func foldedNote(n int, earliest bool) string {
	count := fmt.Sprint(n)
	if n < len(countWords) {
		count = countWords[n]
	}
	switch {
	case n == 1 && earliest:
		return " The earliest is folded; select it to open it."
	case n == 1:
		return " One is folded; select it to open it."
	case n > 1 && earliest:
		return fmt.Sprintf(" The %s earliest are folded; select one to open it.", count)
	case n > 1:
		return fmt.Sprintf(" %s are folded; select one to open it.", strings.ToUpper(count[:1])+count[1:])
	}
	return ""
}

// dateSpan is the days a conversation took: "27 September 2026", "25 to 27
// September 2026", "31 August to 2 September 2026" or with both years.
func dateSpan(first, last time.Time) string {
	switch {
	case first.Format(time.DateOnly) == last.Format(time.DateOnly):
		return last.Format("2 January 2006")
	case first.Year() == last.Year() && first.Month() == last.Month():
		return first.Format("2") + " to " + last.Format("2 January 2006")
	case first.Year() == last.Year():
		return first.Format("2 January") + " to " + last.Format("2 January 2006")
	}
	return first.Format("2 January 2006") + " to " + last.Format("2 January 2006")
}

func addressList(list []model.Address) string {
	parts := make([]string, len(list))
	for i, a := range list {
		parts[i] = a.Address
		if parts[i] == "" {
			parts[i] = a.Name
		}
	}
	return strings.Join(parts, ", ")
}

// fileType names a file's type for its card, such as "PDF".
func fileType(name, mimeType string) string {
	if ext := strings.TrimPrefix(path.Ext(name), "."); ext != "" && len(ext) <= 5 {
		return strings.ToUpper(ext)
	}
	if sub := strings.SplitN(mimeType, "/", 2); len(sub) == 2 && sub[1] != "" && !strings.ContainsAny(sub[1], ".-+") {
		return strings.ToUpper(sub[1])
	}
	return "File"
}

func mailFile(a archive.StoredAttachment) MailFile {
	v := attachmentView(a)
	f := MailFile{ID: a.ID, Name: v.Name, Mode: v.Mode, URL: v.URL, Detail: fileType(a.FileName, a.MimeType) + ", " + humanBytes(a.Size)}
	if v.Mode != "image" && v.Mode != "audio" && v.Mode != "video" && v.Mode != "file" {
		f.Detail = v.Detail
	}
	return f
}

// threadView reads a mail conversation for its page. It reports false when
// the conversation is not in the selection.
func (s *Server) threadView(r *http.Request, sel selection, folder mailFolder, chatID int64) (*ThreadView, bool, error) {
	chat, err := s.deps.Store.Chat(chatID)
	if errors.Is(err, archive.ErrNotFound) || (err == nil && (!containsID(sel.AccountIDs, chat.AccountID) || chat.Kind != model.ChatThread)) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	msgs, _, err := s.deps.Store.ThreadMessages(chatID, threadMessages)
	if err != nil {
		return nil, false, err
	}
	loc := s.deps.Location
	view := &ThreadView{Title: chat.Title, BackLabel: "Back to " + folder.label, BackHref: "/s/" + sel.Key + "?folder=" + folder.param}
	q := searchQuery(r)
	keep := keepQuery(q)
	if keep != "" {
		view.BackLabel, view.BackHref = "Back to results", "/s/"+sel.Key+"?"+keep[1:]
	}
	view.Self = r.URL.RequestURI()
	target, _ := strconv.ParseInt(r.URL.Query().Get("at"), 10, 64)
	total := chat.Messages
	if len(msgs) > 0 {
		view.Summary = plural(total, "message", "messages") + ", " + dateSpan(chat.FirstAt.In(loc), msgs[len(msgs)-1].SentAt.In(loc)) + "."
	}
	folded, earliest := 0, true // earliest: no open message comes before a folded one
	for i, m := range msgs {
		sent := m.SentAt.In(loc)
		c := Card{
			Anchor: fmt.Sprintf("m%d", m.ID), Own: m.Outgoing, Target: m.ID == target,
			Name: m.From.Name, Address: m.From.Address, Position: fmt.Sprintf("%d of %d", total-len(msgs)+i+1, total),
			Open: i >= len(msgs)-2 || m.ID == target,
		}
		if c.Name == "" {
			c.Name = c.Address
		}
		c.Initials = initials(c.Name)
		if m.Outgoing {
			c.Name, c.Initials = "You", "You"
		}
		if to := addressList(m.To); to != "" {
			c.To = "to " + to
		}
		if cc := addressList(m.Cc); cc != "" {
			c.To = strings.TrimPrefix(c.To+"; cc "+cc, "; ")
		}
		if !m.DeletedAt.IsZero() {
			c.Deleted = "Deleted at source"
		}
		if c.Open {
			c.When = sent.Format("2 Jan 2006, 15:04")
		} else {
			c.When = sent.Format("2 Jan, 15:04")
			folded++
			if len(view.Cards) > 0 && view.Cards[len(view.Cards)-1].Open {
				earliest = false
			}
		}
		c.Snippet = snippet(m.Text)
		c.Text, c.Lead, c.Quote = splitQuote(m.Text)
		c.TextParts = linkParts(c.Text)
		if c.Quote != "" {
			quote := c.Quote
			if c.Lead != "" {
				quote = c.Lead + "\n" + quote
			}
			c.QuoteParts = linkParts(quote)
		}
		if c.Target && keep != "" {
			c.Marked = pieces(s.deps.Index.Searcher().Highlight(q, c.Text))
		}
		for _, a := range m.Attachments {
			c.Files = append(c.Files, mailFile(a))
		}
		if m.HasOriginal {
			c.OriginalHref = fmt.Sprintf("/original/%d", m.ID)
		}
		view.Cards = append(view.Cards, c)
	}
	view.Summary += foldedNote(folded, earliest)
	return view, true, nil
}

// serveOriginal sends a mail's original as received, as a download.
func (s *Server) serveOriginal(w http.ResponseWriter, r *http.Request, _ archive.Session) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	file, subject, err := s.deps.Store.OriginalFile(id)
	if errors.Is(err, archive.ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		s.fail(w, err)
		return
	}
	f, err := os.Open(file)
	if errors.Is(err, fs.ErrNotExist) {
		http.Error(w, "This file is missing from the data folder.", http.StatusNotFound)
		return
	}
	if err != nil {
		s.fail(w, err)
		return
	}
	defer f.Close()
	name := strings.Map(func(r rune) rune {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || r == ' ' || r == '-' || r == '_' {
			return r
		}
		return -1
	}, subject)
	if name = strings.Join(strings.Fields(name), " "); name == "" {
		name = "message"
	}
	if runes := []rune(name); len(runes) > 80 {
		name = strings.TrimSpace(string(runes[:80]))
	}
	h := w.Header()
	h.Set("Content-Type", "message/rfc822")
	h.Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": name + ".eml"}))
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Content-Security-Policy", mediaPolicy)
	h.Set("Cache-Control", "private, no-store")
	http.ServeContent(w, r, "", time.Time{}, f)
}

// TextPart is a run of mail text, or a link in it: Href is set for a link,
// shown as Label.
type TextPart struct {
	Text, Href, Label string
}

// linkPattern finds web addresses in text, with the angle brackets mail
// writes around an address after a link's words.
var linkPattern = regexp.MustCompile(`<https?://[^\s<>"]+>|https?://[^\s<>"]+`)

// linkLabel is how long a link's shown address may be.
const linkLabel = 38

// linkParts splits text into runs and links to web addresses, so a long
// tracking address reads as a short link. Only http and https become links.
func linkParts(text string) []TextPart {
	var out []TextPart
	plain := func(s string) {
		if s == "" {
			return
		}
		if n := len(out); n > 0 && out[n-1].Href == "" {
			out[n-1].Text += s
			return
		}
		out = append(out, TextPart{Text: s})
	}
	at := 0
	for _, m := range linkPattern.FindAllStringIndex(text, -1) {
		start, end := m[0], m[1]
		href := text[start:end]
		if strings.HasPrefix(href, "<") {
			href = href[1 : len(href)-1]
		} else {
			// Punctuation after an address ends the sentence, not the address.
			trimmed := strings.TrimRight(href, ".,;:!?)]}'")
			end -= len(href) - len(trimmed)
			href = trimmed
		}
		plain(text[at:start])
		label := strings.TrimPrefix(strings.TrimPrefix(strings.TrimPrefix(href, "https://"), "http://"), "www.")
		if r := []rune(label); len(r) > linkLabel {
			label = string(r[:linkLabel-1]) + "…"
		}
		out = append(out, TextPart{Href: href, Label: label})
		at = end
	}
	plain(text[at:])
	return out
}
