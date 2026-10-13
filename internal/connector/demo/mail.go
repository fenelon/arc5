package demo

import (
	"encoding/base64"
	"fmt"
	"image/color"
	"strings"
	"time"

	"github.com/fenelon/arc5/internal/archive"
	"github.com/fenelon/arc5/internal/connector"
	"github.com/fenelon/arc5/internal/connector/email"
	"github.com/fenelon/arc5/internal/model"
)

// MailName is the connector name stored on the demo mailbox.
const MailName = "demo-mail"

// mailAddress is the demo mailbox's own address.
const mailAddress = "me@example.org"

// RegisterMail adds the demo mailbox's connector, hidden like the demo's.
func RegisterMail(reg *connector.Registry) {
	reg.Register(connector.Info{Name: MailName, Title: "Email", Display: connector.DisplayMail, Hidden: true}, func() connector.Connector { return idle{} })
}

// letter is one sample message, written as MIME so the demo goes through the
// same parser as real mail.
type letter struct {
	id, inReplyTo string
	refs          []string
	from, to, cc  string
	subject       string
	date          time.Time
	plain, html   string
	files         []sampleFile
	folders       []string
}

type sampleFile struct {
	name, mimeType string
	data           []byte
}

func (l letter) mime() []byte {
	var b strings.Builder
	header := func(k, v string) {
		if v != "" {
			fmt.Fprintf(&b, "%s: %s\r\n", k, v)
		}
	}
	header("From", l.from)
	header("To", l.to)
	header("Cc", l.cc)
	header("Subject", mimeWord(l.subject))
	header("Date", l.date.Format(time.RFC1123Z))
	header("Message-ID", "<"+l.id+">")
	if l.inReplyTo != "" {
		header("In-Reply-To", "<"+l.inReplyTo+">")
	}
	if len(l.refs) > 0 {
		header("References", "<"+strings.Join(l.refs, "> <")+">")
	}
	header("MIME-Version", "1.0")
	body := func(contentType, text string) {
		fmt.Fprintf(&b, "Content-Type: %s; charset=utf-8\r\nContent-Transfer-Encoding: base64\r\n\r\n%s\r\n", contentType, wrap64([]byte(text)))
	}
	if len(l.files) == 0 && (l.plain == "" || l.html == "") {
		if l.html != "" {
			body("text/html", l.html)
		} else {
			body("text/plain", l.plain)
		}
		return []byte(b.String())
	}
	b.WriteString("Content-Type: multipart/mixed; boundary=\"arc5-demo\"\r\n\r\n")
	b.WriteString("--arc5-demo\r\n")
	if l.html != "" {
		body("text/html", l.html)
	} else {
		body("text/plain", l.plain)
	}
	for _, f := range l.files {
		fmt.Fprintf(&b, "--arc5-demo\r\nContent-Type: %s; name=%q\r\nContent-Disposition: attachment; filename=%q\r\nContent-Transfer-Encoding: base64\r\n\r\n%s\r\n",
			f.mimeType, f.name, f.name, wrap64(f.data))
	}
	b.WriteString("--arc5-demo--\r\n")
	return []byte(b.String())
}

// mimeWord encodes a header value that is not plain ASCII.
func mimeWord(s string) string {
	for _, r := range s {
		if r > 127 {
			return "=?utf-8?B?" + base64.StdEncoding.EncodeToString([]byte(s)) + "?="
		}
	}
	return s
}

func wrap64(data []byte) string {
	enc := base64.StdEncoding.EncodeToString(data)
	var b strings.Builder
	for len(enc) > 76 {
		b.WriteString(enc[:76] + "\r\n")
		enc = enc[76:]
	}
	b.WriteString(enc)
	return b.String()
}

func on(month time.Month, d, hour, minute int) time.Time {
	return time.Date(2026, month, d, hour, minute, 0, 0, time.UTC)
}

// pdf is a small file standing for a document.
func pdf(title string, size int) []byte {
	body := "%PDF-1.4\n% " + title + "\n"
	return []byte(body + strings.Repeat(" ", max(0, size-len(body))))
}

const ticketsHTML = `<html><head><style>td{font-family:Arial}</style></head><body>
<div style="display:none;max-height:0">Your e-tickets are attached&nbsp;&zwnj;&nbsp;&zwnj;</div>
<table width="100%"><tr><td><img src="https://northline.example/logo.png" alt="North Line Rail"></td></tr>
<tr><td><h1>Your tickets for 11 July</h1>
<p>Two adults, departing 07:42. Your tickets are attached as a PDF.</p>
<table><tr><th>From</th><th>To</th><th>Departs</th></tr><tr><td>Portbury</td><td>Kingsmere</td><td>07:42</td></tr></table>
<p>Manage your booking at <a href="https://northline.example/booking/NL-4471">northline.example/booking</a>.</p>
</td></tr></table><img src="https://northline.example/o.gif" width="1" height="1"></body></html>`

const renewalHTML = `<html><body><div style="display:none">Renew before 30 November</div>
<p>Dear member,</p><p>Your membership ends on <b>30&nbsp;November</b>. Renew at the office or by post.</p>
<ul><li>Office hours: 10:00 to 13:00</li><li>By post: Quay Road 1, Portbury</li></ul>
<p>Harbour Yacht Club</p></body></html>`

