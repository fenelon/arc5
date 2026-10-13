// Package email archives mailboxes over IMAP: login with a password or app
// password, the chosen folders (each message stored once), new mail and
// deletions. It reads only: no command it sends can change the mailbox.
package email

import (
	"context"
	"errors"
	"net"
	"strings"
	"time"

	"github.com/fenelon/arc5/internal/connector"
)

// Connector archives one mailbox per run.
type Connector struct {
	dial  dialer
	now   func() time.Time
	sleep func(ctx context.Context, d time.Duration) error
	// slice is how long a cycle spends on history before looking for new
	// mail again; wait is how long it waits for news when history is done.
	slice, wait time.Duration
	afterCycle  func() // for tests: called when a cycle ends
	// lookupMX finds who receives a domain's mail, so a custom domain on a
	// known service is set up as that service. Nil skips the lookup.
	lookupMX func(ctx context.Context, domain string) ([]*net.MX, error)
}

// New is the connector with a real IMAP client.
func New() *Connector {
	return &Connector{dial: dialIMAP, now: time.Now, sleep: sleep, slice: time.Minute, wait: 5 * time.Minute,
		lookupMX: net.DefaultResolver.LookupMX}
}

// Register adds the email connector.
func Register(reg *connector.Registry) {
	reg.Register(connector.Info{Name: Name, Title: "Email", Display: connector.DisplayMail}, func() connector.Connector { return New() })
}

func sleep(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return ctx.Err()
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// ages are the choices for how far back history is fetched.
var ages = []string{"All history", "Last 5 years", "Last year", "Last 3 months", "Since a date"}

// speeds are the pauses between two batches of history.
var speeds = map[string]time.Duration{"Slow": 3 * time.Second, "Normal": 500 * time.Millisecond, "Fast": 0}

// Settings are the per-account options on the Sources page.
func (c *Connector) Settings() []connector.Field {
	return []connector.Field{
		{Name: "age", Label: "How far back", Help: "Mail received before this is not fetched. Mail already archived stays.",
			Kind: connector.FieldChoice, Choices: ages, Default: ages[0]},
		{Name: "since", Label: "Since", Help: "For \"Since a date\": the first day to fetch, as 2024-03-01.", Kind: connector.FieldText},
		{Name: "speed", Label: "Speed", Help: "How quickly old mail is fetched. New mail is saved within a minute, whatever this is.",
			Kind: connector.FieldChoice, Choices: []string{"Slow", "Normal", "Fast"}, Default: "Normal"},
		{Name: "paused", Label: "Pause catch-up", Help: "Stop fetching old mail for now. New mail is still saved.", Kind: connector.FieldBool},
		{Name: "addresses", Label: "Your other addresses", Help: "Mail from these addresses counts as yours, as mail from the mailbox's own address does. Separate them with commas.",
			Kind: connector.FieldText},
	}
}

// options are an account's settings, read once per run.
type options struct {
	since  time.Time // mail received before this is not fetched; zero for all
	pause  time.Duration
	paused bool
	own    map[string]bool // lower-cased addresses whose mail is the owner's
}

func readOptions(settings map[string]string, address string, now time.Time) options {
	o := options{pause: speeds["Normal"], paused: settings["paused"] == "true", own: map[string]bool{strings.ToLower(address): true}}
	if d, ok := speeds[settings["speed"]]; ok {
		o.pause = d
	}
	today := now.UTC().Truncate(24 * time.Hour)
	switch settings["age"] {
	case "Last 5 years":
		o.since = today.AddDate(-5, 0, 0)
	case "Last year":
		o.since = today.AddDate(-1, 0, 0)
	case "Last 3 months":
		o.since = today.AddDate(0, -3, 0)
	case "Since a date":
		if day, err := time.Parse("2006-01-02", strings.TrimSpace(settings["since"])); err == nil {
			o.since = day
		}
	}
	for _, a := range strings.Split(settings["addresses"], ",") {
		if a = strings.ToLower(strings.TrimSpace(a)); a != "" {
			o.own[a] = true
		}
	}
	return o
}

// Run archives the mailbox until ctx ends. A missing or refused login is
// connector.ErrNeedsLogin; other failures are returned for the runner to
// retry.
func (c *Connector) Run(ctx context.Context, sink connector.Sink, state connector.State, settings map[string]string) error {
	a, ok, err := loadAccount(state)
	if err != nil {
		return err
	}
	if !ok || a.Password == "" {
		return connector.ErrNeedsLogin
	}
	mailSink, ok := sink.(connector.MailSink)
	if !ok {
		return errors.New("email: the archive cannot hold mail")
	}
	return c.run(ctx, mailSink, state, a, readOptions(settings, a.Address, c.now()))
}
