package telegram

import (
	"context"
	"errors"
	"slices"
	"sync"
	"testing"

	"github.com/gotd/td/session"
	"github.com/gotd/td/telegram/updates"
)

// memState is a connector.State in memory.
type memState struct {
	mu sync.Mutex
	m  map[string][]byte
}

func newMemState() *memState { return &memState{m: map[string][]byte{}} }

func (s *memState) Get(key string) ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.m[key]), nil
}

func (s *memState) Set(key string, value []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if value == nil {
		delete(s.m, key)
	} else {
		s.m[key] = slices.Clone(value)
	}
	return nil
}

func TestSessionStorage(t *testing.T) {
	ctx := context.Background()
	st := sessionStorage{newMemState()}
	if _, err := st.LoadSession(ctx); !errors.Is(err, session.ErrNotFound) {
		t.Fatalf("an empty session: %v", err)
	}
	if err := st.StoreSession(ctx, []byte("key")); err != nil {
		t.Fatal(err)
	}
	if got, err := st.LoadSession(ctx); err != nil || string(got) != "key" {
		t.Fatalf("LoadSession = %q, %v", got, err)
	}
}

func TestUpdateStorage(t *testing.T) {
	ctx := context.Background()
	st := newUpdateStorage(newMemState())
	var (
		_ updates.StateStorage        = st
		_ updates.ChannelAccessHasher = st
		_ updates.UserAccessHasher    = st
	)
	if _, found, err := st.GetState(ctx, 1); found || err != nil {
		t.Fatalf("no state yet: %v, %v", found, err)
	}
	if err := st.SetPts(ctx, 1, 5); err == nil {
		t.Error("SetPts before any state must fail, as gotd expects")
	}
	if err := st.SetState(ctx, 1, updates.State{Pts: 1, Qts: 2, Date: 3, Seq: 4}); err != nil {
		t.Fatal(err)
	}
	for _, set := range []func() error{
		func() error { return st.SetPts(ctx, 1, 10) },
		func() error { return st.SetQts(ctx, 1, 20) },
		func() error { return st.SetDateSeq(ctx, 1, 30, 40) },
	} {
		if err := set(); err != nil {
			t.Fatal(err)
		}
	}
	if got, found, err := st.GetState(ctx, 1); !found || err != nil || got != (updates.State{Pts: 10, Qts: 20, Date: 30, Seq: 40}) {
		t.Errorf("state = %+v, %v, %v", got, found, err)
	}

	for id, pts := range map[int64]int{7: 70, 8: 80} {
		if err := st.SetChannelPts(ctx, 1, id, pts); err != nil {
			t.Fatal(err)
		}
	}
	if pts, found, err := st.GetChannelPts(ctx, 1, 7); !found || err != nil || pts != 70 {
		t.Errorf("channel 7 pts = %d, %v, %v", pts, found, err)
	}
	if _, found, _ := st.GetChannelPts(ctx, 1, 9); found {
		t.Error("an unknown channel has a pts")
	}
	seen := map[int64]int{}
	if err := st.ForEachChannels(ctx, 1, func(_ context.Context, id int64, pts int) error { seen[id] = pts; return nil }); err != nil || len(seen) != 2 || seen[8] != 80 {
		t.Errorf("ForEachChannels = %v, %v", seen, err)
	}

	if err := st.SetChannelAccessHash(ctx, 1, 7, 777); err != nil {
		t.Fatal(err)
	}
	if err := st.SetUserAccessHash(ctx, 1, 3, 333); err != nil {
		t.Fatal(err)
	}
	if h, found, err := st.GetChannelAccessHash(ctx, 1, 7); !found || err != nil || h != 777 {
		t.Errorf("channel hash = %d, %v, %v", h, found, err)
	}
	if h, found, err := st.GetUserAccessHash(ctx, 1, 3); !found || err != nil || h != 333 {
		t.Errorf("user hash = %d, %v, %v", h, found, err)
	}
	if _, found, _ := st.GetUserAccessHash(ctx, 1, 4); found {
		t.Error("an unknown user has a hash")
	}
}

func TestCheckpointsAndCredentials(t *testing.T) {
	state := newMemState()
	if c, err := loadCheckpoint(state, "user:1"); err != nil || c != (checkpoint{}) {
		t.Fatalf("no checkpoint yet = %+v, %v", c, err)
	}
	if err := saveCheckpoint(state, "user:1", checkpoint{Oldest: 500, OldestDate: 1700000000, Start: true}); err != nil {
		t.Fatal(err)
	}
	if c, _ := loadCheckpoint(state, "user:1"); c != (checkpoint{Oldest: 500, OldestDate: 1700000000, Start: true}) {
		t.Errorf("checkpoint = %+v", c)
	}
	if top, err := loadTop(state, "user:1"); err != nil || top != 0 {
		t.Fatalf("no top yet = %d, %v", top, err)
	}
	for _, id := range []int{40, 90, 60} {
		if err := raiseTop(state, "user:1", id); err != nil {
			t.Fatal(err)
		}
	}
	if top, _ := loadTop(state, "user:1"); top != 90 {
		t.Errorf("top = %d, want the highest seen", top)
	}
	if _, ok, err := loadCredentials(state); ok || err != nil {
		t.Fatalf("no credentials yet: %v, %v", ok, err)
	}
	if err := saveCredentials(state, credentials{ID: 12345, Hash: "abc"}); err != nil {
		t.Fatal(err)
	}
	if c, ok, err := loadCredentials(state); !ok || err != nil || c != (credentials{ID: 12345, Hash: "abc"}) {
		t.Errorf("credentials = %+v, %v, %v", c, ok, err)
	}
	if err := state.Set(keyCredentials, []byte("not json")); err != nil {
		t.Fatal(err)
	}
	if _, _, err := loadCredentials(state); err == nil {
		t.Error("damaged credentials were read")
	}
}
