package email

import (
	"slices"
	"strings"

	"github.com/fenelon/arc5/internal/model"
)

// specialUse maps SPECIAL-USE attributes (RFC 6154) to folder roles.
var specialUse = map[string]model.FolderRole{
	`\all`: model.FolderAll, `\sent`: model.FolderSent, `\drafts`: model.FolderDrafts, `\junk`: model.FolderJunk,
	`\trash`: model.FolderTrash, `\flagged`: model.FolderFlagged, `\important`: model.FolderImportant,
}

// usualNames are the roles of folders on servers without SPECIAL-USE, by
// the English names mail programs give them.
var usualNames = map[string]model.FolderRole{
	"sent": model.FolderSent, "sent items": model.FolderSent, "sent messages": model.FolderSent, "sent mail": model.FolderSent,
	"drafts": model.FolderDrafts, "draft": model.FolderDrafts,
	"junk": model.FolderJunk, "spam": model.FolderJunk, "junk e-mail": model.FolderJunk, "junk email": model.FolderJunk, "bulk mail": model.FolderJunk,
	"trash": model.FolderTrash, "deleted items": model.FolderTrash, "deleted messages": model.FolderTrash, "bin": model.FolderTrash,
}

// roleOf is a folder's role: from its SPECIAL-USE attribute, else INBOX by
// its name, else the usual names (only on a server that marks no folder).
func roleOf(mb mailbox, serverMarks bool) model.FolderRole {
	if strings.EqualFold(mb.Name, "INBOX") {
		return model.FolderInbox
	}
	for _, attr := range mb.Attrs {
		if role, ok := specialUse[attr]; ok {
			return role
		}
	}
	if !serverMarks {
		if role, ok := usualNames[strings.ToLower(displayName(mb.Name))]; ok {
			return role
		}
	}
	return model.FolderOther
}

// displayName is how a folder is shown: "Inbox", and without the "[Gmail]/"
// or "INBOX." a server puts before its folders.
func displayName(name string) string {
	if strings.EqualFold(name, "INBOX") {
		return "Inbox"
	}
	for _, prefix := range []string{"[Gmail]/", "[Google Mail]/", "INBOX/", "INBOX."} {
		if len(name) > len(prefix) && strings.EqualFold(name[:len(prefix)], prefix) {
			return name[len(prefix):]
		}
	}
	return name
}

// folderPlan is the folders a server lists, with their roles and whether
// they are archived unless the owner says otherwise.
func folderPlan(list []mailbox) []model.Folder {
	marks, hasAll := false, false
	for _, mb := range list {
		for _, attr := range mb.Attrs {
			if role, ok := specialUse[attr]; ok {
				marks = true
				hasAll = hasAll || role == model.FolderAll
			}
		}
	}
	out := make([]model.Folder, 0, len(list))
	for _, mb := range list {
		role := roleOf(mb, marks)
		archive := role != model.FolderDrafts && role != model.FolderJunk && role != model.FolderTrash &&
			role != model.FolderFlagged && role != model.FolderImportant
		if hasAll {
			// All Mail holds every message once: the Inbox and Sent add which
			// folder mail is in; labels and the rest add nothing new.
			archive = role == model.FolderInbox || role == model.FolderSent || role == model.FolderAll
		}
		out = append(out, model.Folder{SourceID: mb.Name, Name: displayName(mb.Name), Role: role, ArchiveByDefault: archive})
	}
	return out
}

// captureOrder puts All Mail first, so a Gmail message is downloaded from
// there and only matched in the Inbox and Sent; then the Inbox, Sent, and the
// rest in the server's order.
func captureOrder(folders []model.Folder) []model.Folder {
	rank := func(r model.FolderRole) int {
		switch r {
		case model.FolderAll:
			return 0
		case model.FolderInbox:
			return 1
		case model.FolderSent:
			return 2
		}
		return 3
	}
	out := slices.Clone(folders)
	slices.SortStableFunc(out, func(a, b model.Folder) int { return rank(a.Role) - rank(b.Role) })
	return out
}
