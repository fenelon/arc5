package mail

import (
	"strings"
	"time"

	"github.com/fenelon/arc5/internal/model"
)

// AttachmentKind is the archive's kind for a file of this type.
func AttachmentKind(mimeType string) model.AttachmentKind {
	switch t := strings.ToLower(mimeType); {
	case strings.HasPrefix(t, "image/"):
		return model.AttachPhoto
	case strings.HasPrefix(t, "video/"):
		return model.AttachVideo
	case strings.HasPrefix(t, "audio/"):
		return model.AttachAudio
	}
	return model.AttachDocument
}

// From is the message's first From address, lower-cased, or "".
func (p Parsed) From() string {
	for _, a := range p.Addresses {
		if a.Role == model.AddrFrom {
			return strings.ToLower(a.Address)
		}
	}
	return ""
}

// Message is the archive's form of a parsed message, filed in conversation
// chatSourceID under sourceID. own reports whether a lower-cased address is
// the mailbox's own, which makes the message outgoing. received stands in
// for a missing Date. The caller adds the original.
func (p Parsed) Message(chatSourceID, sourceID string, own func(address string) bool, received time.Time) model.Message {
	from := p.From()
	sent := p.Date
	if sent.IsZero() {
		sent = received
	}
	m := model.Message{
		ChatSourceID: chatSourceID, SourceID: sourceID, SenderSourceID: from, SentAt: sent,
		Text: p.Text, Subject: p.Subject, Addresses: p.Addresses, ReplyToSourceID: p.InReplyTo,
		Outgoing: from != "" && own(from),
	}
	for _, part := range p.Parts {
		m.Attachments = append(m.Attachments, model.Attachment{
			Kind: AttachmentKind(part.MimeType), FileName: part.FileName, MimeType: part.MimeType, Size: int64(len(part.Data)),
		})
	}
	return m
}
