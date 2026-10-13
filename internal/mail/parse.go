// Package mail reads email messages for the archive: their headers, their
// readable text (turning HTML into text when there is nothing else), their
// attachments, and which conversation they belong to. It knows nothing of
// IMAP or storage.
package mail

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"mime"
	netmail "net/mail"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/emersion/go-message"
	_ "github.com/emersion/go-message/charset" // legacy charsets: KOI8-R, Shift_JIS, Windows-125x...
	gomail "github.com/emersion/go-message/mail"

	"github.com/fenelon/arc5/internal/model"
)

// Unreadable is the text of a message that could not be parsed at all. Its
// original is still kept.
const Unreadable = "This message could not be read"

// Part is an attached file or an inline image.
type Part struct {
	Path      string // its MIME part number, as IMAP writes it ("2", "1.2"): stable however the text is chosen
	Damaged   string // why its content could not be decoded, or ""
	FileName  string
	MimeType  string
	ContentID string // without angle brackets
	Inline    bool   // shown within the message rather than attached to it
	Data      []byte
}

// Parsed is what the archive needs from one message.
type Parsed struct {
	MessageID  string   // normalized; "" when the message has none
	References []string // normalized, oldest first
	InReplyTo  string   // normalized; the first one given
	Subject    string
	Date       time.Time // zero when missing or unreadable
	Addresses  []model.Address
	Text       string
	Parts      []Part
	Broken     bool // the body could not be read at all
}

// maxPartBytes stops one part from taking unbounded memory; a message's
// parts are already in memory as the original, so this only guards against
// decoding blowing it up.
const maxPartBytes = 200 << 20

var addressHeaders = []struct {
	role model.AddressRole
	key  string
}{
	{model.AddrFrom, "From"}, {model.AddrTo, "To"}, {model.AddrCc, "Cc"}, {model.AddrBcc, "Bcc"}, {model.AddrReplyTo, "Reply-To"},
}

// Parse reads a message. It never fails: what cannot be read is left out,
// and a message whose text cannot be read at all gets Unreadable as its text.
func Parse(raw []byte) Parsed {
	if len(bytes.TrimSpace(raw)) == 0 {
		return Parsed{Broken: true, Text: Unreadable}
	}
	e, err := message.Read(bytes.NewReader(raw))
	if e == nil || (err != nil && !message.IsUnknownCharset(err) && !message.IsUnknownEncoding(err)) {
		// A malformed header line stops the reader: read it again without them.
		e, err = message.Read(bytes.NewReader(withoutBadHeaderLines(raw)))
		if e == nil || (err != nil && !message.IsUnknownCharset(err) && !message.IsUnknownEncoding(err)) {
			return Parsed{Broken: true, Text: Unreadable}
		}
	}
	h := gomail.Header{Header: e.Header}
	p := Parsed{}
	if ids := messageIDs(h.Get("Message-Id")); len(ids) > 0 {
		p.MessageID = ids[0]
	}
	p.References = messageIDs(h.Get("References"))
	if ids := messageIDs(h.Get("In-Reply-To")); len(ids) > 0 {
		p.InReplyTo = ids[0]
	}
	p.Subject, _ = h.Subject() // the raw value when it cannot be decoded
	p.Subject = strings.Join(strings.Fields(p.Subject), " ")
	if d, err := h.Date(); err == nil {
		p.Date = d.UTC()
	}
	for _, ah := range addressHeaders {
		p.Addresses = append(p.Addresses, addresses(h, ah.key, ah.role)...)
	}

	w := &walker{}
	walkErr := w.entity(e, "", 0)
	p.Text = w.text()
	p.Parts = w.parts
	if p.Text == "" && walkErr != nil && len(p.Parts) == 0 {
		p.Broken, p.Text = true, Unreadable
	}
	return p
}

var headerField = regexp.MustCompile(`^[!-9;-~]+:`)

// withoutBadHeaderLines drops header lines that are neither a field nor the
// continuation of a kept one.
func withoutBadHeaderLines(raw []byte) []byte {
	end := bytes.Index(raw, []byte("\r\n\r\n"))
	sep := []byte("\r\n")
	if end < 0 {
		if end = bytes.Index(raw, []byte("\n\n")); end < 0 {
			return append([]byte("\r\n"), raw...) // no header at all: it is all body
		}
		sep = []byte("\n")
	}
	var out [][]byte
	keeping := false
	for _, line := range bytes.Split(raw[:end], sep) {
		switch {
		case len(line) > 0 && (line[0] == ' ' || line[0] == '\t'):
			if keeping {
				out = append(out, line)
			}
		case headerField.Match(line):
			out = append(out, line)
			keeping = true
		default:
			keeping = false
		}
	}
	return append(bytes.Join(out, sep), raw[end:]...)
}