// seedMail writes the demo mailbox: the conversations of the approved mail
// mockups, filed in Inbox, Sent and Receipts, one of them deleted at source.
func seedMail(store *archive.Store) error {
	acc, err := store.CreateAccount(MailName, "Home mail", nil)
	if err != nil {
		return err
	}
	sink := connector.NewSink(store, acc, nil, func(connector.Progress) {})
	if err := sink.SetAccountAddress(mailAddress); err != nil {
		return err
	}
	for _, f := range []model.Folder{
		{SourceID: "INBOX", Name: "Inbox", Role: model.FolderInbox, ArchiveByDefault: true},
		{SourceID: "Sent", Name: "Sent", Role: model.FolderSent, ArchiveByDefault: true},
		{SourceID: "Receipts", Name: "Receipts", Role: model.FolderOther, ArchiveByDefault: true},
		{SourceID: "Trash", Name: "Trash", Role: model.FolderTrash},
	} {
		if err := sink.UpsertFolder(f); err != nil {
			return err
		}
	}
	const (
		me    = "Me <me@example.org>"
		dan   = "Dan Whitfield <dan.whitfield@example.com>"
		priya = "Priya Nair <priya.nair@example.com>"
	)
	photo := func(bg, fg color.RGBA) []byte { return portrait(bg, fg) }
	letters := []letter{
		{id: "olg-1@example.com", from: "Олег Ветров <oleg.vetrov@example.com>", to: me, subject: "Отчёт о походе и расходы", date: on(time.July, 12, 19, 5),
			plain: "Привет! Выложил фото с похода. Расходы сведу к выходным.", folders: []string{"INBOX"}},
		{id: "olg-2@example.org", inReplyTo: "olg-1@example.com", refs: []string{"olg-1@example.com"}, from: me, to: "Олег Ветров <oleg.vetrov@example.com>",
			subject: "Re: Отчёт о походе и расходы", date: on(time.July, 12, 21, 30), plain: "Отлично, жду таблицу.", folders: []string{"Sent"}},
		{id: "olg-3@example.com", inReplyTo: "olg-2@example.org", refs: []string{"olg-1@example.com", "olg-2@example.org"}, from: "Олег Ветров <oleg.vetrov@example.com>", to: me,
			subject: "Re: Отчёт о походе и расходы", date: on(time.July, 14, 8, 15), plain: "Свёл расходы в таблицу, посмотрите, всё ли сходится.\n\nОлег", folders: []string{"INBOX"}},
		{id: "olg-4@example.org", inReplyTo: "olg-3@example.com", refs: []string{"olg-1@example.com", "olg-3@example.com"}, from: me, to: "Олег Ветров <oleg.vetrov@example.com>",
			subject: "Re: Отчёт о походе и расходы", date: on(time.July, 14, 9, 2), plain: "Всё сходится, спасибо!\n\n> Свёл расходы в таблицу, посмотрите, всё ли сходится.", folders: []string{"Sent"}},

		{id: "nl-4471@northline.example", from: "North Line Rail <tickets@northline.example>", to: mailAddress, subject: "Your tickets for 11 July", date: on(time.July, 2, 10, 0),
			html: ticketsHTML, files: []sampleFile{{"tickets-NL-4471.pdf", "application/pdf", pdf("Tickets NL-4471", 41_000)}}, folders: []string{"INBOX", "Receipts"}},

		{id: "val-1@example.com", from: "Marina Sokolova <marina.sokolova@example.com>", to: me, subject: "Fotos de Valencia", date: on(time.August, 31, 18, 20),
			plain: "Te mando las originales, pesan bastante. Dime si llegan bien.\n\nMarina",
			files: []sampleFile{{"valencia-playa.png", "image/png", photo(color.RGBA{0x3E, 0x7C, 0xB1, 0xFF}, color.RGBA{0xF2, 0xD7, 0x9E, 0xFF})},
				{"valencia-mercado.png", "image/png", photo(color.RGBA{0xB1, 0x5E, 0x3E, 0xFF}, color.RGBA{0xF6, 0xE7, 0xC8, 0xFF})}}, folders: []string{"INBOX"}},
		{id: "val-2@example.org", inReplyTo: "val-1@example.com", refs: []string{"val-1@example.com"}, from: me, to: "Marina Sokolova <marina.sokolova@example.com>",
			subject: "Re: Fotos de Valencia", date: on(time.August, 31, 20, 2), plain: "¡Llegaron perfectas! Gracias.", folders: []string{"Sent"}},

		{id: "cs-1@example.org", from: me, to: "Calle Sol Lettings <lettings@callesol.example>", subject: "Deposit for Calle Sol 14", date: on(time.September, 12, 9, 30),
			plain: "Hello,\n\nI sent the deposit for Calle Sol 14 this morning. Could you confirm when it arrives?\n\nThanks", folders: []string{"Sent"}},
		{id: "cs-2@callesol.example", inReplyTo: "cs-1@example.org", refs: []string{"cs-1@example.org"}, from: "Calle Sol Lettings <lettings@callesol.example>", to: me,
			subject: "RE: Deposit for Calle Sol 14", date: on(time.September, 13, 11, 5), plain: "It has arrived, thank you. The receipt follows once it is signed.", folders: []string{"INBOX"}},
		{id: "cs-3@callesol.example", inReplyTo: "cs-2@callesol.example", refs: []string{"cs-1@example.org", "cs-2@callesol.example"}, from: "Calle Sol Lettings <lettings@callesol.example>", to: me,
			subject: "Deposit receipt for Calle Sol 14", date: on(time.September, 14, 16, 40),
			plain: "Attached is the signed receipt. Keys can be collected from Thursday.\n\nLucía Ortega\nCalle Sol Lettings",
			files: []sampleFile{{"receipt-calle-sol-14.pdf", "application/pdf", pdf("Receipt", 96_000)}}, folders: []string{"INBOX", "Receipts"}},

		{id: "mp-1@example.com", from: dan, to: me + ", " + priya, subject: "Monday planning, room 4", date: on(time.September, 25, 9, 12),
			plain: "Can we move the planning session to Monday? Friday clashes with the audit.\n\nDan", folders: []string{"INBOX"}},
		{id: "mp-2@example.org", inReplyTo: "mp-1@example.com", refs: []string{"mp-1@example.com"}, from: me, to: dan + ", " + priya, subject: "Re: Monday planning, room 4",
			date: on(time.September, 25, 9, 40), plain: "Monday works. I can bring the projector if room 4 is free.\n\nOn 25 Sep 2026, at 09:12, Dan Whitfield wrote:\n> Can we move the planning session to Monday? Friday clashes with the audit.", folders: []string{"Sent"}},
		{id: "mp-3@example.com", inReplyTo: "mp-2@example.org", refs: []string{"mp-1@example.com", "mp-2@example.org"}, from: priya, to: dan + ", " + me, subject: "Re: Monday planning, room 4",
			date: on(time.September, 26, 17, 3), plain: "Fine by me, though I have to leave by noon.\n\nPriya", folders: []string{"INBOX"}},
		{id: "mp-4@example.com", inReplyTo: "mp-3@example.com", refs: []string{"mp-1@example.com", "mp-3@example.com"}, from: dan, to: me + ", " + priya, subject: "Re: Monday planning, room 4",
			date:  on(time.September, 27, 18, 40),
			plain: "Room 4 is booked from ten. It is the old one with the broken blinds, so the projector will help.\n\nAgenda attached. Can everyone confirm by Sunday evening? If Priya has to leave at noon we should take the budget first.\n\nDan",
			files: []sampleFile{{"agenda-monday.pdf", "application/pdf", pdf("Agenda", 84_000)}}, folders: []string{"INBOX"}},
		{id: "mp-5@example.org", inReplyTo: "mp-4@example.com", refs: []string{"mp-1@example.com", "mp-4@example.com"}, from: me, to: dan + ", " + priya, subject: "Re: Monday planning, room 4",
			date: on(time.September, 27, 19, 5), plain: "Confirmed. Budget first, then the room plan.\n\nI'll be there at half past nine to set up.", folders: []string{"Sent"}},

		{id: "hyc-renewal@harbour-yc.example", from: "Harbour Yacht Club <office@harbour-yc.example>", to: mailAddress, subject: "Membership renewal reminder", date: on(time.October, 1, 8, 0),
			html: renewalHTML, folders: []string{"INBOX"}},

		{id: "cena-1@example.org", from: me, to: "Pablo Ruiz <pablo.ruiz@example.com>", subject: "Cena el sábado", date: on(time.October, 3, 12, 10),
			plain: "¿Os apetece cenar el sábado?", folders: []string{"Sent"}},
		{id: "cena-2@example.com", inReplyTo: "cena-1@example.org", refs: []string{"cena-1@example.org"}, from: "Pablo Ruiz <pablo.ruiz@example.com>", to: me,
			subject: "Re: Cena el sábado", date: on(time.October, 3, 18, 2), plain: "¿A las nueve en casa? Yo llevo el postre y el vino.", folders: []string{"INBOX"}},
	}
	own := func(address string) bool { return address == mailAddress }
	next := map[string]uint32{}  // the next UID of each folder
	inbox := map[string]uint32{} // message ID → its UID in the Inbox
	for _, l := range letters {
		chat, id, err := email.Store(sink, l.mime(), l.date, own)
		if err != nil {
			return fmt.Errorf("demo mail %s: %w", l.id, err)
		}
		for _, folder := range l.folders {
			next[folder]++
			if folder == "INBOX" {
				inbox[l.id] = next[folder]
			}
			if err := sink.FileInFolder(folder, next[folder], chat, id); err != nil {
				return err
			}
		}
	}
	// The club's reminder and one of Priya's messages were deleted in the
	// mailbox: they left the Inbox, their only folder.
	return sink.LeaveFolder("INBOX", []uint32{inbox["hyc-renewal@harbour-yc.example"], inbox["mp-3@example.com"]})
}
