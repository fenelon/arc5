package mail

import (
	"encoding/base64"
	"slices"
	"strings"
	"testing"
	"time"

	"golang.org/x/text/encoding/charmap"
	"golang.org/x/text/encoding/japanese"

	"github.com/fenelon/arc5/internal/model"
)

// crlf turns a readable fixture into a message with CRLF line ends.
func crlf(s string) []byte {
	return []byte(strings.ReplaceAll(strings.TrimPrefix(s, "\n"), "\n", "\r\n"))
}

const plainMail = `
From: Dan Whitfield <dan.whitfield@example.com>
To: me@example.org, "Nair, Priya" <priya.nair@example.com>
Cc: ops@example.com
Reply-To: Dan <dan@home.example>
Subject: Monday planning, room 4
Date: Sat, 27 Sep 2026 18:40:00 +0200
Message-ID: <CAF=planning-4@mail.Example.COM>
In-Reply-To: <planning-3@example.com>
References: <planning-1@example.com>
 <planning-2@example.com> <planning-3@example.com>
Content-Type: text/plain; charset=utf-8

Room 4 is booked from ten.

Dan
`

func TestParsePlain(t *testing.T) {
	p := Parse(crlf(plainMail))
	if p.Broken {
		t.Fatal("a plain message is broken")
	}
	if p.MessageID != "CAF=planning-4@mail.example.com" {
		t.Errorf("message ID = %q", p.MessageID)
	}
	if !slices.Equal(p.References, []string{"planning-1@example.com", "planning-2@example.com", "planning-3@example.com"}) || p.InReplyTo != "planning-3@example.com" {
		t.Errorf("references = %v, in reply to %q", p.References, p.InReplyTo)
	}
	if p.Subject != "Monday planning, room 4" || !p.Date.Equal(time.Date(2026, 9, 27, 16, 40, 0, 0, time.UTC)) {
		t.Errorf("subject %q, date %v", p.Subject, p.Date)
	}
	want := []model.Address{
		{Role: model.AddrFrom, Name: "Dan Whitfield", Address: "dan.whitfield@example.com"},
		{Role: model.AddrTo, Address: "me@example.org"},
		{Role: model.AddrTo, Name: "Nair, Priya", Address: "priya.nair@example.com"},
		{Role: model.AddrCc, Address: "ops@example.com"},
		{Role: model.AddrReplyTo, Name: "Dan", Address: "dan@home.example"},
	}
	if !slices.Equal(p.Addresses, want) {
		t.Errorf("addresses = %+v", p.Addresses)
	}
	if p.Text != "Room 4 is booked from ten.\n\nDan" {
		t.Errorf("text = %q", p.Text)
	}
}

const alternativeMail = `
From: a@example.com
Subject: Both
Message-ID: <both@example.com>
MIME-Version: 1.0
Content-Type: multipart/alternative; boundary="b1"

--b1
Content-Type: text/plain; charset=us-ascii

The plain words.
--b1
Content-Type: text/html; charset=us-ascii

<p>The <b>HTML</b> words.</p>
--b1--
`

const htmlOnlyMail = `
From: tickets@northline.example
Subject: Your tickets for 11 July
Message-ID: <t@northline.example>
Content-Type: text/html; charset=utf-8

<html><head><style>p{color:red}</style></head><body><p>Two adults, departing 07:42.</p></body></html>
`

const stubMail = `
From: news@example.com
Subject: News
Content-Type: multipart/alternative; boundary=x

--x
Content-Type: text/plain

This email requires an HTML-capable mail client. View it in your browser.
--x
Content-Type: text/html

<p>The real news.</p>
--x--
`

func TestParseChoosesText(t *testing.T) {
	for name, c := range map[string]struct {
		raw  string
		want string
	}{
		"plain wins":    {alternativeMail, "The plain words."},
		"html only":     {htmlOnlyMail, "Two adults, departing 07:42."},
		"plain is stub": {stubMail, "The real news."},
	} {
		if got := Parse(crlf(c.raw)).Text; got != c.want {
			t.Errorf("%s: text = %q, want %q", name, got, c.want)
		}
	}
}

