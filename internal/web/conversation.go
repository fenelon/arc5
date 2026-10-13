package web

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/fenelon/arc5/internal/archive"
	"github.com/fenelon/arc5/internal/model"
)

const messagesPerPage = 60

// Conversation is the open chat.
type Conversation struct {
	Title       string
	Subtitle    string
	BackHref    string // the chat list, for phones
	SelfHref    string // this page, for forms that come back to it
	OlderHref   string
	NewerHref   string
	ScrollTo    string // where the page opens: "end", "start" or a message's anchor
	Query       string // the search the page was opened from, kept in its links
	Days        []Day
	Years       []RailYear
	YearOptions []YearOption
}

type Day struct {
	Label    string
	Key      string // the date, as 2006-01-02, so pages loaded in place can join a day split between them
	Messages []Message
}

type Message struct {
	Anchor        string // the element's id, "m" and the message ID
	Service       bool
	Outgoing      bool
	Target        bool // the message the page was opened at
	Sender        string
	ForwardedFrom string
	Text          string
	Marked        []Piece // Text with the search's words marked, for the message opened from a search
	Time          string
	Edited        string // when it was last edited, "at 15:04" or "on 2 Oct at 15:04", or ""
	Deleted       string // when the source deleted it, or ""
	Versions      []Version
	Replaced      []ReplacedFile // media edits replaced, oldest first
	Attachments   []Attachment
	Reply         *Reply // what it replies to, or nil
}

// Reply is what a reply answers, as a quote above it. Missing says it
// answers a message the archive does not have; Href is "" then.
type Reply struct {
	Href, Name, Text string
	Missing          bool
}

// quoteText is how a quoted message reads in one line: its text, or what it
// carried.
func quoteText(q *archive.Quote) string {
	if text := strings.TrimSpace(q.Text); text != "" {
		return text
	}
	switch q.FileKind {
	case model.AttachPhoto:
		return "Photo"
	case model.AttachVideo:
		return "Video"
	case model.AttachVoice:
		return "Voice message"
	case model.AttachAudio:
		return "Audio: " + q.File
	case model.AttachSticker:
		return "Sticker"
	case "":
		return "Message"
	}
	return "File: " + q.File
}

// ReplacedFile is an attachment an edit replaced. URL is "" when the file
// was never downloaded.
type ReplacedFile struct {
	Name, URL, Detail string
}

// Version is the text a message had before an edit.
type Version struct {
	Text  string
	Until string
}

type Attachment struct {
	ID        int64
	Mode      string // image, audio, video, file, skipped, withheld, pending, failed or unavailable
	Name      string
	Detail    string
	URL       string
	Ephemeral bool
}

// RailYear is one block of the year rail.
type RailYear struct {
	Label string
	Now   bool
	Bands []RailBand // newest quarter first
}

type RailBand struct {
	Href  string
	Label string
	Level int // 0 for no messages, up to 4 for the busiest quarter
	Now   bool
}

type YearOption struct {
	Value   string
	Label   string
	Current bool
}

func humanBytes(n int64) string {
	switch {
	case n < 1024:
		return fmt.Sprintf("%d B", n)
	case n < 1024*1024:
		return fmt.Sprintf("%.1f KB", float64(n)/1024)
	case n < 1024*1024*1024:
		return fmt.Sprintf("%.1f MB", float64(n)/(1024*1024))
	}
	return fmt.Sprintf("%.1f GB", float64(n)/(1024*1024*1024))
}

// inlineKind says how a stored file of this type may be shown in a page:
// "image", "audio", "video", or "" for a download only. The list is short on
// purpose: anything that can carry script, such as HTML or SVG, is not on it.
func inlineKind(mimeType string) string {
	switch strings.ToLower(strings.TrimSpace(strings.SplitN(mimeType, ";", 2)[0])) {
	case "image/jpeg", "image/png", "image/gif", "image/webp":
		return "image"
	case "audio/ogg", "audio/mpeg", "audio/mp4", "audio/aac", "audio/wav", "audio/x-wav", "audio/webm":
		return "audio"
	case "video/mp4", "video/webm":
		return "video"
	}
	return ""
}

