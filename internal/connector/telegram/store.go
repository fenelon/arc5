package telegram

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"sync"

	"github.com/gotd/td/session"
	"github.com/gotd/td/telegram/updates"

	"github.com/fenelon/arc5/internal/connector"
)

// Keys in the connector's State. The session grants full access to the
// account; it is kept nowhere else.
const (
	keySession     = "session"
	keyCredentials = "api"
	keyUpdates     = "updates/state"
	keyChannelPts  = "updates/channels"
)

// sessionStorage keeps gotd's session in the connector's State.
type sessionStorage struct{ state connector.State }

func (s sessionStorage) LoadSession(context.Context) ([]byte, error) {
	data, err := s.state.Get(keySession)
	if err == nil && data == nil {
		return nil, session.ErrNotFound
	}
	return data, err
}

func (s sessionStorage) StoreSession(_ context.Context, data []byte) error {
	return s.state.Set(keySession, data)
}

// credentials are the owner's API keys from my.telegram.org.
type credentials struct {
	ID   int    `json:"id"`
	Hash string `json:"hash"`
}

func loadCredentials(state connector.State) (credentials, bool, error) {
	var c credentials
	ok, err := loadJSON(state, keyCredentials, &c)
	return c, ok, err
}

func saveCredentials(state connector.State, c credentials) error {
	return saveJSON(state, keyCredentials, c)
}

// checkpoint is how far back a chat's history has been fetched: the oldest
// message ID stored, and whether the age limit or the chat's start was reached.
type checkpoint struct {
	Oldest int  `json:"oldest"`
	Done   bool `json:"done"`
}

func loadCheckpoint(state connector.State, chat string) (checkpoint, error) {
	var c checkpoint
	_, err := loadJSON(state, "backfill/"+chat, &c)
	return c, err
}

func saveCheckpoint(state connector.State, chat string, c checkpoint) error {
	return saveJSON(state, "backfill/"+chat, c)
}

// topMu keeps two updates from lowering each other's top.
var topMu sync.Mutex

// loadTop is the newest message ID stored for a chat, from which a catch-up
// after a long disconnection starts.
func loadTop(state connector.State, chat string) (int, error) {
	var top int
	_, err := loadJSON(state, "top/"+chat, &top)
	return top, err
}

// raiseTop records a stored message ID if it is newer than the top.
func raiseTop(state connector.State, chat string, id int) error {
	topMu.Lock()
	defer topMu.Unlock()
	top, err := loadTop(state, chat)
	if err != nil || id <= top {
		return err
	}
	return saveJSON(state, "top/"+chat, id)
}

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

// updateStorage keeps gotd's update state and the access hashes it learns in
// the connector's State, so a restart resumes where it stopped. One account
// is one Telegram user, so the user ID gotd passes is not part of the keys.
type updateStorage struct {
	state connector.State
	mu    sync.Mutex // read-modify-write of the state and the channel map
}

func newUpdateStorage(state connector.State) *updateStorage { return &updateStorage{state: state} }

var errNoState = errors.New("no update state stored yet")

func (s *updateStorage) GetState(_ context.Context, _ int64) (updates.State, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var st updates.State
	found, err := loadJSON(s.state, keyUpdates, &st)
	return st, found, err
}

func (s *updateStorage) SetState(_ context.Context, _ int64, st updates.State) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return saveJSON(s.state, keyUpdates, st)
}

// change applies f to the stored state; gotd expects an error when there is none.
func (s *updateStorage) change(f func(*updates.State)) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	var st updates.State
	found, err := loadJSON(s.state, keyUpdates, &st)
	if err != nil {
		return err
	}
	if !found {
		return errNoState
	}
	f(&st)
	return saveJSON(s.state, keyUpdates, st)
}

func (s *updateStorage) SetPts(_ context.Context, _ int64, pts int) error {
	return s.change(func(st *updates.State) { st.Pts = pts })
}

func (s *updateStorage) SetQts(_ context.Context, _ int64, qts int) error {
	return s.change(func(st *updates.State) { st.Qts = qts })
}

func (s *updateStorage) SetDate(_ context.Context, _ int64, date int) error {
	return s.change(func(st *updates.State) { st.Date = date })
}

func (s *updateStorage) SetSeq(_ context.Context, _ int64, seq int) error {
	return s.change(func(st *updates.State) { st.Seq = seq })
}

func (s *updateStorage) SetDateSeq(_ context.Context, _ int64, date, seq int) error {
	return s.change(func(st *updates.State) { st.Date, st.Seq = date, seq })
}

// channels reads the stored pts of every channel; the caller holds mu.
func (s *updateStorage) channels() (map[string]int, error) {
	pts := map[string]int{}
	_, err := loadJSON(s.state, keyChannelPts, &pts)
	return pts, err
}

func (s *updateStorage) GetChannelPts(_ context.Context, _, channelID int64) (int, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	pts, err := s.channels()
	p, found := pts[strconv.FormatInt(channelID, 10)]
	return p, found, err
}

func (s *updateStorage) SetChannelPts(_ context.Context, _, channelID int64, p int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	pts, err := s.channels()
	if err != nil {
		return err
	}
	pts[strconv.FormatInt(channelID, 10)] = p
	return saveJSON(s.state, keyChannelPts, pts)
}

func (s *updateStorage) ForEachChannels(ctx context.Context, _ int64, f func(ctx context.Context, channelID int64, pts int) error) error {
	s.mu.Lock()
	pts, err := s.channels()
	s.mu.Unlock()
	if err != nil {
		return err
	}
	for id, p := range pts {
		n, err := strconv.ParseInt(id, 10, 64)
		if err != nil {
			continue
		}
		if err := f(ctx, n, p); err != nil {
			return err
		}
	}
	return nil
}

func (s *updateStorage) getHash(key string) (int64, bool, error) {
	var h int64
	found, err := loadJSON(s.state, key, &h)
	return h, found, err
}

func (s *updateStorage) SetChannelAccessHash(_ context.Context, _, channelID, hash int64) error {
	return saveJSON(s.state, fmt.Sprintf("hash/channel/%d", channelID), hash)
}

func (s *updateStorage) GetChannelAccessHash(_ context.Context, _, channelID int64) (int64, bool, error) {
	return s.getHash(fmt.Sprintf("hash/channel/%d", channelID))
}

func (s *updateStorage) SetUserAccessHash(_ context.Context, _, userID, hash int64) error {
	return saveJSON(s.state, fmt.Sprintf("hash/user/%d", userID), hash)
}

func (s *updateStorage) GetUserAccessHash(_ context.Context, _, userID int64) (int64, bool, error) {
	return s.getHash(fmt.Sprintf("hash/user/%d", userID))
}
