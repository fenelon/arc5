package web

import (
	"context"
	"errors"
	"sync/atomic"

	"github.com/fenelon/arc5/internal/connector"
)

// fakeConnector stands for a real connector on the Sources page: it has a
// setting of every kind, can be signed out, and logs in with two steps.
type fakeConnector struct{}

func (fakeConnector) Run(ctx context.Context, _ connector.Sink, _ connector.State, _ map[string]string) error {
	<-ctx.Done()
	return ctx.Err()
}

func (fakeConnector) Setup(state connector.State) connector.SetupFlow { return &fakeFlow{state: state} }

func (fakeConnector) Settings() []connector.Field {
	return []connector.Field{
		{Name: "speed", Label: "Speed", Help: "How fast history is fetched.", Kind: connector.FieldChoice, Choices: []string{"Slow", "Normal", "Fast"}, Default: "Normal"},
		{Name: "keep", Label: "Keep self-destructing media", Kind: connector.FieldBool},
		{Name: "months", Label: "Months of history", Kind: connector.FieldNumber, Default: "12"},
		{Name: "note", Label: "Note", Kind: connector.FieldText},
		{Name: "token", Label: "API key", Kind: connector.FieldSecret},
	}
}

func (fakeConnector) SignOut(_ context.Context, state connector.State) error {
	return state.Set("session", nil)
}

// fakeFlow asks for a phone number, then a code. The code is 12345.
type fakeFlow struct {
	state connector.State
	phone string
}

// fakeFlowsClosed counts fakeFlows closed, as a flow holding a connection would be.
var fakeFlowsClosed atomic.Int64

func (f *fakeFlow) Close() error {
	fakeFlowsClosed.Add(1)
	return nil
}

func (f *fakeFlow) Next(_ context.Context, input map[string]string) (connector.Step, error) {
	phone := connector.Step{Title: "Your phone number", Help: "With the country code.",
		Fields: []connector.Field{{Name: "phone", Label: "Phone number", Kind: connector.FieldText}}}
	code := connector.Step{Title: "The code", Help: "Sent to " + f.phone + ".",
		Fields: []connector.Field{{Name: "code", Label: "Code", Kind: connector.FieldText}, {Name: "password", Label: "Two-step password", Kind: connector.FieldSecret}}}
	switch {
	case input == nil:
		return phone, nil
	case f.phone == "":
		if input["phone"] == "" {
			return phone, errors.New("Type your phone number.")
		}
		f.phone = input["phone"]
		code.Help = "Sent to " + f.phone + "."
		return code, nil
	case input["code"] != "12345":
		return code, errors.New("That code is not right.")
	}
	if err := f.state.Set("session", []byte("signed in as "+f.phone)); err != nil {
		return code, err
	}
	return connector.Step{Done: true}, nil
}

// fakeAccount adds a source run by fakeConnector, registered as "fake".
func (s *site) fakeAccount(name string) int64 {
	s.t.Helper()
	if _, known := s.reg.Info("fake"); !known {
		s.reg.Register(connector.Info{Name: "fake", Title: "Fake", Display: connector.DisplayChats}, func() connector.Connector { return fakeConnector{} })
	}
	id, err := s.store.CreateAccount("fake", name, nil)
	if err != nil {
		s.t.Fatal(err)
	}
	return id
}
