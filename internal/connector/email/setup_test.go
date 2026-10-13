package email

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"strconv"
	"strings"
	"testing"

	"github.com/fenelon/arc5/internal/connector"
)

func TestProviderFor(t *testing.T) {
	for address, want := range map[string]string{
		"me@gmail.com":        "imap.gmail.com:993 Gmail",
		"Me@GoogleMail.com":   "imap.gmail.com:993 Gmail",
		"me@fastmail.com":     "imap.fastmail.com:993 Fastmail",
		"me@icloud.com":       "imap.mail.me.com:993 iCloud",
		"me@yahoo.com":        "imap.mail.yahoo.com:993 Yahoo",
		"me@mailbox.org":      "imap.mailbox.org:993 mailbox.org",
		"me@example.org":      "imap.example.org:993 ",
		"me@mail.example.org": "imap.mail.example.org:993 ",
	} {
		p := providerFor(address)
		if got := p.host + ":" + strconv.Itoa(p.port) + " " + p.name; got != want {
			t.Errorf("%s: %q, want %q", address, got, want)
		}
	}
	if p := providerFor("me@outlook.com"); !p.oauthOnly {
		t.Error("Outlook.com is offered with a password")
	}
}

func fields(st connector.Step) map[string]connector.Field {
	out := map[string]connector.Field{}
	for _, f := range st.Fields {
		out[f.Name] = f
	}
	return out
}

func TestSetupGmail(t *testing.T) {
	srv := newFakeServer("INBOX")
	c := &Connector{dial: srv.dial}
	state := newMemState()
	flow := c.Setup(state)
	ctx := context.Background()

	st, err := flow.Next(ctx, nil)
	if err != nil || fields(st)["address"].Kind != connector.FieldText {
		t.Fatalf("first step = %+v, %v", st, err)
	}
	if _, err := flow.Next(ctx, map[string]string{"address": "not an address"}); err == nil {
		t.Error("a bad address was accepted")
	}
	if _, err := flow.Next(ctx, map[string]string{"address": "me@outlook.com"}); err == nil || !strings.Contains(err.Error(), "Microsoft") {
		t.Errorf("Outlook.com: %v", err)
	}
	st, err = flow.Next(ctx, map[string]string{"address": " Me@Gmail.com "})
	if err != nil {
		t.Fatal(err)
	}
	f := fields(st)
	if f["host"].Default != "imap.gmail.com" || f["port"].Default != "993" || f["username"].Default != "Me@Gmail.com" || f["password"].Kind != connector.FieldSecret {
		t.Errorf("server step = %+v", st.Fields)
	}
	for _, words := range []string{"app password", "myaccount.google.com/apppasswords", "2-Step Verification", "Workspace"} {
		if !strings.Contains(st.Help, words) {
			t.Errorf("Gmail help lacks %q: %s", words, st.Help)
		}
	}

	// A refused login says what to do, and nothing is stored.
	_, err = flow.Next(ctx, map[string]string{"host": "imap.gmail.com", "port": "993", "username": "Me@Gmail.com", "password": "my google password"})
	if err == nil || !strings.Contains(err.Error(), "app password") {
		t.Fatalf("refused login: %v", err)
	}
	if v, _ := state.Get(keyAccount); v != nil {
		t.Fatal("credentials stored after a refused login")
	}
	if _, err := flow.Next(ctx, map[string]string{"host": "imap.gmail.com", "port": "nope", "username": "x", "password": "p"}); err == nil {
		t.Error("a bad port was accepted")
	}
	st, err = flow.Next(ctx, map[string]string{"host": "imap.gmail.com", "port": "993", "username": "Me@Gmail.com", "password": "app-password"})
	if err != nil || !st.Done {
		t.Fatalf("login = %+v, %v", st, err)
	}
	var a account
	raw, _ := state.Get(keyAccount)
	if err := json.Unmarshal(raw, &a); err != nil {
		t.Fatal(err)
	}
	if a != (account{Address: "me@gmail.com", Host: "imap.gmail.com", Port: 993, Username: "Me@Gmail.com", Password: "app-password"}) {
		t.Errorf("stored = %+v", a)
	}
}

func TestSetupUnknownDomainAndLoggingInAgain(t *testing.T) {
	srv := newFakeServer("INBOX")
	c := &Connector{dial: srv.dial}
	state := newMemState()
	ctx := context.Background()
	flow := c.Setup(state)
	flow.Next(ctx, nil)
	st, _ := flow.Next(ctx, map[string]string{"address": "me@example.org"})
	if fields(st)["host"].Default != "imap.example.org" || strings.Contains(st.Help, "Gmail") {
		t.Errorf("unknown domain step = %+v", st)
	}
	srv.dialErr = errors.New("connection refused")
	_, err := flow.Next(ctx, map[string]string{"host": "imap.example.org", "port": "993", "username": "me", "password": "app-password"})
	if err == nil || !strings.Contains(err.Error(), "could not reach imap.example.org:993") {
		t.Errorf("unreachable: %v", err)
	}
	srv.dialErr = nil
	if st, err := flow.Next(ctx, map[string]string{"host": "imap.example.org", "port": "143", "username": "me", "password": "app-password"}); err != nil || !st.Done {
		t.Fatalf("login: %+v, %v", st, err)
	}

	// Logging in again starts at the server step, filled in, and an empty
	// password keeps the stored one.
	again := c.Setup(state)
	st, err = again.Next(ctx, nil)
	if err != nil || fields(st)["host"].Default != "imap.example.org" || fields(st)["port"].Default != "143" || fields(st)["username"].Default != "me" {
		t.Fatalf("again = %+v, %v", st, err)
	}
	if st, err := again.Next(ctx, map[string]string{"host": "imap.example.org", "port": "143", "username": "me", "password": ""}); err != nil || !st.Done {
		t.Fatalf("keeping the password: %+v, %v", st, err)
	}
}

