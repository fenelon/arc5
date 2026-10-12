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

func TestRegistry(t *testing.T) {
	reg := NewRegistry()
	reg.Register("zeta", func() Connector { return stubConnector{} })
	reg.Register("alpha", func() Connector { return stubConnector{} })

	if _, ok := reg.New("alpha"); !ok {
		t.Error(`New("alpha") not found`)
	}
	if _, ok := reg.New("missing"); ok {
		t.Error(`New("missing") found`)
	}
	if got, want := reg.Names(), []string{"alpha", "zeta"}; !slices.Equal(got, want) {
		t.Errorf("Names() = %v, want %v", got, want)
	}
}

func TestRegisterTwicePanics(t *testing.T) {
	reg := NewRegistry()
	reg.Register("alpha", func() Connector { return stubConnector{} })
	defer func() {
		if recover() == nil {
			t.Error("registering the same name twice did not panic")
		}
	}()
	reg.Register("alpha", func() Connector { return stubConnector{} })
}
