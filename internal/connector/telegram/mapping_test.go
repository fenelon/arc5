package telegram

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/gotd/td/tg"

	"github.com/fenelon/arc5/internal/model"
)

const self = 1000

var when = time.Date(2026, time.October, 1, 12, 0, 0, 0, time.UTC)

// fixture holds the people and chats the tests' messages refer to.
func fixture() *entities {
	return newEntities([]tg.UserClass{
		&tg.User{ID: self, Self: true, FirstName: "Me", AccessHash: 1},
		&tg.User{ID: 1, FirstName: "Dan", LastName: "Whitfield", Username: "danw", AccessHash: 11, Photo: &tg.UserProfilePhoto{PhotoID: 501}},
		&tg.User{ID: 2, Username: "marina_s", AccessHash: 12},
		&tg.User{ID: 3, Deleted: true},
		&tg.User{ID: 4, FirstName: "Weather", Bot: true, AccessHash: 14},
	}, []tg.ChatClass{
		&tg.Chat{ID: 10, Title: "Familia", Photo: &tg.ChatPhoto{PhotoID: 610}},
		&tg.ChatForbidden{ID: 11, Title: "Old club"},
		&tg.Channel{ID: 20, Title: "Походы", Megagroup: true, AccessHash: 21},
		&tg.Channel{ID: 21, Title: "News", Broadcast: true, AccessHash: 22},
		&tg.ChannelForbidden{ID: 22, Title: "Closed", Broadcast: true},
	})
}

func TestSourceIDs(t *testing.T) {
	for _, c := range []struct {
		peer tg.PeerClass
		want string
	}{
		{&tg.PeerUser{UserID: 7}, "user:7"},
		{&tg.PeerChat{ChatID: 8}, "chat:8"},
		{&tg.PeerChannel{ChannelID: 9}, "channel:9"},
		{nil, ""},
	} {
		got := peerSourceID(c.peer)
		if got != c.want {
			t.Errorf("peerSourceID(%v) = %q, want %q", c.peer, got, c.want)
		}
		if c.want == "" {
			continue
		}
		if peer, ok := sourcePeer(got); !ok || peerSourceID(peer) != got {
			t.Errorf("sourcePeer(%q) = %v, %v", got, peer, ok)
		}
	}
	for _, bad := range []string{"", "user:", "user:x", "group:1", "user:-1", "user:1:2", "chat"} {
		if _, ok := sourcePeer(bad); ok {
			t.Errorf("sourcePeer(%q) accepted", bad)
		}
	}
}

func TestMapChat(t *testing.T) {
	e := fixture()
	for _, c := range []struct {
		peer  tg.PeerClass
		kind  model.ChatKind
		title string
	}{
		{&tg.PeerUser{UserID: 1}, model.ChatPrivate, "Dan Whitfield"},
		{&tg.PeerUser{UserID: 2}, model.ChatPrivate, "@marina_s"},
		{&tg.PeerUser{UserID: 3}, model.ChatPrivate, "Deleted account"},
		{&tg.PeerUser{UserID: 4}, model.ChatBot, "Weather"},
		{&tg.PeerUser{UserID: self}, model.ChatPrivate, "Saved Messages"},
		{&tg.PeerChat{ChatID: 10}, model.ChatGroup, "Familia"},
		{&tg.PeerChat{ChatID: 11}, model.ChatGroup, "Old club"},
		{&tg.PeerChannel{ChannelID: 20}, model.ChatGroup, "Походы"},
		{&tg.PeerChannel{ChannelID: 21}, model.ChatChannel, "News"},
		{&tg.PeerChannel{ChannelID: 22}, model.ChatChannel, "Closed"},
	} {
		got, ok := e.chat(c.peer, self)
		if !ok || got.Kind != c.kind || got.Title != c.title || got.SourceID != peerSourceID(c.peer) {
			t.Errorf("chat(%s) = %+v, %v; want %s %q", peerSourceID(c.peer), got, ok, c.kind, c.title)
		}
	}
	if _, ok := e.chat(&tg.PeerUser{UserID: 99}, self); ok {
		t.Error("an unknown user became a chat")
	}
}

func TestMapSender(t *testing.T) {
	e := fixture()
	if s, ok := e.sender(&tg.PeerUser{UserID: 1}); !ok || s != (model.Sender{SourceID: "user:1", DisplayName: "Dan Whitfield", Username: "danw"}) {
		t.Errorf("Dan = %+v, %v", s, ok)
	}
	if s, ok := e.sender(&tg.PeerChannel{ChannelID: 21}); !ok || s.DisplayName != "News" || s.SourceID != "channel:21" {
		t.Errorf("a channel signing as itself = %+v, %v", s, ok)
	}
	if _, ok := e.sender(&tg.PeerUser{UserID: 99}); ok {
		t.Error("an unknown user became a sender")
	}
}