// The stored password is kept only for the same server and user name: a
// changed host gets no password it was not given.
func TestLoggingInAgainElsewhereNeedsThePassword(t *testing.T) {
	srv := newFakeServer("INBOX")
	var dialed []account
	c := &Connector{dial: func(ctx context.Context, a account) (client, error) {
		dialed = append(dialed, a)
		return srv.dial(ctx, a)
	}}
	state := newMemState()
	saveJSON(state, keyAccount, account{Address: "me@example.org", Host: "imap.example.org", Port: 993, Username: "me", Password: "app-password"})
	ctx := context.Background()
	for _, change := range []map[string]string{
		{"host": "imap.attacker.example", "port": "993", "username": "me"},
		{"host": "imap.example.org", "port": "143", "username": "me"},
		{"host": "imap.example.org", "port": "993", "username": "someone-else"},
	} {
		flow := c.Setup(state)
		flow.Next(ctx, nil)
		change["password"] = ""
		if _, err := flow.Next(ctx, change); err == nil || !strings.Contains(err.Error(), "password") {
			t.Errorf("%v: %v", change, err)
		}
	}
	if len(dialed) != 0 {
		t.Errorf("the stored password was sent to %+v", dialed)
	}
}

func TestSignOut(t *testing.T) {
	srv := newFakeServer("INBOX")
	c := &Connector{dial: srv.dial}
	state := newMemState()
	saveJSON(state, keyAccount, account{Address: "me@example.org", Host: "imap.example.org", Port: 993, Username: "me", Password: "app-password"})
	if err := c.SignOut(context.Background(), state); err != nil {
		t.Fatal(err)
	}
	a, ok, err := loadAccount(state)
	if err != nil || !ok || a.Password != "" || a.Host != "imap.example.org" {
		t.Errorf("after signing out: %+v %v %v", a, ok, err)
	}
	if err := c.Run(context.Background(), nil, state, nil); !errors.Is(err, connector.ErrNeedsLogin) {
		t.Errorf("run without a password: %v", err)
	}
	if err := c.Run(context.Background(), nil, newMemState(), nil); !errors.Is(err, connector.ErrNeedsLogin) {
		t.Errorf("run never set up: %v", err)
	}
}

func TestRegister(t *testing.T) {
	reg := connector.NewRegistry()
	Register(reg)
	info, ok := reg.Info(Name)
	if !ok || info.Title != "Email" || info.Display != connector.DisplayMail || info.Hidden {
		t.Fatalf("info = %+v", info)
	}
	names := map[string]bool{}
	for _, f := range New().Settings() {
		names[f.Name] = true
	}
	for _, want := range []string{"age", "since", "speed", "paused", "addresses"} {
		if !names[want] {
			t.Errorf("no %q setting", want)
		}
	}
}

func TestProviderByMail(t *testing.T) {
	for want, hosts := range map[string][]string{
		"Gmail":     {"aspmx.l.google.com.", "alt1.aspmx.l.google.com."},
		"Gmail ":    {"smtp.google.com."},
		"Microsoft": {"fenelon-ru.mail.protection.outlook.com."},
		"Fastmail":  {"in1-smtp.messagingengine.com."},
		"iCloud":    {"mx01.mail.icloud.com."},
		"Yandex":    {"mx.yandex.net."},
		"Zoho":      {"mx.zoho.com."},
		"":          {"mx.example.org.", "google.com.evil.example."},
	} {
		var mx []*net.MX
		for _, h := range hosts {
			mx = append(mx, &net.MX{Host: h})
		}
		if got := providerByMail(mx).name; got != strings.TrimSpace(want) {
			t.Errorf("%v: %q, want %q", hosts, got, want)
		}
	}
}

// A domain arc5 does not know is looked up: one whose mail Google handles
// (Google Workspace) is set up as Gmail; one Microsoft handles is refused.
func TestSetupFindsWhoHandlesAnUnknownDomain(t *testing.T) {
	srv := newFakeServer("INBOX")
	mx := map[string]string{"workspace.example": "aspmx.l.google.com.", "corp.example": "corp-example.mail.protection.outlook.com."}
	c := &Connector{dial: srv.dial, lookupMX: func(ctx context.Context, domain string) ([]*net.MX, error) {
		if h, ok := mx[domain]; ok {
			return []*net.MX{{Host: h}}, nil
		}
		return nil, errors.New("no such host")
	}}
	ctx := context.Background()

	flow := c.Setup(newMemState())
	flow.Next(ctx, nil)
	st, err := flow.Next(ctx, map[string]string{"address": "me@workspace.example"})
	if err != nil || fields(st)["host"].Default != "imap.gmail.com" || !strings.Contains(st.Help, "apppasswords") {
		t.Fatalf("a Workspace domain: host %q, help %q, %v", fields(st)["host"].Default, st.Help, err)
	}

	flow = c.Setup(newMemState())
	flow.Next(ctx, nil)
	if _, err := flow.Next(ctx, map[string]string{"address": "me@corp.example"}); err == nil || !strings.Contains(err.Error(), "Microsoft") {
		t.Errorf("a Microsoft 365 domain: %v", err)
	}

	flow = c.Setup(newMemState())
	flow.Next(ctx, nil)
	st, err = flow.Next(ctx, map[string]string{"address": "me@unknown.example"})
	if err != nil || fields(st)["host"].Default != "imap.unknown.example" {
		t.Errorf("a domain that cannot be looked up: %q, %v", fields(st)["host"].Default, err)
	}
}
