package telegram

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/gotd/td/tg"

	"github.com/fenelon/arc5/internal/model"
)

// viewOnce is the self-destruct time Telegram gives view-once media.
const viewOnce = 0x7FFFFFFF

// reasonEphemeral explains a self-destructing file left alone.
const reasonEphemeral = "self-destructing media is not archived"

// entities are the users and chats a Telegram response refers to by ID.
type entities struct {
	users    map[int64]*tg.User
	chats    map[int64]*tg.Chat
	channels map[int64]*tg.Channel
	titles   map[string]string // chats and channels the account can no longer see, by source ID
}

func newEntities(users []tg.UserClass, chats []tg.ChatClass) *entities {
	e := &entities{users: map[int64]*tg.User{}, chats: map[int64]*tg.Chat{}, channels: map[int64]*tg.Channel{}, titles: map[string]string{}}
	e.add(users, chats)
	return e
}

// add takes in the users and chats of another response.
func (e *entities) add(users []tg.UserClass, chats []tg.ChatClass) {
	for _, u := range users {
		if u, ok := u.(*tg.User); ok {
			e.users[u.ID] = u
		}
	}
	for _, c := range chats {
		switch c := c.(type) {
		case *tg.Chat:
			e.chats[c.ID] = c
		case *tg.ChatForbidden:
			e.titles[fmt.Sprintf("chat:%d", c.ID)] = c.Title
		case *tg.Channel:
			e.channels[c.ID] = c
		case *tg.ChannelForbidden:
			e.titles[fmt.Sprintf("channel:%d", c.ID)] = c.Title
		}
	}
}

// userName is how a person is called: their name, else their username.
func userName(u *tg.User) string {
	if u.Deleted {
		return "Deleted account"
	}
	if name := strings.TrimSpace(u.FirstName + " " + u.LastName); name != "" {
		return name
	}
	if u.Username != "" {
		return "@" + u.Username
	}
	return "User " + strconv.FormatInt(u.ID, 10)
}

// name is what a user, group or channel is called, or "" if unknown.
func (e *entities) name(p tg.PeerClass) string {
	switch p := p.(type) {
	case *tg.PeerUser:
		if u := e.users[p.UserID]; u != nil {
			return userName(u)
		}
	case *tg.PeerChat:
		if c := e.chats[p.ChatID]; c != nil {
			return c.Title
		}
	case *tg.PeerChannel:
		if c := e.channels[p.ChannelID]; c != nil {
			return c.Title
		}
	}
	return e.titles[peerSourceID(p)]
}

// chat describes the chat with a peer. selfID is the account's own user,
// whose chat with itself is Saved Messages.
func (e *entities) chat(p tg.PeerClass, selfID int64) (model.Chat, bool) {
	c := model.Chat{SourceID: peerSourceID(p), Title: e.name(p)}
	switch p := p.(type) {
	case *tg.PeerUser:
		u := e.users[p.UserID]
		switch {
		case u == nil:
			return model.Chat{}, false
		case u.ID == selfID:
			c.Kind, c.Title = model.ChatPrivate, "Saved Messages"
		case u.Bot:
			c.Kind = model.ChatBot
		default:
			c.Kind = model.ChatPrivate
		}
	case *tg.PeerChat:
		c.Kind = model.ChatGroup
	case *tg.PeerChannel:
		c.Kind = model.ChatChannel
		if ch := e.channels[p.ChannelID]; ch != nil && ch.Megagroup {
			c.Kind = model.ChatGroup
		}
	}
	if c.Title == "" {
		return model.Chat{}, false
	}
	return c, true
}

// sender describes who wrote a message: a person, or a group or channel
// writing as itself.
func (e *entities) sender(p tg.PeerClass) (model.Sender, bool) {
	s := model.Sender{SourceID: peerSourceID(p), DisplayName: e.name(p)}
	if p, ok := p.(*tg.PeerUser); ok {
		if u := e.users[p.UserID]; u != nil {
			s.Username = u.Username
		}
	}
	return s, s.SourceID != "" && s.DisplayName != ""
}

