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
		a := model.Attachment{
			Kind: AttachmentKind(part.MimeType), FileName: part.FileName, MimeType: part.MimeType, Size: int64(len(part.Data)),
			SourceID: PartSourceID(part.Path),
		}
		if part.Damaged != "" {
			a.NoFetch, a.NoFetchReason = true, part.Damaged
		}
		m.Attachments = append(m.Attachments, a)
	}
	return m
}

// PartSourceID is the attachment source ID of a MIME part.
func PartSourceID(path string) string { return "part:" + path }

// Part finds a file by its attachment source ID, or, for one stored
// without, by its position.
func (p Parsed) Part(sourceID string, position int) (Part, bool) {
	if sourceID != "" {
		for _, part := range p.Parts {
			if PartSourceID(part.Path) == sourceID {
				return part, true
			}
		}
		return Part{}, false
	}
	if position >= 0 && position < len(p.Parts) {
		return p.Parts[position], true
	}
	return Part{}, false
}
