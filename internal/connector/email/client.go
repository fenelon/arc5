package email

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/textproto"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"

	"github.com/fenelon/arc5/internal/mail"
)

// account is what the setup flow stores: where the mailbox is and how to log
// in. Port 993 is implicit TLS; any other port uses STARTTLS.
type account struct {
	Address  string `json:"address"`
	Host     string `json:"host"`
	Port     int    `json:"port"`
	Username string `json:"username"`
	Password string `json:"password"`
}

// mailbox is a folder as the server lists it. Attrs are lower-cased, such as
// `\all` or `\noselect`.
type mailbox struct {
	Name  string
	Attrs []string
}

// selected is what opening a folder says about it.
type selected struct {
	UIDValidity uint32
	UIDNext     uint32
	Messages    uint32
}

// header is what a headers-only fetch tells about a message.
type header struct {
	UID       uint32
	Size      int64
	Received  time.Time
	MessageID string // normalized as the archive stores it; "" when none
}

// client is everything the connector asks of an IMAP server. It has no
// method that changes the mailbox: folders are opened read-only (EXAMINE)
// and messages are fetched with BODY.PEEK, so archiving never marks mail as
// read, moves or deletes it.
type client interface {
	List(ctx context.Context) ([]mailbox, error)
	Select(ctx context.Context, name string) (selected, error)
	// Search lists the UIDs from..to (to 0 for the last) of the selected
	// folder, received on or after since when it is set.
	Search(ctx context.Context, from, to uint32, since time.Time) ([]uint32, error)
	Headers(ctx context.Context, uids []uint32) ([]header, error)
	Fetch(ctx context.Context, uid uint32, headersOnly bool) ([]byte, error)
	// Idle waits until the server reports a change to the selected folder,
	// or wait has passed.
	Idle(ctx context.Context, wait time.Duration) error
	Close() error
}

// dialer connects and logs in.
type dialer func(ctx context.Context, a account) (client, error)

// errLoginRefused is a login the server refused: wrong password, an app
// password needed, or IMAP switched off.
var errLoginRefused = errors.New("the mail server refused the login")

// dialIMAP connects over TLS (implicit on 993, STARTTLS otherwise) with
// certificate verification, and logs in.
func dialIMAP(ctx context.Context, a account) (client, error) {
	changes := make(chan struct{}, 1)
	options := &imapclient.Options{
		TLSConfig:             &tls.Config{ServerName: a.Host, MinVersion: tls.VersionTLS12},
		Dialer:                &net.Dialer{Timeout: 30 * time.Second},
		UnilateralDataHandler: changeHandler(changes),
	}
	address := net.JoinHostPort(a.Host, strconv.Itoa(a.Port))
	var (
		c   *imapclient.Client
		err error
	)
	done := make(chan struct{})
	go func() {
		defer close(done)
		if a.Port == 993 {
			c, err = imapclient.DialTLS(address, options)
		} else {
			c, err = imapclient.DialStartTLS(address, options)
		}
	}()
	select {
	case <-done:
	case <-ctx.Done():
		go func() {
			<-done
			if c != nil {
				c.Close()
			}
		}()
		return nil, ctx.Err()
	}
	if err != nil {
		return nil, err
	}
	ic := &imapClient{c: c, changes: changes}
	if err := ic.login(ctx, a.Username, a.Password); err != nil {
		c.Close()
		return nil, err
	}
	return ic, nil
}

// changeHandler notes that the server reported new or expunged mail.
func changeHandler(changes chan struct{}) *imapclient.UnilateralDataHandler {
	note := func() {
		select {
		case changes <- struct{}{}:
		default:
		}
	}
	return &imapclient.UnilateralDataHandler{
		Expunge: func(uint32) { note() },
		Mailbox: func(data *imapclient.UnilateralDataMailbox) {
			if data.NumMessages != nil {
				note()
			}
		},
	}
}

// imapClient is client over go-imap.
type imapClient struct {
	c       *imapclient.Client
	changes chan struct{}
}

