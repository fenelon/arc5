package email

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	netmail "net/mail"
	"strconv"
	"strings"
	"time"

	"github.com/fenelon/arc5/internal/connector"
)

// keyAccount is the state key of the stored login.
const keyAccount = "account"

func loadJSON(state connector.State, key string, v any) (bool, error) {
	data, err := state.Get(key)
	if err != nil || data == nil {
		return false, err
	}
	if err := json.Unmarshal(data, v); err != nil {
		return false, fmt.Errorf("reading %s: %w", key, err)
	}
	return true, nil
}

func saveJSON(state connector.State, key string, v any) error {
	data, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return state.Set(key, data)
}

func loadAccount(state connector.State) (account, bool, error) {
	var a account
	ok, err := loadJSON(state, keyAccount, &a)
	return a, ok, err
}

const (
	stepAddress = "Your email address"
	stepServer  = "Server and password"
)

// loginTimeout bounds the test login of the setup flow.
const loginTimeout = 45 * time.Second

// Setup asks for the address, then the server and password, and tests the
// login before storing it. Logging in again starts at the second step.
func (c *Connector) Setup(state connector.State) connector.SetupFlow {
	return &setup{c: c, state: state}
}

type setup struct {
	c       *Connector
	state   connector.State
	at      string
	address string
	stored  account // the login stored before, when logging in again
}

func addressStep() connector.Step {
	return connector.Step{
		Title: stepAddress,
		Help:  "The mailbox arc5 should keep a copy of. arc5 only reads mail: it never sends, moves, deletes or marks anything as read.",
		Fields: []connector.Field{
			{Name: "address", Label: "Email address", Kind: connector.FieldText},
		},
	}
}

func (s *setup) serverStep() connector.Step {
	p := providerFor(s.address)
	host, port, username := p.host, p.port, s.address
	if s.stored.Host != "" && strings.EqualFold(s.stored.Address, s.address) {
		host, port, username = s.stored.Host, s.stored.Port, s.stored.Username
	}
	help := p.help + " The password is kept in arc5's data folder, so that folder gives access to this mailbox."
	st := connector.Step{
		Title: stepServer,
		Help:  help,
		Fields: []connector.Field{
			{Name: "host", Label: "IMAP server", Kind: connector.FieldText, Default: host},
			{Name: "port", Label: "Port", Help: "993 for TLS. Any other port is secured with STARTTLS; arc5 never logs in unencrypted.", Kind: connector.FieldNumber, Default: strconv.Itoa(port)},
			{Name: "username", Label: "User name", Help: "Usually the email address.", Kind: connector.FieldText, Default: username},
			{Name: "password", Label: "Password", Kind: connector.FieldSecret},
		},
	}
	if s.stored.Password != "" {
		st.Fields[3].Help = "Leave it empty to keep the password saved for this source."
	}
	return st
}

func (s *setup) Next(ctx context.Context, input map[string]string) (connector.Step, error) {
	if input == nil {
		stored, ok, err := loadAccount(s.state)
		if err != nil {
			return addressStep(), err
		}
		if ok && stored.Address != "" {
			s.stored, s.address, s.at = stored, stored.Address, stepServer
			return s.serverStep(), nil
		}
		s.at = stepAddress
		return addressStep(), nil
	}
	switch s.at {
	case stepAddress:
		parsed, err := netmail.ParseAddress(strings.TrimSpace(input["address"]))
		if err != nil || !strings.Contains(parsed.Address, "@") {
			return addressStep(), errors.New("Type the whole address, such as name@gmail.com.")
		}
		if providerFor(parsed.Address).oauthOnly {
			return addressStep(), errors.New("Microsoft mailboxes (Outlook.com, Hotmail, Live, Microsoft 365) no longer accept passwords from mail programs, and arc5 cannot sign in to them yet.")
		}
		s.address, s.at = parsed.Address, stepServer
		return s.serverStep(), nil
	case stepServer:
		return s.login(ctx, input)
	}
	return addressStep(), errors.New("This login has ended. Start it again.")
}

func (s *setup) login(ctx context.Context, input map[string]string) (connector.Step, error) {
	a := account{
		Address: strings.ToLower(s.address), Host: strings.TrimSpace(input["host"]),
		Username: strings.TrimSpace(input["username"]), Password: input["password"],
	}
	port, err := strconv.Atoi(strings.TrimSpace(input["port"]))
	if err != nil || port < 1 || port > 65535 {
		return s.serverStep(), errors.New("The port is a number, usually 993.")
	}
	a.Port = port
	if a.Host == "" || strings.ContainsAny(a.Host, " /:") {
		return s.serverStep(), errors.New("Type the server's name, such as imap.gmail.com.")
	}
	if a.Username == "" {
		a.Username = s.address
	}
	if a.Password == "" && s.stored.Password != "" {
		a.Password = s.stored.Password
	}
	if a.Password == "" {
		return s.serverStep(), errors.New("Type the password.")
	}
	ctx, cancel := context.WithTimeout(ctx, loginTimeout)
	defer cancel()
	c, err := s.c.dial(ctx, a)
	switch {
	case errors.Is(err, errLoginRefused):
		return s.serverStep(), errors.New(providerFor(s.address).refused)
	case err != nil:
		return s.serverStep(), fmt.Errorf("arc5 could not reach %s:%d: %v. Check the server and the port.", a.Host, a.Port, err)
	}
	c.Close()
	if err := saveJSON(s.state, keyAccount, a); err != nil {
		return s.serverStep(), err
	}
	return connector.Step{Done: true}, nil
}

// SignOut forgets the password. The address and server stay, so logging in
// again only asks for the password.
func (c *Connector) SignOut(_ context.Context, state connector.State) error {
	a, ok, err := loadAccount(state)
	if err != nil || !ok {
		return err
	}
	a.Password = ""
	return saveJSON(state, keyAccount, a)
}
