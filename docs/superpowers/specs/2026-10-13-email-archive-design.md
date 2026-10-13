# arc5: email archive — design

Status: draft for the owner's review, written 2026-10-13 while the owner was
away ("move towards mail integration"). Nothing here is built yet. The
decisions marked **Decision** are proposals; the list at the end collects
them.

This extends `2026-10-12-message-archive-design.md` (the main spec), which
already reserves a mail layout and `DisplayMail`. Everything the main spec
says about durability, the archive as the only database writer, search, the
interface's look and security applies here unchanged.

## 1. Purpose

Keep a durable, searchable copy of one or more mailboxes, so the owner can
clear out a mailbox, let a provider's retention delete old mail, or leave a
provider, without losing anything. Mail is read from the server; arc5 never
sends, moves, flags or deletes mail.

### Success criteria

- Every message in the chosen folders is archived once, with its text, its
  attachments, and the original message as received.
- Mail deleted on the server stays in the archive, marked as deleted at
  source.
- New mail is archived within a minute of arriving in the Inbox, and within
  the polling interval in other folders.
- Search finds mail by word, sender, address and subject, alongside chats
  when "Everything at once" covers both.
- The mail layout of the approved mockups (`Mail.dc.html`,
  `MailDark.dc.html`): folders on the left, conversations as rows, a thread
  as a column of message cards.

## 2. Protocol and libraries

**Decision: IMAP4rev1/rev2 over TLS**, the protocol every provider offers.
JMAP (Fastmail) and provider APIs (Gmail, Microsoft Graph) are not used in
v1.

- `github.com/emersion/go-imap/v2`: pure Go IMAP client with IDLE,
  CONDSTORE/QRESYNC and SPECIAL-USE. It is still a beta (v2.0.0-beta.8 in
  October 2026) but is the maintained line; v1 is stable but frozen and
  lacks QRESYNC. **Decision: use v2**, pinned, behind the connector's own
  narrow client interface as for Telegram, so a change of library stays
  inside the connector.
- `github.com/emersion/go-message`: pure Go MIME parsing, including charsets.
- HTML to text: `golang.org/x/net/html` tokenizer, a few dozen lines in arc5
  (no rendering engine).

No cgo; all three keep the single static binary.

## 3. Login

The setup flow (a `connector.SetupFlow`, rendered by the Sources page like
Telegram's):

1. **Server**: address and port, prefilled for known providers from the
   email address's domain (Gmail, Fastmail, iCloud, Yahoo, Proton Bridge,
   GMX, mailbox.org; others: `imap.<domain>:993`). Implicit TLS on 993, or
   STARTTLS on 143; plain text is refused.
2. **Account**: the email address and user name (usually the same).
3. **Password**: the account password or, where the provider requires one,
   an app password. The help text names the providers that require app
   passwords (Gmail, iCloud, Yahoo, Fastmail) and links their pages.

The password is stored in `connector_state` like Telegram's session, with
the same warning: the data folder grants access to the mailbox.