// watch closes the connection when ctx ends, which ends any command
// waiting on it.
func (ic *imapClient) watch(ctx context.Context) func() bool {
	return context.AfterFunc(ctx, func() { ic.c.Close() })
}

func (ic *imapClient) login(ctx context.Context, username, password string) error {
	defer ic.watch(ctx)()
	return ctxErr(ctx, loginError(ic.c.Login(username, password).Wait()))
}

// loginError sorts a LOGIN failure: a refusal of the credentials is
// errLoginRefused, which asks the owner to log in again; a busy, throttling
// or broken server is an error the runner retries later.
func loginError(err error) error {
	var imapErr *imap.Error
	if !errors.As(err, &imapErr) {
		return err
	}
	text := strings.ToLower(imapErr.Text)
	for _, passing := range []string{"too many", "try again", "temporar", "throttl", "rate limit", "unavailable", "later"} {
		if strings.Contains(text, passing) {
			return fmt.Errorf("the mail server is busy: %s", imapErr.Text)
		}
	}
	switch imapErr.Code {
	case "", imap.ResponseCodeAlert, imap.ResponseCodeAuthenticationFailed, imap.ResponseCodeAuthorizationFailed,
		imap.ResponseCodeContactAdmin, imap.ResponseCodeExpired:
		return fmt.Errorf("%w: %s", errLoginRefused, imapErr.Text)
	}
	return fmt.Errorf("the mail server could not log in now: %s", imapErr.Text)
}

// ctxErr prefers the context's error to the one a closed connection gave.
func ctxErr(ctx context.Context, err error) error {
	if err != nil && ctx.Err() != nil {
		return ctx.Err()
	}
	return err
}

func (ic *imapClient) List(ctx context.Context) ([]mailbox, error) {
	defer ic.watch(ctx)()
	options := &imap.ListOptions{}
	if ic.c.Caps().Has(imap.CapSpecialUse) {
		options.ReturnSpecialUse = true
	}
	list, err := ic.c.List("", "*", options).Collect()
	if err != nil {
		return nil, ctxErr(ctx, err)
	}
	var out []mailbox
	for _, data := range list {
		mb := mailbox{Name: data.Mailbox}
		skip := false
		for _, attr := range data.Attrs {
			a := strings.ToLower(string(attr))
			if a == `\noselect` || a == `\nonexistent` {
				skip = true
			}
			mb.Attrs = append(mb.Attrs, a)
		}
		if !skip {
			out = append(out, mb)
		}
	}
	return out, nil
}

func (ic *imapClient) Select(ctx context.Context, name string) (selected, error) {
	defer ic.watch(ctx)()
	data, err := ic.c.Select(name, &imap.SelectOptions{ReadOnly: true}).Wait()
	if err != nil {
		return selected{}, ctxErr(ctx, refused(err))
	}
	// What the server said while opening the folder is not a change.
	select {
	case <-ic.changes:
	default:
	}
	return selected{UIDValidity: data.UIDValidity, UIDNext: uint32(data.UIDNext), Messages: data.NumMessages}, nil
}