func TestMapTextMessages(t *testing.T) {
	e := fixture()
	incoming := &tg.Message{ID: 5, PeerID: &tg.PeerUser{UserID: 1}, Date: int(when.Unix()), Message: "Are we still on?"}
	m, media, ok := mapMessage(incoming, e, false)
	want := model.Message{ChatSourceID: "user:1", SourceID: "5", SenderSourceID: "user:1", SentAt: when, Text: "Are we still on?"}
	if !ok || len(media) != 0 || m.ChatSourceID != want.ChatSourceID || m.SourceID != want.SourceID || m.SenderSourceID != want.SenderSourceID ||
		!m.SentAt.Equal(want.SentAt) || m.Text != want.Text || m.Outgoing || m.Kind != model.KindMessage {
		t.Errorf("incoming = %+v", m)
	}
	var raw map[string]any
	if err := json.Unmarshal(m.Raw, &raw); err != nil || raw["_"] != "message" || raw["message"] != "Are we still on?" {
		t.Errorf("raw = %s, %v", m.Raw, err)
	}
	if peer, _ := raw["peer_id"].(map[string]any); peer["_"] != "peerUser" {
		t.Errorf("raw keeps the type of nested objects: %s", m.Raw)
	}

	out := &tg.Message{ID: 6, Out: true, PeerID: &tg.PeerUser{UserID: 1}, FromID: &tg.PeerUser{UserID: self}, Date: int(when.Unix()), Message: "Yes",
		ReplyTo: &tg.MessageReplyHeader{ReplyToMsgID: 5}, EditDate: int(when.Unix()) + 60}
	out.SetFlags()
	m, _, _ = mapMessage(out, e, false)
	if !m.Outgoing || m.SenderSourceID != "" || m.ReplyToSourceID != "5" || !m.EditedAt.Equal(when.Add(time.Minute)) {
		t.Errorf("outgoing = %+v", m)
	}
	out.EditHide = true
	if m, _, _ = mapMessage(out, e, false); !m.EditedAt.IsZero() {
		t.Error("an edit Telegram hides (such as a reaction) counts as an edit")
	}

	group := &tg.Message{ID: 7, PeerID: &tg.PeerChannel{ChannelID: 20}, FromID: &tg.PeerUser{UserID: 2}, Date: int(when.Unix()), Message: "hi",
		FwdFrom: tg.MessageFwdHeader{FromName: "Hidden Sender"}}
	group.SetFlags()
	m, _, _ = mapMessage(group, e, false)
	if m.ChatSourceID != "channel:20" || m.SenderSourceID != "user:2" || m.ForwardedFrom != "Hidden Sender" {
		t.Errorf("group message = %+v", m)
	}
	group.FwdFrom = tg.MessageFwdHeader{FromID: &tg.PeerUser{UserID: 1}}
	group.SetFlags()
	if m, _, _ = mapMessage(group, e, false); m.ForwardedFrom != "Dan Whitfield" {
		t.Errorf("forwarded from a known user = %q", m.ForwardedFrom)
	}
	post := &tg.Message{ID: 8, Post: true, PeerID: &tg.PeerChannel{ChannelID: 21}, Date: int(when.Unix()), Message: "Headline"}
	if m, _, _ = mapMessage(post, e, false); m.SenderSourceID != "channel:21" {
		t.Errorf("a channel post's sender = %q", m.SenderSourceID)
	}
	if _, _, ok := mapMessage(&tg.MessageEmpty{ID: 9}, e, false); ok {
		t.Error("an empty message was mapped")
	}
}

