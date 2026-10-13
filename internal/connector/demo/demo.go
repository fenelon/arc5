// Package demo provides a sample archive to look at before connecting a real
// account, and a connector that does nothing but report it as live.
package demo

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"strings"
	"time"

	"github.com/fenelon/arc5/internal/archive"
	"github.com/fenelon/arc5/internal/connector"
	"github.com/fenelon/arc5/internal/model"
)

// Name is the connector name stored on demo accounts.
const Name = "demo"

// signedOutKey is set in the account's state while the demo is signed out.
const signedOutKey = "signed_out"

// loginCode is what the demo's login asks for.
const loginCode = "12345"

// idle captures nothing. It reports itself live, or needing login after it
// was signed out, so every part of the Sources page can be tried on the demo.
type idle struct{}

func (idle) Run(ctx context.Context, sink connector.Sink, state connector.State, _ map[string]string) error {
	if v, err := state.Get(signedOutKey); err != nil || len(v) > 0 {
		return errors.Join(connector.ErrNeedsLogin, err)
	}
	sink.ReportProgress(connector.Progress{Phase: connector.PhaseLive})
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case ref := <-sink.RequestedMedia():
			// "Fetch now" on a sample file: there is nothing to fetch.
			if err := sink.SetAttachmentStatus(ref, model.StatusUnavailable, "the demo has no files to fetch"); err != nil {
				return err
			}
		}
	}
}

func (idle) Setup(state connector.State) connector.SetupFlow { return &login{state: state} }

// Settings are examples of each kind of field. The demo ignores them.
func (idle) Settings() []connector.Field {
	return []connector.Field{
		{Name: "speed", Label: "Speed", Help: "How quickly history is fetched. The demo has none to fetch.",
			Kind: connector.FieldChoice, Choices: []string{"Slow", "Normal", "Fast"}, Default: "Normal"},
		{Name: "keep_ephemeral", Label: "Keep self-destructing and view-once media",
			Help: "Off unless you turn it on. The demo has none.", Kind: connector.FieldBool},
	}
}

func (idle) SignOut(_ context.Context, state connector.State) error {
	return state.Set(signedOutKey, []byte("1"))
}

// login asks for a code, the way a real source asks for one sent by text.
type login struct{ state connector.State }

func (l *login) Next(_ context.Context, input map[string]string) (connector.Step, error) {
	ask := connector.Step{
		Title:  "Sign in to the demo",
		Help:   "A real source sends a code to your phone. The demo's code is " + loginCode + ".",
		Fields: []connector.Field{{Name: "code", Label: "Login code", Kind: connector.FieldText}},
	}
	if input == nil {
		return ask, nil
	}
	if strings.TrimSpace(input["code"]) != loginCode {
		return ask, fmt.Errorf("That code is not right. The demo's code is %s.", loginCode)
	}
	if err := l.state.Set(signedOutKey, nil); err != nil {
		return ask, err
	}
	return connector.Step{Done: true}, nil
}

// Register adds the demo connector, so demo accounts show as live. It is
// hidden: "Add a source" does not offer it.
func Register(reg *connector.Registry) {
	reg.Register(connector.Info{Name: Name, Title: "Demo", Display: connector.DisplayChats, Hidden: true}, func() connector.Connector { return idle{} })
}

// day is the last day of the sample conversations.
var day = time.Date(2026, time.September, 28, 0, 0, 0, 0, time.UTC)

func at(daysBefore, hour, minute int) time.Time {
	return day.AddDate(0, 0, -daysBefore).Add(time.Duration(hour)*time.Hour + time.Duration(minute)*time.Minute)
}

// floorPlan draws a small picture to stand for a photo; the corrected plan
// has a door in the inner wall.
func floorPlan(corrected bool) []byte {
	img := image.NewRGBA(image.Rect(0, 0, 320, 200))
	wall, floor := color.RGBA{0x0D, 0x25, 0x30, 0xFF}, color.RGBA{0xD7, 0xEC, 0xEF, 0xFF}
	for y := range 200 {
		for x := range 320 {
			c := floor
			door := corrected && x > 150 && x < 156 && y > 40 && y < 80
			if !door && (x < 6 || x >= 314 || y < 6 || y >= 194 || (x > 150 && x < 156 && y < 120) || (y > 116 && y < 122 && x > 150)) {
				c = wall
			}
			img.SetRGBA(x, y, c)
		}
	}
	var buf bytes.Buffer
	png.Encode(&buf, img)
	return buf.Bytes()
}

