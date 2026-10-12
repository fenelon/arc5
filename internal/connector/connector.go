// Package connector defines how message sources plug into arc5 and supervises
// them. A connector knows its source's protocol and nothing about storage.
package connector

import (
	"context"
	"errors"
	"io"

	"github.com/fenelon/arc5/internal/model"
)

// Connector captures one account's messages from one kind of source.
// Connectors are strictly one-way: they never send, edit or delete at the source.
type Connector interface {
	// Run performs backfill and then live capture. It blocks until ctx is
	// cancelled or an unrecoverable error occurs. Returning ErrNeedsLogin
	// stops the runner from retrying. Settings are fixed for the duration of
	// a run; the runner restarts the connector when they change.
	Run(ctx context.Context, sink Sink, state State, settings map[string]string) error

	// Setup returns the interactive flow used to connect an account.
	Setup(state State) SetupFlow

	// Settings describes the per-account options the interface should offer.
	Settings() []Field
}

// Sink is where a connector emits what it captures. All methods are safe for
// concurrent use and every write is idempotent.
type Sink interface {
	UpsertChat(model.Chat) error
	UpsertSender(model.Sender) error
	// UpsertMessage returns the attachments still to download.
	UpsertMessage(model.Message) ([]model.AttachmentRef, error)
	MarkDeleted(chatSourceID string, messageSourceIDs []string) error
	// LookupChats finds which chats hold a message, for sources that report
	// deletions without naming the chat.
	LookupChats(messageSourceID string, kinds []model.ChatKind) ([]string, error)
	PutMedia(ref model.AttachmentRef, r io.Reader) error
	SetAttachmentStatus(ref model.AttachmentRef, status model.AttachmentStatus, reason string) error
	ChatArchived(chatSourceID string) (bool, error)
	// RequestedMedia delivers attachments the owner asked to fetch or retry.
	// They are downloaded regardless of the size cap. A ref may be delivered
	// more than once.
	RequestedMedia() <-chan model.AttachmentRef
	ReportProgress(Progress)
}

// State is a connector's private per-account key-value store, for sessions and checkpoints.
type State interface {
	// Get returns nil when the key is unset.
	Get(key string) ([]byte, error)
	Set(key string, value []byte) error
}

// SetupFlow walks the owner through connecting an account.
type SetupFlow interface {
	// Next submits the previous step's input (nil for the first call) and
	// returns the next step. A step with Done set ends the flow.
	Next(ctx context.Context, input map[string]string) (Step, error)
}

type Step struct {
	Title  string
	Help   string
	Fields []Field
	Done   bool
}

type FieldKind string

const (
	FieldText   FieldKind = "text"
	FieldSecret FieldKind = "secret"
	FieldNumber FieldKind = "number"
	FieldChoice FieldKind = "choice"
)

type Field struct {
	Name    string
	Label   string
	Help    string
	Kind    FieldKind
	Choices []string // for FieldChoice
	Default string
}

type Phase string

const (
	PhaseConnecting  Phase = "connecting"
	PhaseBackfill    Phase = "backfill"
	PhaseLive        Phase = "live"
	PhaseRateLimited Phase = "rate_limited"
)

// Progress is a connector's latest report of what it is doing.
type Progress struct {
	Phase      Phase
	Detail     string
	ChatsDone  int
	ChatsTotal int
}

var (
	// ErrNeedsLogin means the account's session is missing or revoked. The
	// runner stops retrying until the owner logs in again.
	ErrNeedsLogin = errors.New("account needs login")
	// ErrStorage wraps archive failures a connector cannot fix, such as a full disk.
	ErrStorage = errors.New("archive storage failed")
)
