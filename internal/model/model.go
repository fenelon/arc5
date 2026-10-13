// Package model holds the normalized events connectors emit and the archive stores.
package model

import "time"

type ChatKind string

const (
	ChatPrivate ChatKind = "private"
	ChatGroup   ChatKind = "group"
	ChatChannel ChatKind = "channel"
	ChatBot     ChatKind = "bot"
)

func (k ChatKind) Valid() bool {
	switch k {
	case ChatPrivate, ChatGroup, ChatChannel, ChatBot:
		return true
	}
	return false
}

// ArchivedByDefault reports whether a newly seen chat of this kind is archived
// without the owner opting in.
func (k ChatKind) ArchivedByDefault() bool {
	return k == ChatPrivate || k == ChatGroup
}

type MessageKind string

const (
	KindMessage MessageKind = "message"
	KindService MessageKind = "service"
)

func (k MessageKind) Valid() bool { return k == KindMessage || k == KindService }

type AttachmentKind string

const (
	AttachPhoto    AttachmentKind = "photo"
	AttachVideo    AttachmentKind = "video"
	AttachAudio    AttachmentKind = "audio"
	AttachVoice    AttachmentKind = "voice"
	AttachDocument AttachmentKind = "document"
	AttachSticker  AttachmentKind = "sticker"
	AttachOther    AttachmentKind = "other"
)

func (k AttachmentKind) Valid() bool {
	switch k {
	case AttachPhoto, AttachVideo, AttachAudio, AttachVoice, AttachDocument, AttachSticker, AttachOther:
		return true
	}
	return false
}

type AttachmentStatus string

const (
	StatusPending     AttachmentStatus = "pending"
	StatusStored      AttachmentStatus = "stored"
	StatusSkipped     AttachmentStatus = "skipped"
	StatusFailed      AttachmentStatus = "failed"
	StatusUnavailable AttachmentStatus = "unavailable"
)

type Chat struct {
	SourceID string
	Kind     ChatKind
	Title    string
}

type Sender struct {
	SourceID    string
	DisplayName string
	Username    string
}

type Attachment struct {
	Kind      AttachmentKind
	FileName  string
	MimeType  string
	Size      int64
	Ephemeral bool // self-destructing or view-once at the source
	// SourceID is the source's identity for the file, such as "photo:123".
	// When an edit brings a different file at the same position, the stored
	// one is kept as replaced. Empty means unknown: the first file stays.
	SourceID string
	// NoFetch records the attachment without downloading it; NoFetchReason says why.
	NoFetch       bool
	NoFetchReason string
}

type AvatarKind string

const (
	AvatarChat   AvatarKind = "chat"
	AvatarSender AvatarKind = "sender"
)

// AvatarRef names a chat's or a sender's picture: whose it is, by source ID,
// and the source's identity for the picture, which changes when it does.
type AvatarRef struct {
	Kind     AvatarKind
	SourceID string
	AvatarID string
}

// AttachmentRef names an attachment by source IDs, so connectors never see database IDs.
type AttachmentRef struct {
	ChatSourceID    string
	MessageSourceID string
	Position        int
}

type Message struct {
	ChatSourceID    string
	SourceID        string
	SenderSourceID  string      // empty when the source names no sender
	Kind            MessageKind // empty means KindMessage
	Outgoing        bool        // sent by the account owner
	SentAt          time.Time
	Text            string
	ReplyToSourceID string
	ForwardedFrom   string
	EditedAt        time.Time // zero if never edited
	Raw             []byte    // the source's original payload as JSON, uncompressed
	Attachments     []Attachment
}