func attachmentView(a archive.StoredAttachment) Attachment {
	v := Attachment{ID: a.ID, Name: a.FileName, Ephemeral: a.Ephemeral, URL: fmt.Sprintf("/media/%d", a.ID)}
	if v.Name == "" {
		v.Name = "Unnamed " + string(a.Kind)
	}
	size := humanBytes(a.Size)
	switch a.Status {
	case model.StatusStored:
		v.Mode, v.Detail = inlineKind(a.MimeType), size
		if v.Mode == "" {
			v.Mode = "file"
		}
	case model.StatusSkipped:
		v.Mode, v.Detail = "skipped", size+", not downloaded: "+a.Reason
		if a.Ephemeral {
			v.Mode = "withheld" // kept out by the source's setting; never fetched on request
		}
	case model.StatusFailed:
		v.Mode, v.Detail = "failed", "The download failed: "+a.Reason
	case model.StatusUnavailable:
		v.Mode, v.Detail = "unavailable", "No longer available at the source"
	default:
		v.Mode, v.Detail = "pending", "Waiting to be downloaded"
	}
	return v
}

// replacedView describes a file an edit replaced: a link while it is
// stored, otherwise why it is not.
func replacedView(a archive.StoredAttachment, loc *time.Location) ReplacedFile {
	view := attachmentView(a)
	f := ReplacedFile{Name: view.Name, Detail: "until " + a.Replaced.In(loc).Format("2 Jan 2006, 15:04")}
	if a.Status == model.StatusStored {
		f.URL, f.Detail = view.URL, humanBytes(a.Size)+", "+f.Detail
	} else if a.Reason != "" {
		f.Detail = a.Reason + ", " + f.Detail
	}
	return f
}

var quarterNames = [...]string{"January to March", "April to June", "July to September", "October to December"}

func plural(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return fmt.Sprintf("%d %s", n, many)
}

// rail builds the year rail from a chat's quarters, counted in the zone of
// now. now is the moment the page is reading, which marks one band and one year.
func rail(path, keep string, quarters []archive.Quarter, now time.Time) ([]RailYear, []YearOption) {
	if len(quarters) == 0 {
		return nil, nil
	}
	count := map[[2]int]int{}
	busiest, newest, oldest := 0, quarters[0].Year, quarters[0].Year
	for _, q := range quarters {
		count[[2]int{q.Year, q.Quarter}] = q.Messages
		busiest = max(busiest, q.Messages)
		newest, oldest = max(newest, q.Year), min(oldest, q.Year)
	}
	nowYear, nowQuarter := now.Year(), (int(now.Month())+2)/3
	var years []RailYear
	var options []YearOption
	for year := oldest; year <= newest; year++ {
		y := RailYear{Label: strconv.Itoa(year), Now: year == nowYear}
		for quarter := 1; quarter <= 4; quarter++ {
			n := count[[2]int{year, quarter}]
			level := 0
			if n > 0 {
				level = max(1, (n*4+busiest-1)/busiest)
			}
			y.Bands = append(y.Bands, RailBand{
				Href:  fmt.Sprintf("%s?date=%d-%02d-01%s", path, year, quarter*3-2, keep),
				Label: fmt.Sprintf("%s %d, %s", quarterNames[quarter-1], year, plural(n, "message", "messages")),
				Level: level, Now: year == nowYear && quarter == nowQuarter,
			})
		}
		years = append(years, y)
		// The phone's year picker lists the newest first, as it opens on them.
		options = append([]YearOption{{Value: fmt.Sprintf("%d-01-01", year), Label: y.Label, Current: y.Now}}, options...)
	}
	return years, options
}

func kindLabel(k model.ChatKind) string {
	switch k {
	case model.ChatGroup:
		return "Group"
	case model.ChatChannel:
		return "Channel"
	case model.ChatBot:
		return "Bot"
	}
	return "Private chat"
}