func TestParseAttachments(t *testing.T) {
	pdf := base64.StdEncoding.EncodeToString([]byte("%PDF-1.4 agenda"))
	png := base64.StdEncoding.EncodeToString([]byte("\x89PNG pixels"))
	tnef := base64.StdEncoding.EncodeToString([]byte("tnef"))
	raw := `
From: Dan <dan@example.com>
Subject: Agenda
Message-ID: <agenda@example.com>
Content-Type: multipart/mixed; boundary="outer"

--outer
Content-Type: multipart/related; boundary="rel"

--rel
Content-Type: multipart/alternative; boundary="alt"

--alt
Content-Type: text/plain; charset=utf-8

Agenda attached.
--alt
Content-Type: text/html; charset=utf-8

<p>Agenda attached. <img src="cid:logo@x"></p>
--alt--
--rel
Content-Type: image/png
Content-ID: <logo@x>
Content-Transfer-Encoding: base64

` + png + `
--rel--
--outer
Content-Type: application/pdf; name="agenda-monday.pdf"
Content-Disposition: attachment; filename="agenda-monday.pdf"
Content-Transfer-Encoding: base64

` + pdf + `
--outer
Content-Type: application/ms-tnef; name="winmail.dat"
Content-Disposition: attachment; filename="winmail.dat"
Content-Transfer-Encoding: base64

` + tnef + `
--outer
Content-Type: message/rfc822

From: someone@example.com
Subject: Forwarded

Inner text.
--outer
Content-Type: application/pgp-signature; name="signature.asc"

-----BEGIN PGP SIGNATURE-----
--outer--
`
	p := Parse(crlf(raw))
	if p.Text != "Agenda attached." {
		t.Errorf("text = %q", p.Text)
	}
	var got []string
	for _, part := range p.Parts {
		got = append(got, part.FileName+"|"+part.MimeType+"|"+part.ContentID+"|"+string(part.Data[:4]))
	}
	want := []string{
		"image-1.png|image/png|logo@x|\x89PNG",
		"agenda-monday.pdf|application/pdf||%PDF",
		"winmail.dat|application/ms-tnef||tnef",
		"forwarded-message.eml|message/rfc822||From",
	}
	if !slices.Equal(got, want) {
		t.Errorf("parts = %q\nwant %q", got, want)
	}
	if !p.Parts[0].Inline || p.Parts[1].Inline {
		t.Errorf("inline: %v %v", p.Parts[0].Inline, p.Parts[1].Inline)
	}
}

func TestParseCharsetsAndEncodings(t *testing.T) {
	koi8, err := charmap.KOI8R.NewEncoder().String("Свёл расходы в таблицу.")
	if err != nil {
		t.Fatal(err)
	}
	sjis, err := japanese.ShiftJIS.NewEncoder().String("会議は月曜日です。")
	if err != nil {
		t.Fatal(err)
	}
	jis, err := japanese.ISO2022JP.NewEncoder().String("会議")
	if err != nil {
		t.Fatal(err)
	}
	for name, c := range map[string]struct {
		raw              string
		text, subject    string
		fromName, sender string
	}{
		"koi8-r": {
			raw: "From: =?KOI8-R?B?" + base64.StdEncoding.EncodeToString([]byte(mustKOI8(t, "Олег Ветров"))) + "?= <oleg@example.com>\n" +
				"Subject: =?utf-8?Q?=D0=9E=D1=82=D1=87=D1=91=D1=82?=\nContent-Type: text/plain; charset=koi8-r\nContent-Transfer-Encoding: 8bit\n\n" + koi8 + "\n",
			text: "Свёл расходы в таблицу.", subject: "Отчёт", fromName: "Олег Ветров",
		},
		"shift_jis": {
			raw:  "From: k@example.jp\nSubject: =?ISO-2022-JP?B?" + base64.StdEncoding.EncodeToString([]byte(jis)) + "?=\nContent-Type: text/plain; charset=Shift_JIS\n\n" + sjis + "\n",
			text: "会議は月曜日です。", subject: "会議", fromName: "",
		},
		"quoted-printable": {
			raw: "From: a@example.com\nSubject: QP\nContent-Type: text/plain; charset=utf-8\nContent-Transfer-Encoding: quoted-printable\n\n" +
				"Caf=C3=A9 au lait, a long line that the sender's mail program wrapped=\n here.\n",
			text: "Café au lait, a long line that the sender's mail program wrapped here.", subject: "QP",
		},
		"base64": {
			raw: "From: a@example.com\nSubject: B64\nContent-Type: text/plain; charset=utf-8\nContent-Transfer-Encoding: base64\n\n" +
				base64.StdEncoding.EncodeToString([]byte("¿A las nueve en casa?")) + "\n",
			text: "¿A las nueve en casa?", subject: "B64",
		},
		"flowed": {
			raw: "From: a@example.com\nSubject: Flowed\nContent-Type: text/plain; charset=utf-8; format=flowed\n\n" +
				"This paragraph was \nwrapped by the sender. \nIt is one line.\n\nA second one.\n",
			text: "This paragraph was wrapped by the sender. It is one line.\n\nA second one.", subject: "Flowed",
		},
	} {
		p := Parse(crlf(c.raw))
		if p.Text != c.text || p.Subject != c.subject {
			t.Errorf("%s: text %q, subject %q", name, p.Text, p.Subject)
		}
		if c.fromName != "" && (len(p.Addresses) == 0 || p.Addresses[0].Name != c.fromName) {
			t.Errorf("%s: from = %+v", name, p.Addresses)
		}
	}
}