// mediaItem is a file to download for a message's attachment.
type mediaItem struct {
	Position  int
	Location  tg.InputFileLocationClass
	Size      int64
	DC        int
	Ephemeral bool
	live      bool // found live or asked for, so not paced like the backfill
}

// mapMessage turns a Telegram message into the archive's terms, with the
// files to download. keepEphemeral says whether self-destructing media is
// downloaded; when it is not, it is recorded without its file.
func mapMessage(msg tg.MessageClass, e *entities, keepEphemeral bool) (model.Message, []mediaItem, bool) {
	switch m := msg.(type) {
	case *tg.Message:
		out := model.Message{
			ChatSourceID: peerSourceID(m.PeerID), SourceID: strconv.Itoa(m.ID), Kind: model.KindMessage, Outgoing: m.Out,
			SenderSourceID: senderOf(m.Out, m.FromID, m.PeerID), SentAt: time.Unix(int64(m.Date), 0).UTC(),
			Text: m.Message, Raw: rawJSON(m),
		}
		if r, ok := m.ReplyTo.(*tg.MessageReplyHeader); ok && r.ReplyToMsgID != 0 && r.ReplyToPeerID == nil {
			out.ReplyToSourceID = strconv.Itoa(r.ReplyToMsgID)
		}
		if f, ok := m.GetFwdFrom(); ok {
			out.ForwardedFrom = f.FromName
			if out.ForwardedFrom == "" && f.FromID != nil {
				out.ForwardedFrom = e.name(f.FromID)
			}
			if out.ForwardedFrom == "" {
				out.ForwardedFrom = "Unknown"
			}
		}
		if m.EditDate != 0 && !m.EditHide {
			out.EditedAt = time.Unix(int64(m.EditDate), 0).UTC()
		}
		var media []mediaItem
		if a, item, ok := mapMedia(m.Media, keepEphemeral); ok {
			out.Attachments = []model.Attachment{a}
			if item.Location != nil {
				media = append(media, item)
			}
		} else if line := mediaText(m.Media); line != "" {
			out.Text = strings.TrimSpace(out.Text + "\n" + line)
		}
		return out, media, out.ChatSourceID != ""
	case *tg.MessageService:
		actor := "You"
		if !m.Out {
			actor = e.name(m.FromID)
			if actor == "" {
				actor = e.name(m.PeerID)
			}
		}
		out := model.Message{
			ChatSourceID: peerSourceID(m.PeerID), SourceID: strconv.Itoa(m.ID), Kind: model.KindService, Outgoing: m.Out,
			SenderSourceID: senderOf(m.Out, m.FromID, m.PeerID), SentAt: time.Unix(int64(m.Date), 0).UTC(),
			Text: serviceText(m.Action, actor, m.FromID, e), Raw: rawJSON(m),
		}
		return out, nil, out.ChatSourceID != ""
	}
	return model.Message{}, nil, false
}

// senderOf names who wrote a message: nobody for the owner, the author when
// Telegram names one, otherwise the chat itself (the other person in a
// private chat, or a channel posting).
func senderOf(out bool, from, peer tg.PeerClass) string {
	switch {
	case out:
		return ""
	case from != nil:
		return peerSourceID(from)
	}
	return peerSourceID(peer)
}

