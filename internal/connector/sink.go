package connector

import (
	"errors"
	"fmt"
	"io"

	"github.com/fenelon/arc5/internal/archive"
	"github.com/fenelon/arc5/internal/model"
)

// accountSink binds the archive to one account.
type accountSink struct {
	store     *archive.Store
	accountID int64
	requests  <-chan model.AttachmentRef
	progress  func(Progress)
}

func newSink(store *archive.Store, accountID int64, requests <-chan model.AttachmentRef, progress func(Progress)) Sink {
	return &accountSink{store: store, accountID: accountID, requests: requests, progress: progress}
}

// classify marks archive failures the connector cannot fix as ErrStorage and
// passes through the ones that describe the connector's own input.
func classify(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, archive.ErrUnknownChat),
		errors.Is(err, archive.ErrNotFound),
		errors.Is(err, archive.ErrUnknownAttachment),
		errors.Is(err, archive.ErrMediaRead),
		errors.Is(err, archive.ErrInvalid):
		return err
	default:
		return fmt.Errorf("%w: %v", ErrStorage, err)
	}
}

func (s *accountSink) UpsertChat(c model.Chat) error {
	return classify(s.store.UpsertChat(s.accountID, c))
}

func (s *accountSink) UpsertSender(sn model.Sender) error {
	return classify(s.store.UpsertSender(s.accountID, sn))
}

func (s *accountSink) UpsertMessage(m model.Message) ([]model.AttachmentRef, error) {
	refs, err := s.store.UpsertMessage(s.accountID, m)
	return refs, classify(err)
}

func (s *accountSink) MarkDeleted(chatSourceID string, messageSourceIDs []string) error {
	return classify(s.store.MarkDeleted(s.accountID, chatSourceID, messageSourceIDs))
}

func (s *accountSink) LookupChats(messageSourceID string, kinds []model.ChatKind) ([]string, error) {
	chats, err := s.store.LookupChats(s.accountID, messageSourceID, kinds)
	return chats, classify(err)
}

func (s *accountSink) PutMedia(ref model.AttachmentRef, r io.Reader) error {
	return classify(s.store.PutMedia(s.accountID, ref, r))
}

func (s *accountSink) SetAttachmentStatus(ref model.AttachmentRef, status model.AttachmentStatus, reason string) error {
	return classify(s.store.SetAttachmentStatus(s.accountID, ref, status, reason))
}

func (s *accountSink) ChatArchived(chatSourceID string) (bool, error) {
	archived, err := s.store.ChatArchived(s.accountID, chatSourceID)
	return archived, classify(err)
}

func (s *accountSink) RequestedMedia() <-chan model.AttachmentRef { return s.requests }

func (s *accountSink) PendingMedia() ([]model.AttachmentRef, error) {
	refs, err := s.store.PendingMedia(s.accountID)
	return refs, classify(err)
}

func (s *accountSink) ReportProgress(p Progress) { s.progress(p) }

func (s *accountSink) AvatarStored(ref model.AvatarRef) (bool, error) {
	stored, err := s.store.AvatarStored(s.accountID, ref)
	return stored, classify(err)
}

func (s *accountSink) PutAvatar(ref model.AvatarRef, r io.Reader) error {
	return classify(s.store.PutAvatar(s.accountID, ref, r))
}

// NewState returns an account's state, for a setup flow run outside the runner.
func NewState(store *archive.Store, accountID int64) State {
	return accountState{store: store, accountID: accountID}
}

// accountState binds connector_state to one account.
type accountState struct {
	store     *archive.Store
	accountID int64
}

func (s accountState) Get(key string) ([]byte, error) {
	v, err := s.store.StateGet(s.accountID, key)
	return v, classify(err)
}

func (s accountState) Set(key string, value []byte) error {
	return classify(s.store.StateSet(s.accountID, key, value))
}
