// Package model holds the normalized events connectors emit and the archive stores.
package model

import "time"

type ChatKind string

const (
	ChatPrivate ChatKind = "private"
	ChatGroup   ChatKind = "group"
	ChatChannel ChatKind = "channel"
	ChatBot     ChatKind = "bot"
	ChatThread  ChatKind = "thread" // a mail conversation
)

func (k ChatKind) Valid() bool {
	switch k {
	case ChatPrivate, ChatGroup, ChatChannel, ChatBot, ChatThread:
		return true
	}
	return false
}

// ArchivedByDefault reports whether a newly seen chat of this kind is archived
// without the owner opting in.
func (k ChatKind) ArchivedByDefault() bool {
	return k == ChatPrivate || k == ChatGroup || k == ChatThread
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
	// SourceID is the file's identity, when the source gives one. A ref
	// whose identity no longer matches the attachment at its position (an
	// edit replaced the file) is refused, so a late download cannot land on
	// the replacement.
	SourceID string
}

// FolderRole is what a mail folder is for, from its SPECIAL-USE attribute or
// its name.
type FolderRole string

const (
	FolderInbox     FolderRole = "inbox"
	FolderSent      FolderRole = "sent"
	FolderAll       FolderRole = "all" // every message once, as Gmail's All Mail
	FolderDrafts    FolderRole = "drafts"
	FolderJunk      FolderRole = "junk"
	FolderTrash     FolderRole = "trash"
	FolderFlagged   FolderRole = "flagged"
	FolderImportant FolderRole = "important"
	FolderOther     FolderRole = "other"
)

func (r FolderRole) Valid() bool {
	switch r {
	case FolderInbox, FolderSent, FolderAll, FolderDrafts, FolderJunk, FolderTrash, FolderFlagged, FolderImportant, FolderOther:
		return true
	}
	return false
}

// Folder is a mailbox's folder. ArchiveByDefault applies when the folder is
// first seen; after that the owner's choice stands.
type Folder struct {
	SourceID         string
	Name             string
	Role             FolderRole
	ArchiveByDefault bool
}

// AddressRole is the header a mail address came from.
type AddressRole string

const (
	AddrFrom    AddressRole = "from"
	AddrTo      AddressRole = "to"
	AddrCc      AddressRole = "cc"
	AddrBcc     AddressRole = "bcc"
	AddrReplyTo AddressRole = "reply_to"
)

func (r AddressRole) Valid() bool {
	switch r {
	case AddrFrom, AddrTo, AddrCc, AddrBcc, AddrReplyTo:
		return true
	}
	return false
}

// Address is one mail address of a message, with the name it came with.
type Address struct {
	Role    AddressRole
	Name    string
	Address string
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
	// Mail only. Subject is the Subject header; Addresses are the From, To,
	// Cc, Bcc and Reply-To addresses in header order; Original is the
	// message as received, kept as a file in the media store.
	Subject   string
	Addresses []Address
	Original  []byte
}