// portrait draws a small picture to stand for someone's profile photo: a
// head and shoulders in fg on bg.
func portrait(bg, fg color.RGBA) []byte {
	const size = 160
	img := image.NewRGBA(image.Rect(0, 0, size, size))
	for y := range size {
		for x := range size {
			c := bg
			hx, hy := x-80, y-62  // the head
			sx, sy := x-80, y-170 // the shoulders
			if hx*hx+hy*hy < 30*30 || (sx*sx)/4+sy*sy < 60*60 {
				c = fg
			}
			img.SetRGBA(x, y, c)
		}
	}
	var buf bytes.Buffer
	png.Encode(&buf, img)
	return buf.Bytes()
}

// Seed writes the sample archive. It does nothing, and returns false, if the
// archive already has an account.
func Seed(store *archive.Store) (bool, error) {
	accounts, err := store.Accounts()
	if err != nil || len(accounts) > 0 {
		return false, err
	}
	acc, err := store.CreateAccount(Name, "Personal", nil)
	if err != nil {
		return false, err
	}
	b := &builder{store: store, acc: acc}

	for _, s := range []model.Sender{
		{SourceID: "dan", DisplayName: "Dan Whitfield"}, {SourceID: "marina", DisplayName: "Marina Sokolova", Username: "marina_s"},
		{SourceID: "pablo", DisplayName: "Pablo Ruiz"}, {SourceID: "oleg", DisplayName: "Олег Ветров"},
	} {
		b.do(store.UpsertSender(acc, s))
	}
	for _, c := range []model.Chat{
		{SourceID: "dan", Kind: model.ChatPrivate, Title: "Dan Whitfield"},
		{SourceID: "marina", Kind: model.ChatPrivate, Title: "Marina Sokolova"},
		{SourceID: "familia", Kind: model.ChatGroup, Title: "Familia: Marina y Pablo"},
		{SourceID: "hikes", Kind: model.ChatGroup, Title: "Походы 2026"},
		{SourceID: "saved", Kind: model.ChatPrivate, Title: "Saved Messages"},
	} {
		b.do(store.UpsertChat(acc, c))
	}
	// Pictures for some chats and people; the others show initials. A private
	// chat's picture is its person's, as the Telegram connector keeps it.
	for _, p := range []struct {
		chat, sender string
		bg, fg       color.RGBA
	}{
		{"dan", "dan", color.RGBA{0x2E, 0x5E, 0x8C, 0xFF}, color.RGBA{0xF2, 0xC1, 0x8D, 0xFF}},
		{"marina", "marina", color.RGBA{0x7A, 0x3E, 0x6B, 0xFF}, color.RGBA{0xF6, 0xD5, 0xB8, 0xFF}},
		{"familia", "", color.RGBA{0x3D, 0x7A, 0x4F, 0xFF}, color.RGBA{0xE8, 0xF1, 0xD8, 0xFF}},
	} {
		picture := portrait(p.bg, p.fg)
		b.do(store.PutAvatar(acc, model.AvatarRef{Kind: model.AvatarChat, SourceID: p.chat, AvatarID: "photo:" + p.chat}, bytes.NewReader(picture)))
		if p.sender != "" {
			b.do(store.PutAvatar(acc, model.AvatarRef{Kind: model.AvatarSender, SourceID: p.sender, AvatarID: "photo:" + p.chat}, bytes.NewReader(picture)))
		}
	}

	// Years of small talk with Dan, so the year rail has something to show.
	for month := 0; month < 66; month++ {
		when := time.Date(2021, time.March, 5, 9, 0, 0, 0, time.UTC).AddDate(0, month, 0)
		for i := range 1 + (month*7)%5 {
			b.say("dan", "dan", when.Add(time.Duration(i)*time.Hour), fmt.Sprintf("Lunch on the %dth? Usual place.", 5+i), i%2 == 1)
		}
	}
	b.say("dan", "", at(1, 18, 22), "Are we still on for Monday? I can bring the projector.", true)
	first := b.say("dan", "dan", at(1, 18, 40), "Yes. Room 2 from ten.", false)
	first.Text, first.EditedAt = "Yes. Room 4 from ten, the old one with the broken blinds.", at(1, 18, 44)
	b.put(first)
	plan := b.message("dan", "dan", at(1, 18, 41), "Here is the floor plan.", false)
	plan.Attachments = []model.Attachment{{Kind: model.AttachPhoto, FileName: "floor-plan.png", MimeType: "image/png", Size: 2048, SourceID: "photo:plan-1"}}
	b.put(plan)
	b.do(store.PutMedia(acc, model.AttachmentRef{ChatSourceID: "dan", MessageSourceID: plan.SourceID}, bytes.NewReader(floorPlan(false))))
	// Dan swaps the picture for a corrected one: the first is kept as replaced.
	plan.Attachments = []model.Attachment{{Kind: model.AttachPhoto, FileName: "floor-plan-v2.png", MimeType: "image/png", Size: 2048, SourceID: "photo:plan-2"}}
	plan.EditedAt = at(1, 18, 43)
	b.put(plan)
	b.do(store.PutMedia(acc, model.AttachmentRef{ChatSourceID: "dan", MessageSourceID: plan.SourceID}, bytes.NewReader(floorPlan(true))))
	b.put(model.Message{ChatSourceID: "dan", SourceID: b.next(), Kind: model.KindService, SentAt: at(1, 18, 42), Text: "Dan Whitfield pinned a message"})
	gone := b.say("dan", "dan", at(0, 10, 2), "Marina's flight lands at 18:40. Can you pick her up, or should I book a taxi?", false)
	b.do(store.MarkDeleted(acc, "dan", []string{gone.SourceID}))
	b.say("dan", "", at(0, 10, 5), "I'll get her. Terminal 1?", true)
	voice := b.message("dan", "dan", at(0, 10, 7), "", false)
	voice.Attachments = []model.Attachment{{Kind: model.AttachVoice, FileName: "voice-note.ogg", MimeType: "audio/ogg", Size: 61_000, NoFetch: true, NoFetchReason: "the demo archive has no audio"}}
	b.put(voice)
	video := b.message("dan", "dan", at(0, 10, 15), "And this is the route from arrivals, in case the signs are down again.", false)
	video.Attachments = []model.Attachment{{Kind: model.AttachVideo, FileName: "arrivals-walkthrough.mp4", MimeType: "video/mp4", Size: 312 << 20}}
	b.put(video)
	once := b.message("dan", "dan", at(0, 10, 16), "", false)
	once.Attachments = []model.Attachment{{Kind: model.AttachPhoto, FileName: "door-code.jpg", MimeType: "image/jpeg", Size: 240_000, Ephemeral: true, NoFetch: true, NoFetchReason: "self-destructing media is not archived"}}
	b.put(once)
	b.say("dan", "", at(0, 19, 3), "Got her. Traffic is bad, we'll be there around eight.", true)

	b.say("marina", "", at(2, 20, 15), "Долетела нормально?", true)
	b.say("marina", "marina", at(2, 20, 31), "Спасибо, что встретил! Чемодан нашёлся, всё хорошо.", false)
	b.say("familia", "marina", at(4, 20, 50), "¿Alguien ha visto mis llaves?", false)
	b.say("familia", "pablo", at(4, 21, 14), "Dile a Marina que las llaves están en el buzón, por favor.", false)
	b.say("hikes", "oleg", at(10, 7, 5), "Маршрут на субботу в закрепе. Встречаемся у причала в восемь.", false)
	b.say("hikes", "", at(10, 7, 40), "Буду. Беру горелку и чай.", true)
	b.say("saved", "", at(25, 9, 0), "Passport renewal: form 11, two photos, old passport.", true)
	return true, b.err
}

// builder stores sample messages and remembers the first error.
type builder struct {
	store *archive.Store
	acc   int64
	n     int
	err   error
}

func (b *builder) do(err error) {
	if b.err == nil {
		b.err = err
	}
}

func (b *builder) next() string {
	b.n++
	return fmt.Sprint(b.n)
}

func (b *builder) message(chat, sender string, when time.Time, text string, outgoing bool) model.Message {
	if outgoing {
		sender = "" // the owner is not one of the chat's senders
	}
	return model.Message{ChatSourceID: chat, SourceID: b.next(), SenderSourceID: sender, SentAt: when, Text: text, Outgoing: outgoing}
}

func (b *builder) put(m model.Message) {
	_, err := b.store.UpsertMessage(b.acc, m)
	b.do(err)
}

func (b *builder) say(chat, sender string, when time.Time, text string, outgoing bool) model.Message {
	m := b.message(chat, sender, when, text, outgoing)
	b.put(m)
	return m
}