**Decision: no OAuth2 in v1.** Gmail and Fastmail work with app passwords.
Microsoft's consumer and business mail no longer accept passwords over
IMAP, so Outlook.com and Microsoft 365 mailboxes cannot be added until OAuth2
is built (a later spec: it needs a registered application and a redirect to
the owner's browser).

## 4. Data model

Mail reuses the archive's tables where they fit and adds what mail needs.

| Archive | Mail |
|---|---|
| account | one mailbox |
| chat | one conversation (thread) |
| sender | one address (`source_id` the lower-cased address, `display_name` the name it last appeared with, `username` the address as written) |
| message | one email |
| attachment | one attached file, or an inline image |
| `message_versions` | not used: mail is not edited |

**Decision: a thread is a chat** with a new kind, `thread`, so the chat list,
search, the year rail and the archived-chats switch work for mail without
special cases. Migration: `chats.kind` gains `thread`.

New columns and tables (one migration):

```
messages.subject TEXT            -- empty for chats
messages.message_id TEXT         -- the Message-ID header, normalized
message_addresses(message_id, role, position, address, name)
                                  -- role: from | to | cc | bcc | reply_to
folders(id, account_id, source_id, name, role, archived,
        uidvalidity, UNIQUE(account_id, source_id))
                                  -- role: inbox | sent | archive | all |
                                  --       drafts | junk | trash | other
message_folders(message_id, folder_id, uid, deleted_at,
                PRIMARY KEY(message_id, folder_id))
```

- **Identity.** A message's `source_id` is its Message-ID header, normalized
  (angle brackets and case of the domain part removed). A message without a
  Message-ID gets `sha256:` and the hash of its From, Date, Subject and first
  kilobyte of body, so a copy in two folders is still one message. The same
  message in Inbox and All Mail (Gmail) is stored once and listed in both
  folders through `message_folders`.
- **Raw.** `messages.raw` holds the original message as received (RFC 5322
  bytes, compressed), not JSON: it is the authoritative copy and what
  "Download original (.eml)" serves.
- **Text.** `messages.text` is the readable text: the `text/plain` part, or
  the `text/html` part converted to text (paragraphs and line breaks kept,
  links written as "text (address)", scripts, styles and images dropped).
  Quoted replies (`>` lines, "On … wrote:" blocks) stay in the text but are
  folded in the thread view.
- **Deletion.** A message is deleted at source when it is gone from every
  folder it was seen in (`message_folders.deleted_at` set in all of them);
  moving between folders is not deletion. Expunged mail is never removed
  here.
- **Sent and outgoing.** `messages.outgoing` is 1 when the From address is
  one of the mailbox's own addresses (the login address, plus any the owner
  adds in the source's settings).

## 5. Threads

**Decision: threads follow the headers**, as mail clients do:

1. Gmail's `X-GM-THRID` when the server offers it (Gmail): it is exactly
   Gmail's conversation.
2. Otherwise `References` and `In-Reply-To`: a message joins the thread of
   any message it references (a union of the reference graph, JWZ-style
   without the subject fallback).
3. A message with no references starts a thread of its own.

Subject-only grouping is not used: it merges unrelated mail ("Invoice",
"Hello"). A thread's chat `title` is the subject of its first message, `Re:`
and `Fwd:` prefixes removed; its `source_id` is `thread:` plus the
Message-ID of its first message (or `gm:` plus `X-GM-THRID`). When a late
message links two threads, the later thread's messages move into the
earlier one and the emptied chat is deleted: the only case in which the
archive deletes a row, which this spec adds to the main spec's rules.

## 6. Capture

### Folders

Every selectable folder is listed on the Sources page with an archive
switch, as chats are for Telegram. **Decision: archived by default** — Inbox,
Sent, Archive, All Mail and the owner's own folders; **not** Drafts, Junk and
Trash (SPECIAL-USE attributes, or the names providers use when the server
has none). Gmail's labels appear as folders; "All Mail" alone holds every
message once, so for Gmail only Inbox, Sent and All Mail are needed and the
others are off by default.

### Backfill

Per archived folder, newest first: `UID SEARCH` within the age limit
(`SINCE`), then `UID FETCH` in batches of 50 of `BODYSTRUCTURE`, `ENVELOPE`,
`INTERNALDATE`, `RFC822.SIZE`, the text parts, and attachments under the
size cap. Larger attachments are recorded `skipped` and fetched on request
by `BODY.PEEK[<part>]`. A checkpoint per folder (`uidvalidity` and the lowest
UID stored) is written after each batch is stored. Speed and age limit work
as for Telegram. `BODY.PEEK` is used throughout, so archiving never marks
mail as read.

If a folder's `UIDVALIDITY` changes, its UIDs are re-read: messages are
matched by Message-ID, so nothing is stored twice.

### Live

- One connection IDLEs on the Inbox; new UIDs are fetched as they arrive.
- Other archived folders are checked every 5 minutes (CONDSTORE's
  `HIGHESTMODSEQ` when offered, so an unchanged folder costs one command).
- Expunges are seen through QRESYNC `VANISHED` where offered, otherwise by
  comparing UID sets during the periodic check, and mark the message gone
  from that folder.

### Failure handling

As in the main spec, with: an authentication failure is "needs login";
a server that drops the connection is retried with backoff; a message that
cannot be parsed is stored with its raw bytes and the text "This message
could not be read", never skipped.

## 7. Interface

The mail layout, as in the mockups:

- **Left column:** the mailbox's name and address, then Inbox, Sent and All
  mail (folders with those roles), then the owner's other archived folders,
  and a note that deleted mail stays.
- **Conversation list:** rows with the initials or picture of the latest
  sender, name, address and "and you", message count, subject in bold, the
  start of the latest message, an attachment mark, "Deleted at source" when
  every message is, and the date. "With attachments" and "Deleted at source"
  filter the list. "Older conversations" pages.
- **Thread:** "Back to Inbox", the subject, "5 messages, 25 to 27 September
  2026", then one card per message: initials, name, address, recipients,
  date, "4 of 5"; earlier messages folded to one line, the last two open; the
  owner's tinted; attachments as file cards with "Open"; "Download original
  (.eml)". Message text is shown as text, never as HTML.
- **Search** in a mail source says "Search mail by word, sender, address or
  subject"; subjects are indexed with the body, and `from:`/`to:` are not
  needed because names and addresses already match people.
- **Phone:** folders become a menu above the list; a thread is its own
  screen, as a chat is.

## 8. Security

- No HTML from mail is ever rendered: text only, through `html/template`
  escaping, under the existing CSP. Remote images are never fetched.
- Attachments follow the existing media rules: an allowlist of image, audio
  and video types inline; everything else, including HTML, SVG and the
  original `.eml`, is a download, served with `nosniff` and the media
  sandbox.
- TLS certificate verification is never disabled; a self-signed server is
  added by pinning its certificate's fingerprint in the setup flow (later,
  if asked for).