var bracketed = regexp.MustCompile(`<([^<>\s]+)>`)

// messageIDs reads a Message-ID, References or In-Reply-To header loosely:
// every <...> in it, or the whole value when it has no brackets.
func messageIDs(v string) []string {
	var out []string
	for _, m := range bracketed.FindAllStringSubmatch(v, -1) {
		if id := NormalizeID(m[1]); id != "" {
			out = append(out, id)
		}
	}
	if len(out) == 0 {
		if v = strings.TrimSpace(v); v != "" && !strings.ContainsAny(v, " \t<>") {
			out = append(out, NormalizeID(v))
		}
	}
	return out
}

// HeaderID reads a Message-ID header's value as Parse does, so a message
// found by a headers-only fetch has the identity it will be stored under.
func HeaderID(value string) string {
	if ids := messageIDs(value); len(ids) > 0 {
		return ids[0]
	}
	return ""
}

// NormalizeID turns a Message-ID into the form the archive stores: no angle
// brackets or spaces, and the part after the last @ lower-cased, since domain
// names are not case-sensitive and some servers rewrite them.
func NormalizeID(id string) string {
	id = strings.Trim(strings.TrimSpace(id), "<>")
	if at := strings.LastIndex(id, "@"); at >= 0 {
		id = id[:at] + strings.ToLower(id[at:])
	}
	return id
}

// addresses reads one address header. A header the standard parser refuses
// is read address by address, keeping what can be read.
func addresses(h gomail.Header, key string, role model.AddressRole) []model.Address {
	var out []model.Address
	list, err := h.AddressList(key)
	if err == nil {
		for _, a := range list {
			out = append(out, model.Address{Role: role, Name: oneLine(a.Name), Address: a.Address})
		}
		return out
	}
	value, _ := h.Text(key)
	for _, piece := range strings.Split(value, ",") {
		if a, err := netmail.ParseAddress(strings.TrimSpace(piece)); err == nil {
			out = append(out, model.Address{Role: role, Name: oneLine(a.Name), Address: a.Address})
		}
	}
	return out
}

func oneLine(s string) string { return strings.Join(strings.Fields(s), " ") }

// walker collects a message's text and parts.
type walker struct {
	texts []textPart // in document order
	parts []Part
}

// textPart is a body part of the message's text: plain, or HTML to convert.
type textPart struct {
	plain   string
	html    []byte
	isHTML  bool
	charset string // for HTML: "utf-8" when the MIME header named a charset (go-message has converted it), else "" and the HTML's own <meta> decides
}

func (t textPart) String() string {
	if t.isHTML {
		return HTMLText(t.html, t.charset)
	}
	return strings.TrimSpace(t.plain)
}

func (w *walker) text() string {
	var out []string
	for _, t := range w.texts {
		if s := t.String(); s != "" {
			out = append(out, s)
		}
	}
	return strings.Join(out, "\n\n")
}

// isStub reports whether a plain part only says to read the HTML one, as
// some newsletters' do.
func isStub(plain string) bool {
	if utf8.RuneCountInString(plain) > 300 {
		return false
	}
	lower := strings.ToLower(plain)
	for _, hint := range []string{"html", "browser", "web version", "view this email", "view it online"} {
		if strings.Contains(lower, hint) {
			return true
		}
	}
	return false
}

const maxDepth = 20

var errTooDeep = errors.New("mail: parts nested too deep")

// childPath is the IMAP-style path of the n-th part (from 1) of a part.
func childPath(parent string, n int) string {
	if parent == "" {
		return strconv.Itoa(n)
	}
	return parent + "." + strconv.Itoa(n)
}

// damaged explains a part that could not be decoded.
const damaged = "this part of the message is damaged and could not be decoded"