// anchor reads which part of the conversation the address asks for.
func (s *Server) anchor(r *http.Request) (archive.Anchor, int64) {
	q := r.URL.Query()
	id := func(name string) int64 {
		n, err := strconv.ParseInt(q.Get(name), 10, 64)
		if err != nil || n < 1 {
			return 0
		}
		return n
	}
	switch {
	case id("at") != 0:
		return archive.Anchor{Around: id("at")}, id("at")
	case id("before") != 0:
		return archive.Anchor{Before: id("before")}, 0
	case id("after") != 0:
		return archive.Anchor{After: id("after")}, 0
	}
	if day, err := time.ParseInLocation("2006-01-02", q.Get("date"), s.deps.Location); err == nil {
		return archive.Anchor{From: day}, 0
	}
	return archive.Anchor{}, 0
}

func (s *Server) conversationView(w http.ResponseWriter, r *http.Request, sess archive.Session) {
	chatID, err := strconv.ParseInt(r.PathValue("chat"), 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	data, sel, ok := s.sourceShell(w, r, sess, chatID)
	if !ok {
		return
	}
	chat, err := s.deps.Store.Chat(chatID)
	if err != nil || !containsID(sel.AccountIDs, chat.AccountID) {
		http.NotFound(w, r)
		return
	}
	store := s.deps.Store
	anchor, target := s.anchor(r)
	window, err := store.ChatMessages(chatID, anchor, messagesPerPage)
	if err == nil && len(window.Messages) == 0 && anchor != (archive.Anchor{}) {
		// The anchor led nowhere: show the end of the conversation instead.
		anchor, target = archive.Anchor{}, 0
		window, err = store.ChatMessages(chatID, anchor, messagesPerPage)
	}
	if err != nil {
		s.fail(w, err)
		return
	}
	quarters, err := store.ChatQuarters(chatID, s.deps.Location)
	if err != nil {
		s.fail(w, err)
		return
	}

	path := fmt.Sprintf("/s/%s/c/%d", sel.Key, chatID)
	q := searchQuery(r)
	keep := keepQuery(q)
	searching := keep != ""
	if n := chatCount(r); n > chatsPerPage {
		keep += fmt.Sprintf("&chats=%d", n) // the chat list stays as long as the owner made it
	}
	// part=messages asks for the page's messages alone, loaded in place as
	// the reader scrolls; forms in it return to the conversation itself.
	query := r.URL.Query()
	part := query.Get("part") == "messages"
	query.Del("part")
	self := r.URL.RequestURI()
	if part {
		self = r.URL.Path
		if len(query) > 0 {
			self += "?" + query.Encode()
		}
	}
	conv := &Conversation{Title: chat.Title, BackHref: "/s/" + sel.Key, SelfHref: self}
	if keep != "" {
		conv.BackHref = "/s/" + sel.Key + "?" + keep[1:]
	}
	if searching {
		conv.Query = q
	}
	conv.Subtitle = kindLabel(chat.Kind)
	if service, known := s.serviceOf(chat.AccountID); known {
		conv.Subtitle += " on " + service
	}
	if chat.Messages > 0 {
		conv.Subtitle += fmt.Sprintf(", %s since %s", plural(chat.Messages, "message", "messages"), chat.FirstAt.In(s.deps.Location).Format("January 2006"))
	}

	// reading is the moment the rail marks: the end of the page, unless the
	// page was opened at a date, a message, or just after one.
	reading := s.deps.Now()
	conv.ScrollTo = "end"
	if len(window.Messages) > 0 {
		reading = window.Messages[len(window.Messages)-1].SentAt
		if !anchor.From.IsZero() || anchor.After != 0 {
			reading, conv.ScrollTo = window.Messages[0].SentAt, "start"
		}
		for _, m := range window.Messages {
			if m.ID == target {
				reading, conv.ScrollTo = m.SentAt, fmt.Sprintf("m%d", m.ID)
			}
		}
		if window.HasOlder {
			conv.OlderHref = fmt.Sprintf("%s?before=%d%s", path, window.Messages[0].ID, keep)
		}
		if window.HasNewer {
			conv.NewerHref = fmt.Sprintf("%s?after=%d%s", path, window.Messages[len(window.Messages)-1].ID, keep)
		}
	}
	conv.Years, conv.YearOptions = rail(path, keep, quarters, reading.In(s.deps.Location))

	onPage := map[int64]bool{}
	for _, m := range window.Messages {
		onPage[m.ID] = true
	}
	for _, m := range window.Messages {
		sent := m.SentAt.In(s.deps.Location)
		label := sent.Format("Monday, 2 January 2006")
		if len(conv.Days) == 0 || conv.Days[len(conv.Days)-1].Label != label {
			conv.Days = append(conv.Days, Day{Label: label, Key: sent.Format(time.DateOnly)})
		}
		v := Message{
			Anchor: fmt.Sprintf("m%d", m.ID), Service: m.Kind == model.KindService, Outgoing: m.Outgoing,
			Target: m.ID == target, ForwardedFrom: m.ForwardedFrom, Text: m.Text, Time: sent.Format("15:04"),
		}
		if v.Target && searching {
			v.Marked = pieces(s.deps.Index.Searcher().Highlight(q, m.Text))
		}
		if !m.Outgoing && chat.Kind != model.ChatPrivate {
			v.Sender = m.SenderName
		}
		if !m.EditedAt.IsZero() {
			edited := m.EditedAt.In(s.deps.Location)
			switch {
			case edited.Format(time.DateOnly) == sent.Format(time.DateOnly):
				v.Edited = edited.Format("at 15:04")
			case edited.Year() == sent.Year():
				v.Edited = edited.Format("on 2 Jan at 15:04")
			default:
				v.Edited = edited.Format("on 2 Jan 2006 at 15:04")
			}
		}
		if !m.DeletedAt.IsZero() {
			v.Deleted = m.DeletedAt.In(s.deps.Location).Format("2 Jan 2006")
		}
		if m.Versions > 0 {
			versions, err := store.Versions(m.ID)
			if err != nil {
				s.fail(w, err)
				return
			}
			for _, old := range versions {
				v.Versions = append(v.Versions, Version{Text: old.Text, Until: old.ReplacedAt.In(s.deps.Location).Format("2 Jan 2006, 15:04")})
			}
		}
		for _, a := range m.Attachments {
			v.Attachments = append(v.Attachments, attachmentView(a))
		}
		for _, a := range m.Replaced {
			v.Replaced = append(v.Replaced, replacedView(a, s.deps.Location))
		}
		switch q := m.Reply; {
		case q != nil:
			name := q.ForwardedFrom
			switch {
			case name != "":
			case q.Outgoing:
				name = "You"
			case q.SenderName != "":
				name = q.SenderName
			default:
				name = chat.Title
			}
			v.Reply = &Reply{Href: fmt.Sprintf("%s?at=%d%s", path, q.ID, keep), Name: name, Text: quoteText(q)}
			if onPage[q.ID] {
				v.Reply.Href = fmt.Sprintf("#m%d", q.ID)
			}
		case m.ReplyToSourceID != "":
			v.Reply = &Reply{Missing: true}
		}
		day := &conv.Days[len(conv.Days)-1]
		day.Messages = append(day.Messages, v)
	}

	data.Conversation = conv
	data.Title = chat.Title
	if part {
		s.render(w, http.StatusOK, "messages-page", data)
		return
	}
	s.render(w, http.StatusOK, "source", data)
}

func containsID(ids []int64, id int64) bool {
	for _, x := range ids {
		if x == id {
			return true
		}
	}
	return false
}

// serviceOf is the service name of an account, such as "Telegram".
func (s *Server) serviceOf(accountID int64) (string, bool) {
	acc, err := s.deps.Store.Account(accountID)
	if err != nil {
		return "", false
	}
	if info, ok := s.deps.Registry.Info(acc.Connector); ok {
		return info.Title, true
	}
	return acc.Connector, true
}
