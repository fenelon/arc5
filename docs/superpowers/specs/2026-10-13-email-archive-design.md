# arc5: email archive — design

Status: approved to build, 2026-10-13. The first draft was written while the
owner was away; the owner then said to start building, that **Gmail must
work**, and that **mail sent only as HTML must become good text**. This
revision adds both and changes four of the draft's decisions after checking
the IMAP library (marked **Changed**). The list at the end collects every
decision for the owner to confirm.

This extends `2026-10-12-message-archive-design.md` (the main spec), which
already reserves a mail layout and `DisplayMail`. Everything the main spec
says about durability, the archive as the only database writer, search, the
interface's look and security applies here unchanged.

## 1. Purpose

Keep a durable, searchable copy of one or more mailboxes, so the owner can
clear out a mailbox, let a provider's retention delete old mail, or leave a
provider, without losing anything. Mail is read from the server; arc5 never
sends, moves, flags or deletes mail, and never marks it as read.

### Success criteria

- Every message in the chosen folders is archived once, with its text, its
  attachments, and the original message as received.
- A Gmail mailbox works: Inbox, Sent and All Mail are archived with an app
  password, each message stored once however many labels it has.
- Mail sent only as HTML reads as clean text: paragraphs, lists, links and
  quotes kept; styles, scripts, tracking images and hidden text gone.
- Mail deleted on the server stays in the archive, marked as deleted at
  source.
- New mail is archived within a minute of arriving in the folder arc5
  watches (the Inbox, or All Mail on Gmail), and within five minutes in
  other folders.
- Search finds mail by word, sender, address and subject.
- The mail layout of the approved mockups (`Mail.dc.html`,
  `MailDark.dc.html`): folders on the left, conversations as rows, a thread
  as a column of message cards.

## 2. Protocol and libraries

**Decision: IMAP over TLS**, the protocol every provider offers. JMAP
(Fastmail) and provider APIs (the Gmail API, Microsoft Graph) are not used
in v1.

- `github.com/emersion/go-imap/v2` v2.0.0-beta.8, pinned: pure Go, with
  IDLE, SPECIAL-USE and CONDSTORE. It is a beta but the maintained line (v1
  is frozen). It sits behind the connector's own narrow client interface, as
  gotd does for Telegram, so a change of library stays inside the connector.
- `github.com/emersion/go-message` v0.18: MIME parsing, with its `charset`
  package for legacy charsets (KOI8-R, Shift_JIS, Windows-125x, ISO-8859-x).
- `golang.org/x/net/html`: the HTML parser for HTML-to-text (section 6),
  and `golang.org/x/net/html/charset` for `<meta charset>`.

No cgo; all of them keep the single static binary.

### Gmail's extensions are not available (Changed)

Gmail offers `X-GM-MSGID` (a message's identity), `X-GM-THRID` (its
conversation) and `X-GM-LABELS` (its labels) as FETCH items. go-imap
v2.0.0-beta.8 can neither request them (`FetchOptions` has no field for
them and no way to add a raw item) nor read them (an unknown FETCH item
ends the response with "unsupported msg-att name"). Patching the library
means carrying a fork of a beta.

**Decision: Gmail is archived through its standard IMAP folders**, which
already give everything the archive needs:

- Folders are found by their SPECIAL-USE attributes (`\All`, `\Sent`,
  `\Drafts`, `\Junk`, `\Trash`, `\Flagged`, `\Important`) and `INBOX`, never
  by name, so a Gmail account in any language works (`[Gmail]/Alle
  Nachrichten`, `[Gmail]/Отправленные`).
- `\All` (All Mail) holds every message once, sent mail included, Spam and
  Trash excluded. Its messages are downloaded whole.
- The Inbox and Sent folders give folder membership. Their messages are
  matched by Message-ID with a headers-only fetch, so a message is
  downloaded once even though Gmail lists it in three folders.
- Gmail's labels are folders too; they are listed with their switches off.
- A message deleted in Gmail moves to Trash and so leaves All Mail: that is
  "deleted at source". Archiving a message in Gmail (removing it from the
  Inbox) is not.
- Threads follow the `References` header (section 5), not Gmail's own
  grouping, which also merges by subject.