func TestMapMedia(t *testing.T) {
	e := fixture()
	photo := &tg.Photo{ID: 77, AccessHash: 78, FileReference: []byte{1}, DCID: 2, Sizes: []tg.PhotoSizeClass{
		&tg.PhotoSize{Type: "m", Size: 1000}, &tg.PhotoSizeProgressive{Type: "y", Sizes: []int{500, 9000}}, &tg.PhotoStrippedSize{Type: "i"},
	}}
	doc := func(mime string, attrs ...tg.DocumentAttributeClass) *tg.Document {
		return &tg.Document{ID: 88, AccessHash: 89, FileReference: []byte{2}, DCID: 4, MimeType: mime, Size: 4096, Attributes: attrs}
	}
	msg := func(media tg.MessageMediaClass, text string) *tg.Message {
		return &tg.Message{ID: 1, PeerID: &tg.PeerUser{UserID: 1}, Date: int(when.Unix()), Message: text, Media: media}
	}
	for name, c := range map[string]struct {
		media    tg.MessageMediaClass
		keep     bool
		want     model.Attachment
		download bool
	}{
		"photo": {&tg.MessageMediaPhoto{Photo: photo}, false,
			model.Attachment{Kind: model.AttachPhoto, MimeType: "image/jpeg", Size: 9000, SourceID: "photo:77"}, true},
		"file": {&tg.MessageMediaDocument{Document: doc("application/pdf", &tg.DocumentAttributeFilename{FileName: "lease.pdf"})}, false,
			model.Attachment{Kind: model.AttachDocument, FileName: "lease.pdf", MimeType: "application/pdf", Size: 4096, SourceID: "document:88"}, true},
		"voice": {&tg.MessageMediaDocument{Document: doc("audio/ogg", &tg.DocumentAttributeAudio{Voice: true})}, false,
			model.Attachment{Kind: model.AttachVoice, MimeType: "audio/ogg", Size: 4096, SourceID: "document:88"}, true},
		"song": {&tg.MessageMediaDocument{Document: doc("audio/mpeg", &tg.DocumentAttributeAudio{Title: "Song"}, &tg.DocumentAttributeFilename{FileName: "song.mp3"})}, false,
			model.Attachment{Kind: model.AttachAudio, FileName: "song.mp3", MimeType: "audio/mpeg", Size: 4096, SourceID: "document:88"}, true},
		"round video": {&tg.MessageMediaDocument{Document: doc("video/mp4", &tg.DocumentAttributeVideo{RoundMessage: true})}, false,
			model.Attachment{Kind: model.AttachVideo, MimeType: "video/mp4", Size: 4096, SourceID: "document:88"}, true},
		"sticker": {&tg.MessageMediaDocument{Document: doc("image/webp", &tg.DocumentAttributeSticker{})}, false,
			model.Attachment{Kind: model.AttachSticker, MimeType: "image/webp", Size: 4096, SourceID: "document:88"}, true},
		"timed photo, not kept": {&tg.MessageMediaPhoto{Photo: photo, TTLSeconds: 10}, false,
			model.Attachment{Kind: model.AttachPhoto, MimeType: "image/jpeg", Size: 9000, SourceID: "photo:77", Ephemeral: true, NoFetch: true, NoFetchReason: reasonEphemeral}, false},
		"view-once video, kept": {&tg.MessageMediaDocument{Document: doc("video/mp4", &tg.DocumentAttributeVideo{}), TTLSeconds: viewOnce}, true,
			model.Attachment{Kind: model.AttachVideo, MimeType: "video/mp4", Size: 4096, SourceID: "document:88", Ephemeral: true}, true},
	} {
		m := msg(c.media, "caption")
		m.SetFlags()
		if p, ok := c.media.(*tg.MessageMediaPhoto); ok {
			p.SetFlags()
		}
		if d, ok := c.media.(*tg.MessageMediaDocument); ok {
			d.SetFlags()
		}
		got, media, ok := mapMessage(m, e, c.keep)
		if !ok || got.Text != "caption" || len(got.Attachments) != 1 || got.Attachments[0] != c.want {
			t.Errorf("%s: attachments = %+v", name, got.Attachments)
			continue
		}
		if c.download != (len(media) == 1) {
			t.Errorf("%s: %d downloads, want download %v", name, len(media), c.download)
			continue
		}
		if c.download && (media[0].Position != 0 || media[0].Location == nil || media[0].Ephemeral != c.want.Ephemeral) {
			t.Errorf("%s: download = %+v", name, media[0])
		}
	}
	m, media, _ := mapMessage(msg(&tg.MessageMediaPhoto{Photo: photo}, ""), e, false)
	if loc, ok := media[0].Location.(*tg.InputPhotoFileLocation); !ok || loc.ID != 77 || loc.AccessHash != 78 || loc.ThumbSize != "y" || len(m.Attachments) != 1 {
		t.Errorf("photo location = %+v", media[0].Location)
	}

	for name, c := range map[string]struct {
		media tg.MessageMediaClass
		want  string
	}{
		"location": {&tg.MessageMediaGeo{Geo: &tg.GeoPoint{Lat: 51.5, Long: -0.12}}, "caption\nLocation: 51.50000, -0.12000"},
		"venue":    {&tg.MessageMediaVenue{Geo: &tg.GeoPoint{Lat: 1, Long: 2}, Title: "Café", Address: "Calle Sol 4"}, "caption\nPlace: Café, Calle Sol 4"},
		"contact":  {&tg.MessageMediaContact{FirstName: "Ana", LastName: "Ruiz", PhoneNumber: "+34 600"}, "caption\nContact: Ana Ruiz, +34 600"},
		"poll":     {&tg.MessageMediaPoll{Poll: tg.Poll{Question: tg.TextWithEntities{Text: "Pizza?"}}}, "caption\nPoll: Pizza?"},
		"web page": {&tg.MessageMediaWebPage{}, "caption"},
	} {
		got, media, _ := mapMessage(msg(c.media, "caption"), e, false)
		if got.Text != c.want || len(got.Attachments) != 0 || len(media) != 0 {
			t.Errorf("%s: text %q, %d attachments", name, got.Text, len(got.Attachments))
		}
	}
	if got, _, _ := mapMessage(msg(&tg.MessageMediaContact{FirstName: "Ana"}, ""), e, false); got.Text != "Contact: Ana" {
		t.Errorf("a contact alone = %q", got.Text)
	}
}

