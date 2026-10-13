package email

import (
	"bytes"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/fenelon/arc5/internal/connector"
	"github.com/fenelon/arc5/internal/mail"
	"github.com/fenelon/arc5/internal/model"
)

// Name is the connector name stored on email accounts.
const Name = "email"

// noSubject titles a conversation whose first message has no subject.
const noSubject = "(no subject)"

var threadKinds = []model.ChatKind{model.ChatThread}

// Store archives one message as received: its conversation, the people it
// names, the message with its original, and its attachments under the size
// limit. It returns where the message is filed. A message already archived
// (by its identity) is left as it is. own reports whether a lower-cased
// address is the mailbox's; received stands in for a missing Date.
func Store(sink connector.MailSink, raw []byte, received time.Time, own func(address string) bool) (chat, id string, err error) {
	p := mail.Parse(raw)
	id = mail.Identity(p, raw)
	if chats, err := sink.LookupChats(id, threadKinds); err != nil || len(chats) > 0 {
		if err != nil {
			return "", "", err
		}
		return chats[0], id, nil
	}
	var lookupErr error
	chat = mail.ThreadRoot(p, id, func(parent string) (string, bool) {
		chats, err := sink.LookupChats(parent, threadKinds)
		if err != nil {
			lookupErr = err
		}
		if len(chats) == 0 {
			return "", false
		}
		return chats[0], true
	})
	if lookupErr != nil {
		return "", "", lookupErr
	}
	// A conversation is titled by the message that started it in the
	// archive; later replies do not rename it.
	if known, err := sink.ChatArchived(chat); err != nil {
		return "", "", err
	} else if !known {
		title := mail.CleanSubject(p.Subject)
		if title == "" {
			title = noSubject
		}
		if err := sink.UpsertChat(model.Chat{SourceID: chat, Kind: model.ChatThread, Title: title}); err != nil {
			return "", "", err
		}
	}
	for _, a := range p.Addresses {
		if a.Role == model.AddrBcc || a.Role == model.AddrReplyTo || a.Address == "" {
			continue
		}
		// The archive keeps a name it knows when a message gives none.
		if err := sink.UpsertSender(model.Sender{SourceID: strings.ToLower(a.Address), DisplayName: a.Name, Username: a.Address}); err != nil {
			return "", "", err
		}
	}
	m := p.Message(chat, id, own, received)
	m.Original = raw
	pending, err := sink.UpsertMessage(m)
	if err != nil {
		return "", "", err
	}
	for _, ref := range pending {
		if part, ok := p.Part(ref.SourceID, ref.Position); ok && part.Damaged == "" {
			if err := sink.PutMedia(ref, bytes.NewReader(part.Data)); err != nil {
				return "", "", err
			}
		}
	}
	return chat, id, nil
}

// FetchFromOriginal answers "Fetch now" for a mail attachment: the file is
// taken out of the kept original, without asking the server.
func FetchFromOriginal(sink connector.MailSink, ref model.AttachmentRef) error {
	r, err := sink.Original(ref.ChatSourceID, ref.MessageSourceID)
	if err != nil {
		return err
	}
	raw, err := io.ReadAll(r)
	r.Close()
	if err != nil {
		return fmt.Errorf("%w: %v", connector.ErrStorage, err)
	}
	part, ok := mail.Parse(raw).Part(ref.SourceID, ref.Position)
	switch {
	case !ok:
		return sink.SetAttachmentStatus(ref, model.StatusUnavailable, "the original message does not hold this file")
	case part.Damaged != "":
		return sink.SetAttachmentStatus(ref, model.StatusUnavailable, part.Damaged)
	}
	return sink.PutMedia(ref, bytes.NewReader(part.Data))
}
