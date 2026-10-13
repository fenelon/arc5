package telegram

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/gotd/td/telegram/auth"
	"github.com/gotd/td/tgerr"

	"github.com/fenelon/arc5/internal/connector"
)

const keySelf = "self"

// loadSelf is the Telegram user this source archives, or 0 before its
// first login.
func loadSelf(state connector.State) (int64, error) {
	var id int64
	_, err := loadJSON(state, keySelf, &id)
	return id, err
}

func saveSelf(state connector.State, id int64) error { return saveJSON(state, keySelf, id) }

// Setup walks the owner through logging in: API keys, phone number, login
// code, and the two-step password when the account has one. One connection
// is kept open between steps; the interface closes the flow when it ends.
func (c *Connector) Setup(state connector.State) connector.SetupFlow {
	return &setup{c: c, state: state}
}

type setup struct {
	c     *Connector
	state connector.State
	conn  conn
	creds credentials
	at    string // the step on screen
	phone string
	hash  string
}

const (
	stepKeys     = "Your Telegram API keys"
	stepPhone    = "Your phone number"
	stepCode     = "The login code"
	stepPassword = "Your two-step password"
)

var apiHashPattern = regexp.MustCompile(`^[0-9a-fA-F]{32}$`)

func (s *setup) keysStep() connector.Step {
	st := connector.Step{
		Title: stepKeys,
		Help: "arc5 signs in as you with API keys of your own. Create them at my.telegram.org under " +
			"\"API development tools\" (any app name will do), then copy the App api_id and App api_hash here.",
		Fields: []connector.Field{
			{Name: "api_id", Label: "API ID", Kind: connector.FieldNumber},
			{Name: "api_hash", Label: "API hash", Kind: connector.FieldSecret},
		},
	}
	if stored, ok, _ := loadCredentials(s.state); ok {
		st.Fields[0].Default = strconv.Itoa(stored.ID)
		st.Fields[1].Help = "Leave it empty to keep the hash saved for this source."
	}
	return st
}

func phoneStep() connector.Step {
	return connector.Step{
		Title: stepPhone, Help: "The number of the Telegram account to archive, with its country code, such as +44 20 7946 0000.",
		Fields: []connector.Field{{Name: "phone", Label: "Phone number", Kind: connector.FieldText}},
	}
}

func codeStep() connector.Step {
	return connector.Step{
		Title: stepCode, Help: "Telegram has sent a code to your other devices, or by text message. arc5 will appear in your list of active sessions.",
		Fields: []connector.Field{{Name: "code", Label: "Login code", Kind: connector.FieldText}},
	}
}

func passwordStep() connector.Step {
	return connector.Step{
		Title: stepPassword, Help: "This account has a two-step password (cloud password). arc5 uses it once and does not keep it.",
		Fields: []connector.Field{{Name: "password", Label: "Two-step password", Kind: connector.FieldSecret}},
	}
}

func (s *setup) Next(ctx context.Context, input map[string]string) (connector.Step, error) {
	if input == nil || s.at == "" {
		s.at = stepKeys
		return s.keysStep(), nil
	}
	switch s.at {
	case stepKeys:
		return s.keys(ctx, input)
	case stepPhone:
		return s.sendCode(ctx, strings.TrimSpace(input["phone"]))
	case stepCode:
		return s.signIn(ctx, strings.TrimSpace(input["code"]))
	case stepPassword:
		if err := s.conn.CheckPassword(ctx, input["password"]); err != nil {
			if errors.Is(err, auth.ErrPasswordInvalid) || tgerr.Is(err, "PASSWORD_HASH_INVALID") {
				return connector.Step{}, errors.New("That password is not right. It is the cloud password set in Telegram's privacy settings.")
			}
			return connector.Step{}, explain(err)
		}
		return s.finish(ctx)
	}
	return connector.Step{}, errors.New("this login has ended; start again")
}

func (s *setup) keys(ctx context.Context, input map[string]string) (connector.Step, error) {
	id, err := strconv.Atoi(strings.TrimSpace(input["api_id"]))
	if err != nil || id <= 0 {
		return connector.Step{}, errors.New("The API ID is a number, such as 1234567. Copy it from my.telegram.org.")
	}
	hash := strings.TrimSpace(input["api_hash"])
	if hash == "" {
		if stored, ok, _ := loadCredentials(s.state); ok && stored.ID == id {
			hash = stored.Hash
		}
	}
	if !apiHashPattern.MatchString(hash) {
		return connector.Step{}, errors.New("The API hash is 32 letters and digits. Copy it from my.telegram.org.")
	}
	s.creds = credentials{ID: id, Hash: hash}
	if s.conn != nil {
		s.conn.Close()
		s.conn = nil
	}
	conn, err := s.c.dial(ctx, s.creds, s.state, listener{})
	if err != nil {
		return connector.Step{}, fmt.Errorf("arc5 could not reach Telegram (%v). Check this computer's network connection and try again.", err)
	}
	s.conn, s.at = conn, stepPhone
	return phoneStep(), nil
}

