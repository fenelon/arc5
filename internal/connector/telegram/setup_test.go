package telegram

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/gotd/td/telegram/auth"
	"github.com/gotd/td/tg"
	"github.com/gotd/td/tgerr"

	"github.com/fenelon/arc5/internal/connector"
)

func newTestConnector(d *fakeDialer) *Connector {
	c := New()
	c.dial = d.dial
	return c
}

// step submits input and fails the test unless the flow moves to want.
func step(t *testing.T, flow connector.SetupFlow, input map[string]string, want string) connector.Step {
	t.Helper()
	got, err := flow.Next(context.Background(), input)
	if err != nil {
		t.Fatalf("Next(%v): %v", input, err)
	}
	if want == "done" {
		if !got.Done {
			t.Fatalf("Next(%v) = %q, want the end", input, got.Title)
		}
		return got
	}
	if got.Done || got.Title != want {
		t.Fatalf("Next(%v) = %q (done %v), want %q", input, got.Title, got.Done, want)
	}
	return got
}

func refused(t *testing.T, flow connector.SetupFlow, input map[string]string, says string) {
	t.Helper()
	_, err := flow.Next(context.Background(), input)
	if err == nil || !strings.Contains(err.Error(), says) {
		t.Fatalf("Next(%v) = %v, want a refusal saying %q", input, err, says)
	}
}

func TestLoginWithoutAPassword(t *testing.T) {
	d := &fakeDialer{conn: &fakeConn{}}
	state := newMemState()
	flow := newTestConnector(d).Setup(state)
	first := step(t, flow, nil, "Your Telegram API keys")
	if len(first.Fields) != 2 || first.Fields[0].Kind != connector.FieldNumber || first.Fields[1].Kind != connector.FieldSecret ||
		!strings.Contains(first.Help, "my.telegram.org") {
		t.Errorf("the keys step = %+v", first)
	}
	refused(t, flow, map[string]string{"api_id": "abc", "api_hash": "0123456789abcdef0123456789abcdef"}, "API ID")
	refused(t, flow, map[string]string{"api_id": "123", "api_hash": ""}, "API hash")
	step(t, flow, map[string]string{"api_id": "123", "api_hash": "0123456789abcdef0123456789abcdef"}, "Your phone number")
	if len(d.dials) != 1 || d.dials[0] != (credentials{ID: 123, Hash: "0123456789abcdef0123456789abcdef"}) || d.listener.handler != nil {
		t.Errorf("dials = %+v, listener %+v", d.dials, d.listener)
	}
	step(t, flow, map[string]string{"phone": "+44 20 7946 0000"}, "The login code")
	step(t, flow, map[string]string{"code": "12345"}, "done")
	if c, ok, _ := loadCredentials(state); !ok || c.ID != 123 {
		t.Errorf("credentials not kept: %+v", c)
	}
	if id, _ := loadSelf(state); id != self {
		t.Errorf("the account's user = %d", id)
	}
	if d.conn.closed != 0 {
		t.Error("the flow closed its connection before the interface did")
	}
	if c, ok := flow.(interface{ Close() error }); !ok || c.Close() != nil || d.conn.closed != 1 {
		t.Errorf("Close: %d closes", d.conn.closed)
	}
}