// mapMedia describes a message's photo or file. The item's Location is nil
// when the file is not to be downloaded.
func mapMedia(media tg.MessageMediaClass, keepEphemeral bool) (model.Attachment, mediaItem, bool) {
	var (
		a    model.Attachment
		item mediaItem
		ttl  int
	)
	switch m := media.(type) {
	case *tg.MessageMediaPhoto:
		photo, ok := m.Photo.(*tg.Photo)
		if !ok {
			return a, item, false
		}
		thumb, size := largestSize(photo.Sizes)
		a = model.Attachment{Kind: model.AttachPhoto, MimeType: "image/jpeg", Size: size, SourceID: fmt.Sprintf("photo:%d", photo.ID)}
		item = mediaItem{Size: size, DC: photo.DCID, Location: &tg.InputPhotoFileLocation{
			ID: photo.ID, AccessHash: photo.AccessHash, FileReference: photo.FileReference, ThumbSize: thumb}}
		ttl = m.TTLSeconds
	case *tg.MessageMediaDocument:
		doc, ok := m.Document.(*tg.Document)
		if !ok {
			return a, item, false
		}
		a = model.Attachment{Kind: documentKind(doc, m), MimeType: doc.MimeType, Size: doc.Size, SourceID: fmt.Sprintf("document:%d", doc.ID)}
		for _, attr := range doc.Attributes {
			if f, ok := attr.(*tg.DocumentAttributeFilename); ok {
				a.FileName = f.FileName
			}
		}
		item = mediaItem{Size: doc.Size, DC: doc.DCID, Location: &tg.InputDocumentFileLocation{
			ID: doc.ID, AccessHash: doc.AccessHash, FileReference: doc.FileReference}}
		ttl = m.TTLSeconds
	default:
		return a, item, false
	}
	if ttl > 0 {
		a.Ephemeral, item.Ephemeral = true, true
		if !keepEphemeral {
			a.NoFetch, a.NoFetchReason, item.Location = true, reasonEphemeral, nil
		}
	}
	return a, item, true
}

// largestSize finds a photo's biggest stored size: its type letter and bytes.
func largestSize(sizes []tg.PhotoSizeClass) (string, int64) {
	var (
		thumb string
		best  int
	)
	for _, s := range sizes {
		switch s := s.(type) {
		case *tg.PhotoSize:
			if s.Size > best {
				thumb, best = s.Type, s.Size
			}
		case *tg.PhotoSizeProgressive:
			if n := len(s.Sizes); n > 0 && s.Sizes[n-1] > best {
				thumb, best = s.Type, s.Sizes[n-1]
			}
		}
	}
	return thumb, int64(best)
}

func documentKind(doc *tg.Document, m *tg.MessageMediaDocument) model.AttachmentKind {
	kind := model.AttachDocument
	for _, attr := range doc.Attributes {
		switch attr := attr.(type) {
		case *tg.DocumentAttributeSticker:
			return model.AttachSticker
		case *tg.DocumentAttributeAudio:
			if attr.Voice {
				return model.AttachVoice
			}
			kind = model.AttachAudio
		case *tg.DocumentAttributeVideo:
			kind = model.AttachVideo
		}
	}
	if m.Voice {
		return model.AttachVoice
	}
	if m.Video || m.Round {
		return model.AttachVideo
	}
	return kind
}

// mediaText is a line of text standing for media that has no file.
func mediaText(media tg.MessageMediaClass) string {
	switch m := media.(type) {
	case *tg.MessageMediaGeo:
		if g, ok := m.Geo.(*tg.GeoPoint); ok {
			return fmt.Sprintf("Location: %.5f, %.5f", g.Lat, g.Long)
		}
	case *tg.MessageMediaVenue:
		return "Place: " + strings.Join(nonEmpty(m.Title, m.Address), ", ")
	case *tg.MessageMediaContact:
		return "Contact: " + strings.Join(nonEmpty(strings.TrimSpace(m.FirstName+" "+m.LastName), m.PhoneNumber), ", ")
	case *tg.MessageMediaPoll:
		return "Poll: " + m.Poll.Question.Text
	}
	return ""
}