## 9. Testing

- **Mapping:** MIME fixtures (multipart/alternative, nested multipart,
  charsets including KOI8-R and Shift_JIS, quoted-printable, base64, inline
  images, winmail.dat left as a file, broken MIME) to archive events.
- **Threading:** reference graphs, late messages that join two threads,
  Gmail thread IDs.
- **IMAP behaviour:** against an in-process IMAP server
  (`github.com/emersion/go-imap/v2/imapserver/imapmemserver`):
  backfill with checkpoints, UIDVALIDITY change, IDLE, expunge, a message in
  two folders, BODY.PEEK only (a test fails if a fetch would set `\Seen`).
- **Manual:** one Gmail mailbox with an app password, one Fastmail mailbox.

## 10. Build order

1. Archive: chat kind `thread`, subjects, addresses, folders, message
   folders, raw MIME; the demo gains a mailbox with the mockups' mail.
2. The mail layout and its search, on the demo mailbox.
3. The IMAP connector: login, folders, backfill, live, deletions, threads.

## Decisions for the owner to confirm

1. IMAP only; no JMAP or provider APIs in v1. The go-imap v2 beta, pinned.
2. No OAuth2 in v1, so Microsoft mailboxes cannot be added yet.
3. A thread is a chat of a new kind, `thread`.
4. Threads by `X-GM-THRID` or `References`/`In-Reply-To`, never by subject.
5. Merging threads deletes the emptied chat row (the archive's first delete).
6. Drafts, Junk and Trash are not archived by default.
7. Mail is shown as text only; HTML is never rendered.
8. The raw column holds the original MIME bytes for mail, not JSON.
9. A message is deleted at source only when gone from every folder.
