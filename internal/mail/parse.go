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
	e, err := message.Read(bytes.NewReader(raw))
	if len(bytes.TrimSpace(raw)) == 0 || e == nil || (err != nil && !message.IsUnknownCharset(err) && !message.IsUnknownEncoding(err)) {
		return Parsed{Broken: true, Text: Unreadable}
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
	walkErr := w.entity(e, 0)
	p.Text = w.text()
	p.Parts = w.parts
	if p.Text == "" && walkErr != nil && len(p.Parts) == 0 {
		p.Broken, p.Text = true, Unreadable
	}
	return p
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
	plain []string
	html  []htmlBody
	parts []Part
}

// htmlBody is an HTML part. charset is "utf-8" when the MIME header named a
// charset (go-message has already converted it), else "", and the HTML's own
// <meta> decides.
type htmlBody struct {
	data    []byte
	charset string
}

func (w *walker) text() string {
	plain := strings.TrimSpace(strings.Join(w.plain, "\n\n"))
	if len(w.html) > 0 && (plain == "" || isStub(plain)) {
		var texts []string
		for _, h := range w.html {
			if t := HTMLText(h.data, h.charset); t != "" {
				texts = append(texts, t)
			}
		}
		return strings.Join(texts, "\n\n")
	}
	return plain
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

// entity walks one MIME entity. It returns the first error met, after
// keeping everything it could read.
func (w *walker) entity(e *message.Entity, depth int) error {
	if depth > maxDepth {
		return errTooDeep
	}
	mediaType, params, err := e.Header.ContentType()
	if err != nil || mediaType == "" {
		mediaType, params = "text/plain", map[string]string{}
	}
	if strings.HasPrefix(mediaType, "multipart/") {
		return w.multipart(e, mediaType, depth)
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
		w.html = append(w.html, htmlBody{data: body, charset: charset})
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
		w.plain = append(w.plain, text)
		return readErr
	}
	switch mediaType {
	case "application/pgp-signature", "application/pkcs7-signature", "application/x-pkcs7-signature":
		return readErr // a signature is not something the owner opens
	}
	cid := strings.Trim(strings.TrimSpace(e.Header.Get("Content-Id")), "<>")
	if name == "" {
		name = w.nameFor(mediaType, e)
	}
	w.parts = append(w.parts, Part{
		FileName: name, MimeType: mediaType, ContentID: cid,
		Inline: disposition == "inline" || (disposition == "" && cid != ""), Data: body,
	})
	return readErr
}

// charsetKnown reports whether go-message converted a part in this charset
// to UTF-8.
func charsetKnown(name string) bool {
	_, err := message.CharsetReader(name, strings.NewReader(""))
	return err == nil
}

// nameFor makes up a file name for a part that has none.
func (w *walker) nameFor(mediaType string, e *message.Entity) string {
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

func (w *walker) multipart(e *message.Entity, mediaType string, depth int) error {
	mr := e.MultipartReader()
	if mr == nil {
		return errors.New("mail: multipart without a boundary")
	}
	if mediaType != "multipart/alternative" {
		for {
			part, err := mr.NextPart()
			if err == io.EOF {
				return nil
			}
			if part == nil {
				return err
			}
			if err := w.entity(part, depth+1); err != nil {
				return err
			}
		}
	}
	// One of the alternatives gives the text: the plain one unless it is
	// empty or a stub. Files from every alternative are kept.
	var choices []*walker
	var firstErr error
	for {
		part, err := mr.NextPart()
		if err == io.EOF {
			break
		}
		if part == nil {
			firstErr = err
			break
		}
		c := &walker{}
		if err := c.entity(part, depth+1); err != nil && firstErr == nil {
			firstErr = err
		}
		choices = append(choices, c)
		w.parts = append(w.parts, c.parts...)
	}
	var plain, html *walker
	for _, c := range choices {
		joined := strings.TrimSpace(strings.Join(c.plain, ""))
		if plain == nil && joined != "" && !isStub(joined) {
			plain = c
		}
		if html == nil && len(c.html) > 0 {
			html = c
		}
	}
	switch {
	case plain != nil:
		w.plain = append(w.plain, plain.plain...)
	case html != nil:
		w.html = append(w.html, html.html...)
	case len(choices) > 0:
		w.plain = append(w.plain, choices[0].plain...)
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