// entity walks one MIME entity at path ("" for the message itself, whose
// single body is part "1"). It returns the first error met, after keeping
// everything it could read.
func (w *walker) entity(e *message.Entity, path string, depth int) error {
	if depth > maxDepth {
		return errTooDeep
	}
	mediaType, params, err := e.Header.ContentType()
	if err != nil || mediaType == "" {
		mediaType, params = "text/plain", map[string]string{}
	}
	if strings.HasPrefix(mediaType, "multipart/") {
		return w.multipart(e, mediaType, path, depth)
	}
	if path == "" {
		path = "1"
	}
	disposition, dparams, _ := e.Header.ContentDisposition()
	name := dparams["filename"]
	if name == "" {
		name = params["name"]
	}
	body, readErr := io.ReadAll(io.LimitReader(e.Body, maxPartBytes))
	if mediaType == "text/html" && disposition != "attachment" && name == "" {
		charset := ""
		if params["charset"] != "" && charsetKnown(params["charset"]) {
			charset = "utf-8"
		}
		w.texts = append(w.texts, textPart{html: body, isHTML: true, charset: charset})
		return readErr
	}
	if mediaType == "text/plain" && disposition != "attachment" && name == "" {
		text := strings.ReplaceAll(string(body), "\r\n", "\n")
		if !utf8.ValidString(text) {
			text = strings.ToValidUTF8(text, "\ufffd")
		}
		if strings.EqualFold(params["format"], "flowed") {
			text = unflow(text, strings.EqualFold(params["delsp"], "yes"))
		}
		w.texts = append(w.texts, textPart{plain: text})
		return readErr
	}
	switch mediaType {
	case "application/pgp-signature", "application/pkcs7-signature", "application/x-pkcs7-signature":
		return readErr // a signature is not something the owner opens
	}
	cid := strings.Trim(strings.TrimSpace(e.Header.Get("Content-Id")), "<>")
	if name == "" {
		name = w.nameFor(mediaType)
	}
	part := Part{
		Path: path, FileName: name, MimeType: mediaType, ContentID: cid,
		Inline: disposition == "inline" || (disposition == "" && cid != ""), Data: body,
	}
	if readErr != nil {
		part.Damaged = damaged
	}
	w.parts = append(w.parts, part)
	return nil // recorded as damaged; the walk goes on
}

// charsetKnown reports whether go-message converted a part in this charset
// to UTF-8.
func charsetKnown(name string) bool {
	_, err := message.CharsetReader(name, strings.NewReader(""))
	return err == nil
}

// nameFor makes up a file name for a part that has none.
func (w *walker) nameFor(mediaType string) string {
	if mediaType == "message/rfc822" {
		return "forwarded-message.eml"
	}
	ext := ""
	if exts, _ := mime.ExtensionsByType(mediaType); len(exts) > 0 {
		ext = exts[0]
		for _, preferred := range []string{".jpg", ".png", ".gif", ".pdf", ".txt", ".html", ".ics"} {
			for _, x := range exts {
				if x == preferred {
					ext = x
				}
			}
		}
	}
	kind := "file"
	if strings.HasPrefix(mediaType, "image/") {
		kind = "image"
	}
	return fmt.Sprintf("%s-%d%s", kind, len(w.parts)+1, ext)
}

// multipart walks the parts of a multipart entity. In multipart/alternative
// one alternative gives the text: the plain one unless it is empty or only
// says to read the HTML. Elsewhere every text part counts, in order. Files
// from every part are kept. A part that fails is passed over; only a part
// list that cannot be read further ends the walk.
func (w *walker) multipart(e *message.Entity, mediaType, path string, depth int) error {
	mr := e.MultipartReader()
	if mr == nil {
		return errors.New("mail: multipart without a boundary")
	}
	var firstErr error
	keep := func(err error) {
		if err != nil && firstErr == nil {
			firstErr = err
		}
	}
	var choices []*walker
	for n := 1; ; n++ {
		part, err := mr.NextPart()
		if err == io.EOF {
			break
		}
		if part == nil {
			keep(err)
			break
		}
		c := w
		if mediaType == "multipart/alternative" {
			c = &walker{parts: w.parts}
		}
		keep(c.entity(part, childPath(path, n), depth+1))
		if mediaType == "multipart/alternative" {
			w.parts = c.parts
			choices = append(choices, c)
		}
	}
	if mediaType != "multipart/alternative" {
		return firstErr
	}
	var plain, html *walker
	for _, c := range choices {
		hasHTML, plainText := false, ""
		for _, t := range c.texts {
			if t.isHTML {
				hasHTML = true
			} else {
				plainText += t.plain
			}
		}
		if plain == nil && !hasHTML && strings.TrimSpace(plainText) != "" && !isStub(strings.TrimSpace(plainText)) {
			plain = c
		}
		if html == nil && hasHTML {
			html = c
		}
	}
	switch {
	case plain != nil:
		w.texts = append(w.texts, plain.texts...)
	case html != nil:
		w.texts = append(w.texts, html.texts...)
	case len(choices) > 0:
		w.texts = append(w.texts, choices[0].texts...)
	}
	return firstErr
}

// unflow joins the lines of a format=flowed part (RFC 3676): a line ending
// in a space continues on the next one.
func unflow(text string, delsp bool) string {
	var b strings.Builder
	lines := strings.Split(text, "\n")
	for i, line := range lines {
		if line != "-- " && strings.HasSuffix(line, " ") && i < len(lines)-1 {
			if delsp {
				line = strings.TrimSuffix(line, " ")
			}
			b.WriteString(line)
			continue
		}
		b.WriteString(line)
		if i < len(lines)-1 {
			b.WriteByte('\n')
		}
	}
	return b.String()
}
