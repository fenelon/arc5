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
	// PendingMedia lists attachments still waiting to be downloaded that
	// nobody asked for by hand, so a connector can queue them again after a
	// restart.
	PendingMedia() ([]model.AttachmentRef, error)
	ReportProgress(Progress)
	// AvatarStored reports whether a chat's or sender's picture with this
	// identity is already kept, so only new pictures are downloaded.
	AvatarStored(model.AvatarRef) (bool, error)
	// PutAvatar stores a chat's or sender's picture and makes it the one shown.
	PutAvatar(model.AvatarRef, io.Reader) error
}

// MailSink is the Sink of a mail connector: the runner's sink implements it,
// and a mail connector asserts it. Threads are chats of kind ChatThread;
// folders and which messages each holds are recorded here.
type MailSink interface {
	Sink
	// UpsertFolder records a folder; ArchiveByDefault applies the first time.
	UpsertFolder(model.Folder) error
	// FolderArchived reports whether the owner archives a folder.
	FolderArchived(folderSourceID string) (bool, error)
	// FileInFolder records that a stored message is in a folder under a UID.
	FileInFolder(folderSourceID string, uid uint32, chatSourceID, messageSourceID string) error
	// FolderUIDs lists the UIDs of the messages filed in a folder and still there.
	FolderUIDs(folderSourceID string) ([]uint32, error)
	// LeaveFolder records that messages left a folder; a message gone from
	// every folder is deleted at source.
	LeaveFolder(folderSourceID string, uids []uint32) error
	// ResetFolder forgets a folder's UIDs after the server renumbered it.
	ResetFolder(folderSourceID string) error
	// SetAccountAddress records the mailbox's own address.
	SetAccountAddress(address string) error
	// Original opens a stored message as received.
	Original(chatSourceID, messageSourceID string) (io.ReadCloser, error)
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
	// returns the next step. A step with Done set ends the flow. An error
	// means the input was not accepted: the flow stays on the step it was
	// on, and the interface shows the error's text and asks again, so the
	// text should say what to do. ctx ends with the request that submitted
	// the step. A flow that holds something open between steps, such as a
	// connection, implements io.Closer: the interface closes it when the flow
	// ends, is cancelled, or is left unfinished for 30 minutes.
	Next(ctx context.Context, input map[string]string) (Step, error)
}

// SignOuter is implemented by connectors that can end their session at the
// source, such as logging out of Telegram. The interface offers to sign a
// source out only when its connector implements it. The runner stops the
// connector first and starts it again afterwards, when it should report
// ErrNeedsLogin.
type SignOuter interface {
	SignOut(ctx context.Context, state State) error
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
	FieldBool   FieldKind = "bool" // a checkbox; its value is "true" when ticked and "" when not
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
	// ErrCannotSignOut is returned by Runner.SignOut for a connector that
	// does not implement SignOuter.
	ErrCannotSignOut = errors.New("this kind of source cannot be signed out")
)