func nonEmpty(parts ...string) []string {
	var out []string
	for _, p := range parts {
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

// serviceText is a short line saying what a service message records.
func serviceText(action tg.MessageActionClass, actor string, from tg.PeerClass, e *entities) string {
	userNames := func(ids []int64) string {
		names := make([]string, len(ids))
		for i, id := range ids {
			names[i] = e.name(&tg.PeerUser{UserID: id})
		}
		if len(names) < 2 {
			return strings.Join(names, "")
		}
		return strings.Join(names[:len(names)-1], ", ") + " and " + names[len(names)-1]
	}
	self := func(id int64) bool {
		u, ok := from.(*tg.PeerUser)
		return ok && u.UserID == id
	}
	switch a := action.(type) {
	case *tg.MessageActionPinMessage:
		return actor + " pinned a message"
	case *tg.MessageActionChatCreate:
		return actor + " created the group “" + a.Title + "”"
	case *tg.MessageActionChannelCreate:
		return actor + " created “" + a.Title + "”"
	case *tg.MessageActionChatEditTitle:
		return actor + " renamed the chat to “" + a.Title + "”"
	case *tg.MessageActionChatEditPhoto:
		return actor + " changed the chat's picture"
	case *tg.MessageActionChatDeletePhoto:
		return actor + " removed the chat's picture"
	case *tg.MessageActionChatAddUser:
		if len(a.Users) == 1 && self(a.Users[0]) {
			return actor + " joined"
		}
		return actor + " added " + userNames(a.Users)
	case *tg.MessageActionChatDeleteUser:
		if self(a.UserID) {
			return actor + " left"
		}
		return actor + " removed " + userNames([]int64{a.UserID})
	case *tg.MessageActionChatJoinedByLink:
		return actor + " joined by invite link"
	case *tg.MessageActionChatJoinedByRequest:
		return actor + " joined"
	case *tg.MessageActionPhoneCall:
		call := "call"
		if a.Video {
			call = "video call"
		}
		switch a.Reason.(type) {
		case *tg.PhoneCallDiscardReasonMissed:
			return "Missed " + call
		case *tg.PhoneCallDiscardReasonBusy:
			return "Declined " + call
		}
		return strings.ToUpper(call[:1]) + call[1:] + duration(a.Duration)
	case *tg.MessageActionGroupCall:
		return "Group call" + duration(a.Duration)
	case *tg.MessageActionHistoryClear:
		return "History cleared"
	case *tg.MessageActionScreenshotTaken:
		return actor + " took a screenshot"
	case *tg.MessageActionSetMessagesTTL:
		if a.Period == 0 {
			return actor + " turned off auto-delete"
		}
		return actor + " set messages to delete after " + period(a.Period)
	case *tg.MessageActionChatMigrateTo:
		return "This group became a supergroup"
	case *tg.MessageActionChannelMigrateFrom:
		return "Upgraded from the group “" + a.Title + "”"
	case *tg.MessageActionContactSignUp:
		return actor + " joined Telegram"
	case *tg.MessageActionCustomAction:
		return a.Message
	}
	return "Service message"
}

// duration is ", 2 min 5 s" for a call that lasted, or "".
func duration(seconds int) string {
	switch {
	case seconds <= 0:
		return ""
	case seconds < 60:
		return fmt.Sprintf(", %d s", seconds)
	case seconds%60 == 0:
		return fmt.Sprintf(", %d min", seconds/60)
	}
	return fmt.Sprintf(", %d min %d s", seconds/60, seconds%60)
}

// period says an auto-delete time in words.
func period(seconds int) string {
	for _, u := range []struct {
		size       int
		one, other string
	}{{365 * 86400, "year", "years"}, {30 * 86400, "month", "months"}, {7 * 86400, "week", "weeks"}, {86400, "day", "days"}, {3600, "hour", "hours"}, {60, "minute", "minutes"}} {
		if seconds >= u.size && seconds%u.size == 0 {
			if n := seconds / u.size; n != 1 {
				return fmt.Sprintf("%d %s", n, u.other)
			}
			return "1 " + u.one
		}
	}
	return fmt.Sprintf("%d seconds", seconds)
}
