package connector

import (
	"context"
	"slices"
	"testing"
)

// stubConnector is a Connector whose Run is supplied by the test.
type stubConnector struct {
	run func(ctx context.Context, sink Sink, state State, settings map[string]string) error
}

func (s stubConnector) Run(ctx context.Context, sink Sink, state State, settings map[string]string) error {
	return s.run(ctx, sink, state, settings)
}
func (stubConnector) Setup(State) SetupFlow { return nil }
func (stubConnector) Settings() []Field     { return nil }

func stubInfo(name string) Info {
	return Info{Name: name, Title: "Stub", Display: DisplayChats}
}

func TestRegistry(t *testing.T) {
	reg := NewRegistry()
	reg.Register(Info{Name: "zeta", Title: "Zeta Mail", Display: DisplayMail}, func() Connector { return stubConnector{} })
	reg.Register(stubInfo("alpha"), func() Connector { return stubConnector{} })

	if _, ok := reg.New("alpha"); !ok {
		t.Error(`New("alpha") not found`)
	}
	if _, ok := reg.New("missing"); ok {
		t.Error(`New("missing") found`)
	}
	if got, want := reg.Names(), []string{"alpha", "zeta"}; !slices.Equal(got, want) {
		t.Errorf("Names() = %v, want %v", got, want)
	}
	info, ok := reg.Info("zeta")
	if !ok || info.Title != "Zeta Mail" || info.Display != DisplayMail {
		t.Errorf(`Info("zeta") = %+v, %v`, info, ok)
	}
	if _, ok := reg.Info("missing"); ok {
		t.Error(`Info("missing") found`)
	}
}

func TestRegisterRejectsBadInfo(t *testing.T) {
	for name, info := range map[string]Info{
		"no name":    {Title: "X", Display: DisplayChats},
		"no title":   {Name: "x", Display: DisplayChats},
		"no display": {Name: "x", Title: "X"},
		"duplicate":  stubInfo("taken"),
	} {
		t.Run(name, func(t *testing.T) {
			reg := NewRegistry()
			reg.Register(stubInfo("taken"), func() Connector { return stubConnector{} })
			defer func() {
				if recover() == nil {
					t.Error("Register did not panic")
				}
			}()
			reg.Register(info, func() Connector { return stubConnector{} })
		})
	}
}

func TestInfosListsEveryConnectorByTitle(t *testing.T) {
	reg := NewRegistry()
	reg.Register(Info{Name: "zeta", Title: "Zeta", Display: DisplayChats}, func() Connector { return stubConnector{} })
	reg.Register(Info{Name: "alpha", Title: "Mail", Display: DisplayMail}, func() Connector { return stubConnector{} })
	reg.Register(Info{Name: "demo", Title: "Demo", Display: DisplayChats, Hidden: true}, func() Connector { return stubConnector{} })
	var titles []string
	for _, info := range reg.Infos() {
		titles = append(titles, info.Title)
	}
	if want := []string{"Demo", "Mail", "Zeta"}; !slices.Equal(titles, want) {
		t.Fatalf("Infos titles = %v, want %v", titles, want)
	}
}