Cost if wrong: if the library gains the Gmail items, the connector can use
them for exact identities and labels without changing the archive.

## 3. Login

The setup flow (a `connector.SetupFlow`, rendered by the Sources page like
Telegram's) has two steps:

1. **Mailbox**: the email address.
2. **Server and password**: the IMAP server and port, prefilled from the
   address's domain for known providers (Gmail and Googlemail, Fastmail,
   iCloud, Yahoo, GMX, mailbox.org, Yandex, Zoho); otherwise
   `imap.<domain>` on 993. The user
   name, prefilled with the address. The password or app password. arc5
   connects and logs in before accepting the step; a refused login says
   what to check.

Implicit TLS on 993, or STARTTLS on any other port; plain text is refused,
and so is a certificate that does not verify (Proton Bridge's self-signed
one included, until pinning is built). The password is stored in
`connector_state` like Telegram's session, with the same warning: the data
folder grants access to the mailbox.

**Gmail.** Google requires an app password for IMAP: the account needs
2-Step Verification, then an app password is created at
<https://myaccount.google.com/apppasswords> and pasted in place of the
normal password. The help text says so when the address is a Gmail one. A
Google Workspace account works only if its administrator allows app
passwords and IMAP; if not, it has to wait for OAuth.

**Decision: no OAuth2 in v1.** Gmail, Fastmail, iCloud and Yahoo work with
app passwords. Microsoft's consumer and business mail no longer accept
passwords over IMAP, so Outlook.com and Microsoft 365 mailboxes cannot be
added until OAuth2 is built (a later spec: it needs a registered
application and a redirect to the owner's browser), nor can Workspace
accounts whose administrator forbids app passwords.

## 4. Data model

Mail reuses the archive's tables where they fit and adds what mail needs.

| Archive | Mail |
|---|---|
| account | one mailbox; `accounts.address` is its own address |
| chat | one conversation (thread), kind `thread` |
| sender | one address: `source_id` the lower-cased address, `display_name` the name it last came with, `username` the address |
| message | one email |
| attachment | one attached file, or an inline image |
| `message_versions` | not used: mail is not edited |

**Decision: a thread is a chat** of a new kind, `thread`, so the chat list,
search, the archived-chats switch and "Everything at once" work for mail
without special cases. Migration: `chats.kind` gains `thread` (a table
rebuild, since SQLite cannot change a CHECK constraint in place).

New columns and tables (one migration):

```
accounts.address TEXT            -- a mailbox's own address, '' for chats
messages.subject TEXT            -- '' for chats
messages.original_sha256 TEXT    -- the message as received, in the media store
message_addresses(message_id, role, position, address, name)
                                  -- role: from | to | cc | bcc | reply_to
folders(id, account_id, source_id, name, role, archived, created_at,
        UNIQUE(account_id, source_id))
                                  -- role: inbox | sent | all | drafts | junk |
                                  --       trash | flagged | important | other
message_folders(message_id, folder_id, uid, gone_at,
                PRIMARY KEY(message_id, folder_id))
```

- **Identity.** A message's `source_id` is its Message-ID, normalized
  (angle brackets removed, the domain part lower-cased). A message without
  one gets `sha256:` and the hash of its From, Date, Subject and first
  kilobyte of body, so a copy in two folders is still one message.
- **The original (Changed).** The message as received (RFC 5322 bytes) is
  kept as a file in the media store, content-addressed and synced like any
  attachment, and `messages.original_sha256` names it. The draft put it in
  `messages.raw`; a mailbox's originals are most of its size, and keeping
  them out of SQLite keeps the database small enough to back up and cache.
  "Download original (.eml)" serves it. `messages.raw` stays empty for mail.
- **Attachments.** Each attachment and inline image is an attachment row.
  Those under the attachment size limit are also kept as files of their
  own, so they open in the browser; the original holds them too, so they
  take their space twice. Larger ones are recorded `skipped`, and "Fetch
  now" takes them out of the kept original, without asking the server.
- **Text.** `messages.text` is the readable text: the `text/plain` part,
  or, when a message has only HTML, the HTML converted to text (section 6).
  Quoted replies stay in the text and are folded in the thread view.
- **Deletion.** A message is deleted at source when it is gone from every
  folder it was seen in (`message_folders.gone_at` set in all of them).
  Moving it between archived folders is not deletion, and a message seen
  again in any folder is no longer marked deleted. Nothing is removed from
  the archive.
- **Sent and outgoing.** `messages.outgoing` is 1 when the From address is
  the mailbox's own address or one the owner lists in the source's settings
  ("Your other addresses").

## 5. Threads (Changed)

**Decision: a thread is named by its first message**, read from the
headers, as mail clients do:

1. The first Message-ID in `References`, which by RFC 5322 is the start of
   the conversation and which clients keep when they shorten the header.
2. Otherwise the `In-Reply-To` Message-ID: the thread the archive already
   files that message under, or that Message-ID.
3. Otherwise the message's own Message-ID.

A thread's chat `source_id` is `thread:` plus that Message-ID; its title is
the subject without `Re:`, `Fwd:`, `AW:`, `Sv:`, `Ответ:` and similar
prefixes.

The draft joined threads through the whole reference graph and merged two
threads when a late message linked them, which meant moving messages and
deleting the emptied chat, the archive's only delete. Naming a thread by
its first message gives the same threads for mail from every modern client,
needs no merge, and never deletes. What it costs: a reply from a client that
drops `References` and whose parent arrives later starts a thread of its own.
Every original is kept, so threads can be recomputed later if that matters.

Subject-only grouping is not used: it merges unrelated mail ("Invoice",
"Hello").

## 6. HTML-only mail

Much mail, newsletters and receipts above all, has no `text/plain` part.
Its HTML is turned into text by arc5's own converter, built on the
`golang.org/x/net/html` parser, which repairs broken markup the way a
browser does. The text is what the thread view shows
and what search indexes, so anything left in it is noise in both.

Rules:

- **Charset.** The part's MIME `charset`, then the HTML's `<meta charset>`
  or `http-equiv` declaration, then UTF-8 (invalid bytes replaced).
- **Dropped with their content:** `<head>`, `<title>`, `<style>`,
  `<script>`, `<noscript>`, `<template>`, `<svg>`, `<object>`, comments
  (including Outlook's `<!--[if mso]>` conditional blocks), and elements
  hidden with `display:none`, `visibility:hidden`, `max-height:0`,
  `opacity:0` or the `hidden` attribute (the hidden "preheader" a
  newsletter puts before its content). Text whose font size in force is
  zero is dropped too, but `font-size:0` is inherited as in a browser:
  responsive (MJML) layouts put it on wrappers and set a size again on the
  content, which is kept. `mso-hide:all` hides only from Outlook, so it
  hides nothing here.
- **Blocks:** `p`, `div`, `table`, `tr`, `li`, `h1` to `h6`, `blockquote`,
  `pre`, `section`, `article`, `header`, `footer`, `address`, `center`,
  `form`, `hr` and similar start a new line; paragraphs, headings, tables
  and quotes are separated by one blank line. Runs of blank lines collapse
  to one.
- **Inline:** whitespace runs collapse to one space, as a browser does;
  `&nbsp;` counts as a space (an invisible zero-width character is
  dropped); `<br>` is a line break, and two or more in a row make one
  blank line.
- **Tables** used for layout read as text in order: each row starts a line,
  a cell that holds blocks breaks into its lines, and cells of a row that
  hold only text are joined by a tab, so a receipt's columns stay apart.
- **Blank paragraphs:** a paragraph or `div` holding only `&nbsp;` is a
  blank line, as Outlook and Gmail write one; Outlook's `MsoNormal`
  paragraphs, which have no margins, are lines rather than paragraphs.
- **Lists:** `ul` items start with "• ", `ol` items with "1. ", "2. " (or
  the `start` value); nested lists are indented two spaces per level.
- **Headings** are their own paragraph, as text (no `#`).
- **Quotes:** `<blockquote>` (Apple Mail's `type="cite"`, Gmail's
  `gmail_quote`) is written with "> " before each line, so the thread view
  folds it like a plain-text quote.
- **`<pre>`** keeps its spaces and line breaks.
- **Links** are written as `text (address)` for `http`, `https` and `ftp`
  addresses, except when the text already is the address (with or without
  the scheme), when there is no text, when the link holds only an image,
  or when the address is longer than 200 characters (tracking redirects). `mailto:` links are written as their text
  only. `javascript:` and other schemes are dropped.
- **Images** are written as their `alt` text in brackets when it is
  meaningful, otherwise nothing; one-pixel tracking images and spacers
  (`width="1"`, `height="1"`) are always dropped.
- **Outlook:** `<o:p>`, `<v:*>` and other namespaced Office tags are
  ignored, keeping their text.
- The result is trimmed, lines lose trailing spaces, and it is never more
  than one blank line at a time.

A message with both a `text/plain` and a `text/html` part shows the plain
part. When the plain part is empty or only says to view the message in a
browser ("This email requires HTML"), the HTML is converted instead.

**Testing.** A corpus of realistic HTML-only messages, each with the text
expected from it (golden files): a table-layout newsletter with a hidden
preheader and tracking pixels, a shop receipt with an items table, an
Outlook message with `mso` conditional comments and `<o:p>`, a Gmail reply
with `<div dir="ltr">` and a `gmail_quote`, an Apple Mail reply with
`<blockquote type="cite">`, runs of `&nbsp;` and other entities, runs of
`<br>`, nested ordered and unordered lists, headings, `<pre>`, a document
whose only charset is in `<meta>` (Windows-1251), and links of every kind
above. A test also checks that no corpus message leaves CSS, `{`, `@media`,
`<` or tracking addresses in its text.

## 7. Capture

### Folders

Every selectable folder is listed on the Sources page with an archive
switch, as chats are for Telegram ("Choose which folders are archived").
Roles come from SPECIAL-USE attributes and `INBOX`; a server with none gets
roles from the common English names (`Sent`, `Sent Items`, `Drafts`,
`Junk`, `Spam`, `Trash`, `Deleted Items`, `Archive`).

**Decision: archived by default:**

- When the server has an All Mail folder (`\All`, as Gmail does): Inbox,
  Sent and All Mail only; labels and every other folder are off, since All
  Mail already holds their messages.
- Otherwise: every folder except Drafts, Junk and Trash.

### The capture cycle

One connection does everything, in a cycle:

1. **New mail.** For each archived folder: `SELECT`, then the UIDs above the
   highest one already seen. New messages are fetched.
2. **Deletions.** A folder whose message count is lower than the archive
   expects (count before plus new mail) has lost messages: arc5 lists its
   UIDs (`UID SEARCH ALL`) and marks the ones it no longer holds as gone from
   that folder. New mail is taken in every folder before deletions are
   looked for, so a message moved between two archived folders is never
   marked deleted in between.
3. **History.** A slice of the catch-up, at most a minute: per archived
   folder, newest first, `UID SEARCH` within the age limit (`SINCE`), in
   batches of 50. A checkpoint per folder (`UIDVALIDITY` and the lowest UID
   done) is written after each batch is stored.
4. **Wait.** When history is complete, IDLE on the watched folder (All Mail
   when there is one, else the Inbox) until the server reports a change or
   five minutes pass, then start again.

So new mail is never more than a minute behind, even during the catch-up.

**Fetching.** For each new UID, a headers-only fetch (`BODY.PEEK[HEADER]`,
`INTERNALDATE`, `RFC822.SIZE`) gives the Message-ID. A message the archive
already holds is only filed under this folder. Others are fetched whole
(`BODY.PEEK[]`), parsed, and stored. `BODY.PEEK` is used throughout, so
archiving never marks mail as read; a test fails if a fetch would set
`\Seen`.

A message larger than 100 MB is stored from its headers alone, with the
text "This message is larger than arc5 downloads (100 MB)".

If a folder's `UIDVALIDITY` changes, its UIDs are forgotten and read again:
messages are matched by Message-ID, so nothing is stored twice.

### Failure handling

As in the main spec, with: a refused login is "needs login"; a server that
drops the connection is retried with backoff by the runner; a message that
cannot be parsed is stored with its original and the text "This message
could not be read", never skipped.

## 8. Interface

The mail layout, as in the mockups:

- **Left column:** the mailbox's name and address, then Inbox, Sent and All
  mail, then the owner's other archived folders, and a note that deleted
  mail stays. "All mail" is every archived conversation of the mailbox.
  For "Everything at once" the column lists Inbox, Sent and All mail across
  every mailbox.
- **Conversation list:** rows with the initials of the latest sender, their
  name, address and "and you" (or the other participants), message count,
  subject in bold, the start of the latest message, an attachment mark,
  "Deleted at source" when every message is, and the date. "With
  attachments" and "Deleted at source" filter the list. "Older
  conversations" pages.
- **Thread:** "Back to Inbox", the subject, "5 messages, 25 to 27 September
  2026", then one card per message: initials, name, address, recipients,
  date, "4 of 5"; earlier messages folded to one line, the last two open;
  the owner's tinted; quoted text folded; attachments as file cards with
  "Open"; "Download original (.eml)". Message text is shown as text, never
  as HTML.
- **Search** in a mail source says "Search mail by word, sender, address or
  subject"; the subject is indexed with the text, and people are found by
  name or address. Results replace the conversation list; a hit opens its
  thread at that message.
- **Phone:** the folders become a row of links above the list; a thread is
  its own screen, as a chat is.

## 9. Security

- No HTML from mail is ever rendered: text only, through `html/template`
  escaping, under the existing CSP. Remote images are never fetched.
- Attachments follow the existing media rules: an allowlist of image, audio
  and video types inline; everything else, including HTML, SVG and the
  original `.eml`, is a download, served with `nosniff` and the media
  sandbox.
- TLS certificate verification is never disabled.

## 10. Testing

- **Parsing:** MIME fixtures (multipart/alternative, nested multipart,
  charsets including KOI8-R and Shift_JIS, quoted-printable, base64,
  RFC 2047 headers, inline images, `winmail.dat` left as a file, broken
  MIME) to parsed messages.
- **HTML to text:** the corpus of section 6.
- **Threading:** references, In-Reply-To only, no references, subjects.
- **The connector** against a fake client: folders and roles (Gmail and
  not), backfill with checkpoints, a message in two folders stored once,
  new mail, deletions and moves, UIDVALIDITY change, needs login.
- **The client** against go-imap's in-process server
  (`imapserver/imapmemserver`): login, list, select, search, fetch, IDLE,
  expunge, and fetching never sets `\Seen`.
- **Manual:** one Gmail mailbox with an app password (milestone 5's list).

## 11. Build order

1. Archive: chat kind `thread`, subjects, addresses, folders, message
   folders, the original; mail parsing and HTML to text.
2. The mail layout and its search, on a demo mailbox.
3. The IMAP connector: login, folders, catch-up, new mail, deletions.

## Decisions for the owner to confirm

1. IMAP only; no JMAP or provider APIs in v1. go-imap v2 beta.8, pinned.
2. No OAuth2 in v1: Gmail by app password; Microsoft mailboxes, and
   Workspace accounts that forbid app passwords, cannot be added yet.
3. A thread is a chat of a new kind, `thread`.
4. **Changed:** Gmail through standard IMAP folders (All Mail for bodies,
   Inbox and Sent for membership by Message-ID), because the library cannot
   fetch Gmail's identities, threads or labels.
5. **Changed:** threads are named by their first message (`References`,
   then `In-Reply-To`), never by subject; no merging, so the archive never
   deletes a chat.
6. Drafts, Junk and Trash are not archived by default; with an All Mail
   folder, only Inbox, Sent and All Mail are.
7. Mail is shown as text only; HTML is converted, never rendered.
8. **Changed:** the original message is a file in the media store, not in
   `messages.raw`; attachments under the size limit are kept twice (inside
   the original and as their own file).
9. A message is deleted at source only when gone from every folder, and is
   no longer deleted if seen again.
10. **Changed:** new mail is found by a one-connection cycle (new mail in
    every folder, then deletions, then a minute of history, then IDLE),
    rather than IDLE plus a separate five-minute poll; deletions are found
    by comparing message counts and then UIDs, since Gmail lacks QRESYNC.