func (ic *imapClient) Search(ctx context.Context, from, to uint32, since time.Time) ([]uint32, error) {
	defer ic.watch(ctx)()
	if from == 0 {
		from = 1
	}
	criteria := &imap.SearchCriteria{UID: []imap.UIDSet{{imap.UIDRange{Start: imap.UID(from), Stop: imap.UID(to)}}}, Since: since}
	data, err := ic.c.UIDSearch(criteria, nil).Wait()
	if err != nil {
		return nil, ctxErr(ctx, err)
	}
	var out []uint32
	for _, uid := range data.AllUIDs() {
		// "n:*" also matches the last message when its UID is below n.
		if u := uint32(uid); u >= from && (to == 0 || u <= to) {
			out = append(out, u)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out, nil
}

func uidSet(uids []uint32) imap.UIDSet {
	var set imap.UIDSet
	for _, u := range uids {
		set.AddNum(imap.UID(u))
	}
	return set
}

var messageIDHeader = &imap.FetchItemBodySection{Specifier: imap.PartSpecifierHeader, HeaderFields: []string{"Message-ID"}, Peek: true}

func (ic *imapClient) Headers(ctx context.Context, uids []uint32) ([]header, error) {
	if len(uids) == 0 {
		return nil, nil
	}
	defer ic.watch(ctx)()
	msgs, err := ic.c.Fetch(uidSet(uids), &imap.FetchOptions{
		UID: true, RFC822Size: true, InternalDate: true, BodySection: []*imap.FetchItemBodySection{messageIDHeader},
	}).Collect()
	if err != nil {
		return nil, ctxErr(ctx, refused(err))
	}
	out := make([]header, 0, len(msgs))
	for _, m := range msgs {
		h := header{UID: uint32(m.UID), Size: m.RFC822Size, Received: m.InternalDate}
		if raw := m.FindBodySection(messageIDHeader); raw != nil {
			tp := textproto.NewReader(bufio.NewReader(bytes.NewReader(append(raw, '\r', '\n'))))
			if fields, err := tp.ReadMIMEHeader(); err == nil || len(fields) > 0 {
				h.MessageID = mail.HeaderID(fields.Get("Message-Id"))
			}
		}
		out = append(out, h)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].UID < out[j].UID })
	return out, nil
}

func (ic *imapClient) Fetch(ctx context.Context, uid uint32, headersOnly bool) ([]byte, error) {
	defer ic.watch(ctx)()
	section := &imap.FetchItemBodySection{Peek: true}
	if headersOnly {
		section.Specifier = imap.PartSpecifierHeader
	}
	msgs, err := ic.c.Fetch(uidSet([]uint32{uid}), &imap.FetchOptions{UID: true, BodySection: []*imap.FetchItemBodySection{section}}).Collect()
	if err != nil {
		return nil, ctxErr(ctx, refused(err))
	}
	for _, m := range msgs {
		if uint32(m.UID) == uid {
			if body := m.FindBodySection(section); body != nil {
				return body, nil
			}
		}
	}
	return nil, errGone
}

// errGone is a message that left the folder before it could be fetched.
var errGone = errors.New("the message is no longer in the folder")

// errServerRefused is a command the server answered with NO or BAD: the
// connection is fine, but this folder or message cannot be had now.
var errServerRefused = errors.New("the mail server refused")

// refused marks the server's NO or BAD as errServerRefused.
func refused(err error) error {
	var imapErr *imap.Error
	if errors.As(err, &imapErr) {
		return fmt.Errorf("%w: %s %s", errServerRefused, imapErr.Type, imapErr.Text)
	}
	return err
}

// pollEvery is how long a server without IDLE is left before looking again.
const pollEvery = time.Minute

// canIdle reports whether a server offers IDLE.
func canIdle(caps imap.CapSet) bool {
	return caps.Has(imap.CapIdle) || caps.Has(imap.CapIMAP4rev2)
}

func (ic *imapClient) Idle(ctx context.Context, wait time.Duration) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if !canIdle(ic.c.Caps()) {
		// No IDLE: look again after a while instead.
		t := time.NewTimer(min(wait, pollEvery))
		defer t.Stop()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-t.C:
			return nil
		}
	}
	select {
	case <-ic.changes:
		return nil // something changed while other commands ran
	default:
	}
	cmd, err := ic.c.Idle()
	if err != nil {
		return err
	}
	timer := time.NewTimer(wait)
	defer timer.Stop()
	select {
	case <-ic.changes:
	case <-timer.C:
	case <-ctx.Done():
	}
	if err := cmd.Close(); err != nil {
		return ctxErr(ctx, err)
	}
	return ctxErr(ctx, cmd.Wait())
}

func (ic *imapClient) Close() error {
	done := make(chan struct{})
	go func() {
		ic.c.Logout().Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
	}
	return ic.c.Close()
}