func TestMapServiceMessages(t *testing.T) {
	e := fixture()
	for _, c := range []struct {
		action tg.MessageActionClass
		out    bool
		want   string
	}{
		{&tg.MessageActionPinMessage{}, false, "Dan Whitfield pinned a message"},
		{&tg.MessageActionPinMessage{}, true, "You pinned a message"},
		{&tg.MessageActionChatCreate{Title: "Familia"}, false, "Dan Whitfield created the group “Familia”"},
		{&tg.MessageActionChannelCreate{Title: "News"}, false, "Dan Whitfield created “News”"},
		{&tg.MessageActionChatEditTitle{Title: "Familia 2"}, false, "Dan Whitfield renamed the chat to “Familia 2”"},
		{&tg.MessageActionChatEditPhoto{}, false, "Dan Whitfield changed the chat's picture"},
		{&tg.MessageActionChatDeletePhoto{}, false, "Dan Whitfield removed the chat's picture"},
		{&tg.MessageActionChatAddUser{Users: []int64{2, 4}}, false, "Dan Whitfield added @marina_s and Weather"},
		{&tg.MessageActionChatAddUser{Users: []int64{1}}, false, "Dan Whitfield joined"},
		{&tg.MessageActionChatDeleteUser{UserID: 2}, false, "Dan Whitfield removed @marina_s"},
		{&tg.MessageActionChatDeleteUser{UserID: 1}, false, "Dan Whitfield left"},
		{&tg.MessageActionChatJoinedByLink{}, false, "Dan Whitfield joined by invite link"},
		{&tg.MessageActionChatJoinedByRequest{}, false, "Dan Whitfield joined"},
		{&tg.MessageActionPhoneCall{Duration: 125}, false, "Call, 2 min 5 s"},
		{&tg.MessageActionPhoneCall{Video: true, Reason: &tg.PhoneCallDiscardReasonMissed{}}, false, "Missed video call"},
		{&tg.MessageActionPhoneCall{Reason: &tg.PhoneCallDiscardReasonBusy{}}, false, "Declined call"},
		{&tg.MessageActionGroupCall{Duration: 30}, false, "Group call, 30 s"},
		{&tg.MessageActionHistoryClear{}, false, "History cleared"},
		{&tg.MessageActionScreenshotTaken{}, false, "Dan Whitfield took a screenshot"},
		{&tg.MessageActionSetMessagesTTL{Period: 86400}, false, "Dan Whitfield set messages to delete after 1 day"},
		{&tg.MessageActionSetMessagesTTL{}, false, "Dan Whitfield turned off auto-delete"},
		{&tg.MessageActionChatMigrateTo{ChannelID: 20}, false, "This group became a supergroup"},
		{&tg.MessageActionChannelMigrateFrom{Title: "Familia"}, false, "Upgraded from the group “Familia”"},
		{&tg.MessageActionContactSignUp{}, false, "Dan Whitfield joined Telegram"},
		{&tg.MessageActionCustomAction{Message: "Something odd"}, false, "Something odd"},
		{&tg.MessageActionGameScore{}, false, "Service message"},
	} {
		svc := &tg.MessageService{ID: 3, Out: c.out, PeerID: &tg.PeerChat{ChatID: 10}, FromID: &tg.PeerUser{UserID: 1}, Date: int(when.Unix()), Action: c.action}
		if c.out {
			svc.FromID = &tg.PeerUser{UserID: self}
		}
		m, media, ok := mapMessage(svc, e, false)
		if !ok || m.Kind != model.KindService || m.Text != c.want || len(media) != 0 || m.ChatSourceID != "chat:10" {
			t.Errorf("%T = %q (%+v), want %q", c.action, m.Text, m, c.want)
		}
	}
}

func TestRawSurvivesEveryMessageKind(t *testing.T) {
	e := fixture()
	for _, m := range []tg.MessageClass{
		&tg.Message{ID: 1, PeerID: &tg.PeerUser{UserID: 1}, Media: &tg.MessageMediaPhoto{Photo: &tg.Photo{ID: 1}}},
		&tg.MessageService{ID: 2, PeerID: &tg.PeerChat{ChatID: 10}, Action: &tg.MessageActionPinMessage{}},
	} {
		got, _, _ := mapMessage(m, e, false)
		if !json.Valid(got.Raw) || !strings.Contains(string(got.Raw), `"_":"`) {
			t.Errorf("%T raw = %s", m, got.Raw)
		}
	}
}