func TestLoginWithAPasswordAndRefusals(t *testing.T) {
	fc := &fakeConn{
		sendCode: func(phone string) (string, error) {
			if phone == "123" {
				return "", tgerr.New(400, "PHONE_NUMBER_INVALID")
			}
			if phone == "+1 555 0100" {
				return "", tgerr.New(420, "FLOOD_WAIT_120")
			}
			return "hash", nil
		},
		signIn: func(code string) error {
			switch code {
			case "000":
				return tgerr.New(400, "PHONE_CODE_INVALID")
			case "111":
				return tgerr.New(400, "PHONE_CODE_EXPIRED")
			}
			return errPasswordNeeded
		},
		password: func(pw string) error {
			if pw != "correct horse" {
				return auth.ErrPasswordInvalid
			}
			return nil
		},
	}
	state := newMemState()
	flow := newTestConnector(&fakeDialer{conn: fc}).Setup(state)
	step(t, flow, nil, "Your Telegram API keys")
	step(t, flow, map[string]string{"api_id": "1", "api_hash": "0123456789abcdef0123456789abcdef"}, "Your phone number")
	refused(t, flow, map[string]string{"phone": ""}, "phone number")
	refused(t, flow, map[string]string{"phone": "123"}, "country code")
	refused(t, flow, map[string]string{"phone": "+1 555 0100"}, "2 min")
	step(t, flow, map[string]string{"phone": "+34 600 000 000"}, "The login code")
	refused(t, flow, map[string]string{"code": "000"}, "not right")
	refused(t, flow, map[string]string{"code": "111"}, "sent a new one")
	if fc.calls[len(fc.calls)-1] != "sendCode +34 600 000 000" {
		t.Errorf("an expired code did not ask for a new one: %v", fc.calls)
	}
	pw := step(t, flow, map[string]string{"code": "22222"}, "Your two-step password")
	if len(pw.Fields) != 1 || pw.Fields[0].Kind != connector.FieldSecret {
		t.Errorf("the password step = %+v", pw)
	}
	refused(t, flow, map[string]string{"password": "wrong"}, "not right")
	step(t, flow, map[string]string{"password": "correct horse"}, "done")
}

func TestLoginRefusesAnotherAccount(t *testing.T) {
	state := newMemState()
	if err := saveSelf(state, 42); err != nil {
		t.Fatal(err)
	}
	if err := saveCredentials(state, credentials{ID: 9, Hash: "0123456789abcdef0123456789abcdef"}); err != nil {
		t.Fatal(err)
	}
	fc := &fakeConn{}
	flow := newTestConnector(&fakeDialer{conn: fc}).Setup(state)
	keys := step(t, flow, nil, "Your Telegram API keys")
	if keys.Fields[0].Default != "9" || !strings.Contains(keys.Fields[1].Help, "empty") {
		t.Errorf("stored keys are offered: %+v", keys.Fields)
	}
	step(t, flow, map[string]string{"api_id": "9", "api_hash": ""}, "Your phone number")
	step(t, flow, map[string]string{"phone": "+34 600"}, "The login code")
	state.Set(keySession, []byte("the other account's session")) // as the login connection stores it
	refused(t, flow, map[string]string{"code": "12345"}, "another Telegram account")
	// Review finding: the other account's live session stayed behind.
	if v, _ := state.Get(keySession); v != nil || fc.loggedOut != 1 {
		t.Errorf("after refusing another account: session %q, logged out %d times", v, fc.loggedOut)
	}
}

func TestLoginWhenTelegramCannotBeReached(t *testing.T) {
	flow := newTestConnector(&fakeDialer{err: errors.New("dial tcp: no route")}).Setup(newMemState())
	step(t, flow, nil, "Your Telegram API keys")
	refused(t, flow, map[string]string{"api_id": "1", "api_hash": "0123456789abcdef0123456789abcdef"}, "could not reach Telegram")
}

func TestSignOut(t *testing.T) {
	fc := &fakeConn{}
	state := newMemState()
	for k, v := range map[string]string{keySession: "secret", keyCredentials: `{"id":1,"hash":"h"}`} {
		state.Set(k, []byte(v))
	}
	c := newTestConnector(&fakeDialer{conn: fc})
	if err := c.SignOut(context.Background(), state); err != nil {
		t.Fatal(err)
	}
	if v, _ := state.Get(keySession); v != nil || fc.loggedOut != 1 || fc.closed != 1 {
		t.Errorf("session %q, logged out %d, closed %d", v, fc.loggedOut, fc.closed)
	}
	// Telegram unreachable: the session is still forgotten here.
	state.Set(keySession, []byte("secret"))
	c = newTestConnector(&fakeDialer{err: errors.New("offline")})
	c.signOutWait = 10 * time.Millisecond
	if err := c.SignOut(context.Background(), state); err != nil {
		t.Fatal(err)
	}
	if v, _ := state.Get(keySession); v != nil {
		t.Error("the session survived a sign-out while offline")
	}
	_ = tg.User{}
}