func mustKOI8(t *testing.T, s string) string {
	t.Helper()
	out, err := charmap.KOI8R.NewEncoder().String(s)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestParseBroken(t *testing.T) {
	for name, raw := range map[string]string{
		"no header end":    "this is not a message at all",
		"bad multipart":    "From: a@example.com\nSubject: Cut\nContent-Type: multipart/mixed; boundary=zz\n\n--zz\nContent-Type: text/plain\n\nStarted",
		"bad address list": "From: <<<@@>>>\nSubject: Odd\n\nStill readable.\n",
		"unknown charset":  "From: a@example.com\nContent-Type: text/plain; charset=x-made-up\n\nPlain enough.\n",
		"unknown encoding": "From: a@example.com\nContent-Type: text/plain\nContent-Transfer-Encoding: x-weird\n\nRaw words.\n",
	} {
		p := Parse(crlf(raw))
		if p.Text == "" {
			t.Errorf("%s: no text at all", name)
		}
	}
	if p := Parse(crlf("From: a@example.com\nSubject: Cut\nContent-Type: multipart/mixed; boundary=zz\n\n--zz\nContent-Type: text/plain\n\nStarted")); p.Text != "Started" {
		t.Errorf("a cut multipart keeps what was read: %q", p.Text)
	}
	if p := Parse(crlf("From: <<<@@>>>\nSubject: Odd\n\nStill readable.\n")); p.Text != "Still readable." || p.Subject != "Odd" {
		t.Errorf("a bad From loses the rest: %+v", p)
	}
	if p := Parse(nil); !p.Broken || p.Text != Unreadable {
		t.Errorf("an empty message: %+v", p)
	}
}

func TestIdentity(t *testing.T) {
	raw := crlf(plainMail)
	if id := Identity(Parse(raw), raw); id != "CAF=planning-4@mail.example.com" {
		t.Errorf("identity = %q", id)
	}
	noID := crlf("From: a@example.com\nSubject: Hi\nDate: Sat, 27 Sep 2026 18:40:00 +0200\n\nHello.\n")
	id := Identity(Parse(noID), noID)
	if !strings.HasPrefix(id, "sha256:") || len(id) != len("sha256:")+64 {
		t.Fatalf("identity without a Message-ID = %q", id)
	}
	// The same message with headers a server added is the same message.
	again := append(crlf("X-Received: by server\n"), noID...)
	if Identity(Parse(again), again) != id {
		t.Error("an added header changed the identity")
	}
	other := crlf("From: a@example.com\nSubject: Hi\nDate: Sat, 27 Sep 2026 18:40:00 +0200\n\nHello again.\n")
	if Identity(Parse(other), other) == id {
		t.Error("different messages share an identity")
	}
}

func TestNormalizeID(t *testing.T) {
	for in, want := range map[string]string{
		"<Abc.Def@Mail.Example.COM>": "Abc.Def@mail.example.com",
		"  <x@y> ":                   "x@y",
		"no-brackets@Host":           "no-brackets@host",
		"":                           "",
	} {
		if got := NormalizeID(in); got != want {
			t.Errorf("NormalizeID(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestThreadRoot(t *testing.T) {
	known := map[string]string{"parent@x": "thread:root@x"}
	lookup := func(id string) (string, bool) { t, ok := known[id]; return t, ok }
	for name, c := range map[string]struct {
		p    Parsed
		want string
	}{
		"references":         {Parsed{MessageID: "c@x", References: []string{"a@x", "b@x"}, InReplyTo: "b@x"}, "thread:a@x"},
		"in-reply-to known":  {Parsed{MessageID: "c@x", InReplyTo: "parent@x"}, "thread:root@x"},
		"in-reply-to unseen": {Parsed{MessageID: "c@x", InReplyTo: "p@x"}, "thread:p@x"},
		"a first message":    {Parsed{MessageID: "c@x"}, "thread:c@x"},
	} {
		if got := ThreadRoot(c.p, c.p.MessageID, lookup); got != c.want {
			t.Errorf("%s: %q, want %q", name, got, c.want)
		}
	}
	if got := ThreadRoot(Parsed{}, "sha256:ab", lookup); got != "thread:sha256:ab" {
		t.Errorf("no Message-ID: %q", got)
	}
}

func TestCleanSubject(t *testing.T) {
	for in, want := range map[string]string{
		"Re: Monday planning":    "Monday planning",
		"RE: Fwd: re[2]: Monday": "Monday",
		"AW: WG: Termin":         "Termin",
		"Ответ: Отчёт о походе":  "Отчёт о походе",
		"Sv: Middag":             "Middag",
		"  Fw:   Spaces  ":       "Spaces",
		"Regarding the plan":     "Regarding the plan",
		"Re:":                    "",
		"":                       "",
	} {
		if got := CleanSubject(in); got != want {
			t.Errorf("CleanSubject(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestMessage(t *testing.T) {
	raw := crlf(plainMail)
	p := Parse(raw)
	p.Parts = []Part{{FileName: "agenda.pdf", MimeType: "application/pdf", Data: []byte("pdf")}, {FileName: "x.jpg", MimeType: "image/jpeg", Data: []byte("jpg!")}}
	received := time.Date(2026, 9, 27, 17, 0, 0, 0, time.UTC)
	m := p.Message("thread:planning-1@example.com", "id", func(a string) bool { return a == "me@example.org" }, received)
	if m.ChatSourceID != "thread:planning-1@example.com" || m.SourceID != "id" || m.SenderSourceID != "dan.whitfield@example.com" ||
		m.Outgoing || m.Subject != p.Subject || m.Text != p.Text || !m.SentAt.Equal(p.Date) || m.ReplyToSourceID != "planning-3@example.com" ||
		len(m.Addresses) != 5 {
		t.Errorf("message = %+v", m)
	}
	if len(m.Attachments) != 2 || m.Attachments[0].Kind != model.AttachDocument || m.Attachments[1].Kind != model.AttachPhoto || m.Attachments[1].Size != 4 {
		t.Errorf("attachments = %+v", m.Attachments)
	}
	mine := Parse(crlf("From: Me <ME@example.org>\nSubject: x\n\nhi\n"))
	if got := mine.Message("t", "i", func(a string) bool { return a == "me@example.org" }, received); !got.Outgoing || got.SenderSourceID != "me@example.org" || !got.SentAt.Equal(received) {
		t.Errorf("own message = %+v", got)
	}
}

func TestAttachmentKind(t *testing.T) {
	for mimeType, want := range map[string]model.AttachmentKind{
		"image/jpeg": model.AttachPhoto, "IMAGE/PNG": model.AttachPhoto, "video/mp4": model.AttachVideo,
		"audio/mpeg": model.AttachAudio, "application/pdf": model.AttachDocument, "": model.AttachDocument,
	} {
		if got := AttachmentKind(mimeType); got != want {
			t.Errorf("%q: %q", mimeType, got)
		}
	}
}