func (s *setup) sendCode(ctx context.Context, phone string) (connector.Step, error) {
	if phone == "" {
		return connector.Step{}, errors.New("Type the account's phone number.")
	}
	hash, err := s.conn.SendCode(ctx, phone)
	switch {
	case errors.Is(err, errSignedIn):
		return s.finish(ctx)
	case tgerr.Is(err, "PHONE_NUMBER_INVALID"):
		return connector.Step{}, errors.New("Telegram does not know that number. Include the country code, such as +44.")
	case tgerr.Is(err, "PHONE_NUMBER_BANNED"):
		return connector.Step{}, errors.New("Telegram has banned this number.")
	case tgerr.Is(err, "API_ID_INVALID"):
		return connector.Step{}, errors.New("Telegram does not accept these API keys. Cancel the login and check them at my.telegram.org.")
	case err != nil:
		return connector.Step{}, explain(err)
	}
	s.phone, s.hash, s.at = phone, hash, stepCode
	return codeStep(), nil
}

func (s *setup) signIn(ctx context.Context, code string) (connector.Step, error) {
	if code == "" {
		return connector.Step{}, errors.New("Type the code Telegram sent.")
	}
	err := s.conn.SignIn(ctx, s.phone, code, s.hash)
	var signUp *auth.SignUpRequired
	switch {
	case errors.Is(err, errPasswordNeeded):
		s.at = stepPassword
		return passwordStep(), nil
	case tgerr.Is(err, "PHONE_CODE_INVALID", "PHONE_CODE_EMPTY"):
		return connector.Step{}, errors.New("That code is not right. Type the latest code Telegram sent.")
	case tgerr.Is(err, "PHONE_CODE_EXPIRED"):
		hash, sendErr := s.conn.SendCode(ctx, s.phone)
		if sendErr != nil {
			return connector.Step{}, explain(sendErr)
		}
		s.hash = hash
		return connector.Step{}, errors.New("That code has expired, so Telegram has sent a new one. Type the new code.")
	case errors.As(err, &signUp):
		return connector.Step{}, errors.New("This phone number has no Telegram account, and arc5 does not create accounts.")
	case err != nil:
		return connector.Step{}, explain(err)
	}
	return s.finish(ctx)
}

// finish checks who signed in and keeps the keys. A source archives one
// Telegram account for good, so logging in as another is refused.
func (s *setup) finish(ctx context.Context) (connector.Step, error) {
	me, err := s.conn.Self(ctx)
	if err != nil {
		return connector.Step{}, explain(err)
	}
	if known, err := loadSelf(s.state); err != nil {
		return connector.Step{}, err
	} else if known != 0 && known != me.ID {
		// End the session just made for the other account, at Telegram and here.
		_ = s.conn.LogOut(ctx)
		if err := s.state.Set(keySession, nil); err != nil {
			return connector.Step{}, err
		}
		s.Close()
		s.at = ""
		return connector.Step{}, errors.New("This source archives another Telegram account. Log in with that account, or add a new source for this one.")
	}
	if err := saveCredentials(s.state, s.creds); err != nil {
		return connector.Step{}, err
	}
	if err := saveSelf(s.state, me.ID); err != nil {
		return connector.Step{}, err
	}
	s.at = "done"
	return connector.Step{Done: true}, nil
}

// Close ends the flow's connection.
func (s *setup) Close() error {
	if s.conn != nil {
		s.conn.Close()
		s.conn = nil
	}
	return nil
}

// explain turns a Telegram error into something the owner can act on.
func explain(err error) error {
	if d, ok := floodWait(err); ok {
		return fmt.Errorf("Telegram asks arc5 to wait %s before trying again.", humanDuration(d))
	}
	return fmt.Errorf("Telegram refused: %v", err)
}

// humanDuration says a wait like "2 min" or "45 s".
func humanDuration(d time.Duration) string {
	switch {
	case d >= time.Hour:
		return fmt.Sprintf("%d h %d min", int(d.Hours()), int(d.Minutes())%60)
	case d >= time.Minute:
		return fmt.Sprintf("%d min", int(d.Round(time.Minute).Minutes()))
	}
	return fmt.Sprintf("%d s", int(d.Seconds()))
}

// SignOut ends arc5's session at Telegram if it can, and always forgets it
// here, so the source needs a login to run again.
func (c *Connector) SignOut(ctx context.Context, state connector.State) error {
	if creds, ok, err := loadCredentials(state); err == nil && ok {
		if session, _ := state.Get(keySession); session != nil {
			waitCtx, cancel := context.WithTimeout(ctx, c.signOutWait)
			if conn, err := c.dial(waitCtx, creds, state, listener{}); err == nil {
				_ = conn.LogOut(waitCtx) // a session Telegram already ended is just as signed out
				conn.Close()
			}
			cancel()
		}
	}
	return state.Set(keySession, nil)
}
