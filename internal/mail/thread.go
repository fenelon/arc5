package mail

import (
	"crypto/sha256"
	"encoding/hex"
	"regexp"
	"strings"
)

// Identity is a message's source ID in the archive: its Message-ID, or, for
// a message without one, a hash of its From, Date, Subject and first
// kilobyte of text, so the same message found in two folders, even with a
// header a server added, is one message.
func Identity(p Parsed, raw []byte) string {
	if p.MessageID != "" {
		return p.MessageID
	}
	h := sha256.New()
	from := ""
	for _, a := range p.Addresses {
		if a.Role == "from" {
			from = a.Address
			break
		}
	}
	date := ""
	if !p.Date.IsZero() {
		date = p.Date.UTC().Format("2006-01-02T15:04:05Z")
	}
	body := p.Text
	if p.Broken {
		body = string(raw)
	}
	if len(body) > 1024 {
		body = body[:1024]
	}
	for _, field := range []string{from, date, p.Subject, body} {
		h.Write([]byte(field))
		h.Write([]byte{0})
	}
	return "sha256:" + hex.EncodeToString(h.Sum(nil))
}

// ThreadRoot is the source ID of the conversation a message belongs to,
// named by its first message: the first of its References, else the
// conversation the message it replies to is filed under (known looks that
// up by Message-ID), else that message, else itself (identity).
func ThreadRoot(p Parsed, identity string, known func(messageID string) (thread string, ok bool)) string {
	if len(p.References) > 0 {
		return "thread:" + p.References[0]
	}
	if p.InReplyTo != "" {
		if thread, ok := known(p.InReplyTo); ok {
			return thread
		}
		return "thread:" + p.InReplyTo
	}
	return "thread:" + identity
}

// replyPrefix matches the prefixes mail programs put before a subject when
// replying or forwarding, in the languages they are most often seen in.
var replyPrefix = regexp.MustCompile(`(?i)^\s*(re|fwd?|aw|wg|sv|vs|antw|tr|rif|r|odp|réf|ref|ответ|отв|пер|回复|回覆|答复|転送|返信)\s*(\[\d+\]|\(\d+\))?\s*[:：]\s*`)

// CleanSubject is a conversation's title: its subject without "Re:",
// "Fwd:" and their kin, however many there are.
func CleanSubject(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	for {
		loc := replyPrefix.FindStringIndex(s)
		if loc == nil {
			return s
		}
		s = strings.TrimSpace(s[loc[1]:])
	}
}
