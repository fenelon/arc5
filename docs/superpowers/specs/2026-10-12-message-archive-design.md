# arc5: self-hosted message archive — design

Date: 2026-10-12
Status: approved in conversation, awaiting written-spec review
Working name: `arc5` (binary, env prefix `ARC5_`)

## 1. Purpose

A self-hosted web tool that keeps a durable, searchable copy of personal
message history, so the owner can clean out accounts periodically or run with
auto-delete enabled without losing anything.

Sources plug in through connectors. The first version ships the core and a
Telegram connector. Email (IMAP) and WhatsApp follow as separate specs.

### Who it is for

One person running one instance for their own accounts. It will be
open-sourced later, so nothing personal is hard-coded, connector boundaries are
clean, and setup happens in the web interface rather than by editing files.

### Success criteria

- A message that arrives in an archived Telegram chat while arc5 is running is
  in the archive, with its media (under the size cap), before Telegram's
  auto-delete removes it.
- Deleting or editing a message at the source never removes anything from the
  archive.
- Searching a word finds messages containing other forms of that word in each
  configured language, in well under a second on an archive of a few million
  messages.
- It runs as one process with one data folder on a small VPS or home server.

### Decisions made during brainstorming

| Topic | Decision |
|---|---|
| Audience | Single user now, open source later |
| First connector | Telegram; email next; WhatsApp deferred |
| Capture | Continuous live capture plus backfill; strictly one-way |
| Chat scope | Private chats and groups on by default; channels and bots opt-in |
| Media | Download under a configurable size cap; placeholder above it |
| Languages | English plus other space-separated languages, chosen in settings |
| Backend | Go |
| Architecture | Single binary, compiled-in connectors, SQLite with FTS5 |
| JavaScript | Stimulus, vendored, no frontend build step |
| Password | Set in the interface, or supplied via `ARC5_PASSWORD` |
| Backfill | Per-account age limit and speed setting |
| Self-destructing media | Captured only when opted in; never reported as opened |

## 2. Architecture

One Go binary. One data folder containing `arc5.db` (SQLite, WAL mode,
`synchronous=FULL` so an acknowledged message survives a power cut) and
`media/`.

```
 connectors ──events──▶ archive ──▶ SQLite + media files
 (telegram, …)             │
      ▲                    ▼
   runner               search ◀── web interface
                                        │
                           alert ◀── runner status
```

| Unit | Responsibility | Depends on |
|---|---|---|
| `archive` | The only writer to the database. Accepts normalized events, stores messages, versions, attachments and media files. Owns migrations. | SQLite, filesystem |
| `search` | Text analysis (normalizing, stemming), query parsing, running queries, snippets. It decides what goes into the index; the archive writes the rows. The index is derived and rebuildable. | `archive` (read), SQLite |
| `connector` | The connector interface, registry, and the runner that supervises connector instances. | `archive` (via `Sink`) |
| `connector/telegram` | Telegram protocol: login, backfill, live updates, media download. Knows nothing about storage or search. | `connector`, gotd/td |
| `web` | HTTP server, login, server-rendered pages, media serving. | `archive`, `search`, `connector` |
| `alert` | Sends the outage webhook. | runner status |

### Layout

```
cmd/arc5/              main, subcommands: serve, backup, reindex
internal/model/        event types shared by archive and connectors
internal/archive/      store, migrations, media store
internal/search/       analyze, index, query, snippet
internal/connector/    interfaces, registry, runner
internal/connector/telegram/
internal/web/          handlers, templates, static (Stimulus controllers, CSS)
internal/alert/
internal/config/
```

### Libraries

- Telegram: `github.com/gotd/td` (pure Go MTProto client with an updates
  manager that handles gap recovery).
- SQLite: `modernc.org/sqlite` (pure Go, keeps builds cgo-free). The first
  implementation step verifies FTS5 is available in it; if not, fall back to
  `github.com/mattn/go-sqlite3` built with the FTS5 tag.
- Stemming: `github.com/blevesearch/snowballstem`, a Go port of the Snowball
  stemmers.
- Unicode normalization: `golang.org/x/text/unicode/norm`.
- Password hashing: argon2id from `golang.org/x/crypto`.
- Stimulus 3.2.2: the single-file ES module build, vendored into
  `internal/web/static`.
- Typeface: Geologica (variable, SIL Open Font License), vendored as four
  subset files (Latin, Latin Extended, Cyrillic, Cyrillic Extended). Nothing is
  loaded from another site at run time.

## 3. Connector interface

```go
type Connector interface {
    // Run performs backfill and then live capture. It blocks until ctx is
    // cancelled or an unrecoverable error occurs.
    Run(ctx context.Context, sink Sink, state State, settings map[string]string) error

    // Setup returns the interactive flow used to connect an account.
    Setup(state State) SetupFlow

    // Settings describes the connector's per-account options so the web
    // interface can render them without connector-specific code.
    Settings() []Field
}

type Sink interface {
    UpsertChat(Chat) error
    UpsertSender(Sender) error
    // New or edited; idempotent. Returns the attachments still to download.
    UpsertMessage(Message) ([]AttachmentRef, error)
    MarkDeleted(chatSourceID string, messageSourceIDs []string) error
    LookupChats(messageSourceID string, kinds []ChatKind) ([]string, error)
    PutMedia(ref AttachmentRef, r io.Reader) error
    SetAttachmentStatus(ref AttachmentRef, s AttachmentStatus, reason string) error
    ChatArchived(chatSourceID string) (bool, error)
    RequestedMedia() <-chan AttachmentRef   // "fetch now" and retry, ignoring the cap
    ReportProgress(Progress)                // backfill progress, connection state
}

type State interface {                      // per-account key-value store
    Get(key string) ([]byte, error)
    Set(key string, value []byte) error
}

type SetupFlow interface {
    // Next submits the previous step's input and returns the next step, or
    // a step with Done set.
    Next(ctx context.Context, input map[string]string) (Step, error)
}

type Step struct {
    Title  string
    Help   string
    Fields []Field   // name, label, kind (text, secret, number, choice)
    Done   bool
}
```

Connectors are registered at build time with a small description:

```go
type Info struct {
    Name    string  // "telegram"; stored on each account
    Title   string  // "Telegram"; shown on the source's tile
    Display Display // DisplayChats or DisplayMail
}
```

`Display` decides how a source is shown: messenger connectors as chats, mail
connectors as mail. The interface never asks the owner to choose a layout. An account is one configured
instance of a connector. The runner starts every enabled account's connector,
restarts it with exponential backoff (capped at five minutes) when `Run`
returns an error, and keeps the latest `Progress` per account for the web
interface and the alert unit.

Settings are passed to `Run` once. When the owner changes an account's
settings or toggles archiving for one of its chats, the runner cancels and
restarts that connector. Checkpoints and gap recovery make a restart lossless,
so connectors need no live-reconfiguration code.

"Fetch now" and "retry" in the interface set the attachment back to `pending`
with `requested = 1` and deliver its reference on `RequestedMedia`; the
connector downloads it through its live queue regardless of the size cap. The
flag is stored, so a request made while the connector is down is delivered
when it next starts.

`LookupChats` exists because some sources report deletions without naming the
chat (see Telegram, section 6). It is a read helper, not a storage leak: the
connector decides which chat kinds to search.

A future out-of-process connector would be one more compiled-in connector that
speaks a wire protocol to a subprocess; the core does not change.

## 4. Data model

```
accounts(id, connector, label, settings JSON, enabled, created_at)

chats(id, account_id, source_id, kind, title, archived, created_at,
      UNIQUE(account_id, source_id))
      kind: private | group | channel | bot

senders(id, account_id, source_id, display_name, username,
        UNIQUE(account_id, source_id))

messages(id, chat_id, source_id, sender_id, kind, sent_at, text,
         outgoing, reply_to_source_id, forwarded_from, edited_at,
         edit_dated, deleted_at, captured_at, raw BLOB,
         UNIQUE(chat_id, source_id))
         kind: message | service

message_versions(id, message_id, text, replaced_at)

attachments(id, message_id, position, kind, file_name, mime_type, size,
            sha256, status, reason, ephemeral, requested)
            kind:   photo | video | audio | voice | document | sticker | other
            status: pending | stored | skipped | failed | unavailable

connector_state(account_id, key, value BLOB, PRIMARY KEY(account_id, key))
settings(key, value)
web_sessions(token_hash, created_at, expires_at)
messages_fts   -- FTS5, see section 5
```

Rules the archive enforces:

- **Idempotent ingest.** `UpsertMessage` keys on `(chat_id, source_id)`.
  Receiving the same message twice changes nothing. Backfill and live capture
  may overlap, and a connector may replay after a crash.
- **Edits keep history.** If an upsert carries different text from the stored
  message, the old text is written to `message_versions` and `edited_at` is set.
  A replayed older copy never reverts a later edit: dated text is ignored if
  the source dated the stored text later, and undated text is ignored if the
  source dates its edits or if that exact text was already replaced.
  `edit_dated` records whether `edited_at` came from the source or is our
  capture time; only source times are compared.
- **Deletions are marks.** `MarkDeleted` sets `deleted_at`. No row or file is
  removed because the source removed it.
- **Raw payload.** `raw` holds the source's original message object as
  compressed JSON, so fields not modelled in v1 (reactions, polls, formatting
  entities) can be derived later without re-fetching.
- **Outgoing.** `outgoing` is 1 for messages the account owner sent. Only the
  connector knows this; the interface uses it to set the owner's messages
  apart.
- **Service messages** (joins, calls, pinned-message notices) are stored with
  `kind = service` and a short rendered description in `text`. They appear in
  the chat view and are excluded from search by default.
- **Media files** are stored at `media/<aa>/<bb>/<sha256>`, so identical files
  are kept once. `attachments.sha256` points at the file.
- **Size cap.** A global setting, default 50 MB. An attachment over the cap is
  recorded with `status = skipped` and can be fetched on demand from the
  interface while the source still has it.

## 5. Search

### Indexing

`messages_fts` is a contentless FTS5 table with two columns, `exact` and
`stem`, keyed by `messages.id`. It uses the `ascii` tokenizer, which here only
splits on spaces: all language handling happens in the `search` unit's
analyzer. The archive calls the analyzer inside the transaction that stores a
message and writes the two columns it returns, so a stored message is always
searchable and the archive remains the only writer.

The analyzer:

1. Splits text into words: runs of letters, numbers and combining marks.
   Invisible characters inside a word (soft hyphen, zero-width joiners) do not
   split it.
2. Normalizes each word: Unicode NFKC, lowercase, `ё` to `е`, stress marks
   removed from Cyrillic vowels, and accents removed from Latin and Greek
   letters. Text is canonically composed first and half-width kana widened, so
   text that reads the same is indexed the same.
3. Writes the normalized word to `exact`.
4. Writes to `stem` the output of every configured language's stemmer whose
   script matches the word, keeping distinct results.
5. For scripts written without spaces (Chinese, Japanese, Thai, Lao, Khmer,
   Burmese), writes overlapping pairs of characters to `exact` instead of
   words, then the last character of the run on its own, and no stems. Every
   character therefore begins a token, so a one-character query finds it.

Stemming is available for Arabic, Danish, Dutch, English, Finnish, French,
German, Hungarian, Irish, Italian, Norwegian, Portuguese, Romanian, Russian,
Spanish, Swedish, Tamil and Turkish. Text in any other language is still
searchable by exact word, prefix and phrase, but not by word form.

There is no per-message language detection; it is unreliable on short chat
messages. The owner picks languages in settings (default: English). Each
extra language that shares a script adds one more stem per word, so the list
should hold the languages actually used, not all of them.

The index records the signature of the analyzer that built it (analyzer
version plus language list). A process that stores a message while using a
different analyzer, or none, marks the index dirty in the same transaction.
When the signature no longer matches or the index is dirty, it is rebuilt in
the background at startup; `arc5 reindex` rebuilds it on demand and
`arc5 reindex --languages en,ru,es` changes the list first. A rebuild runs in
small batches so ingestion continues, and messages that arrive during it are
indexed as usual.

Indexed text is the message's current text plus its attachment file names.
Earlier versions of an edited message are visible in the chat view but not
searched. Deleted messages stay indexed.

### Querying

- Terms are ANDed. A term matches if its exact form is in `exact` or any of
  its stems is in `stem`.
- `"quoted text"` is a phrase match against `exact` only. Text in a script
  without spaces is always matched as a run of adjacent character pairs.
- The last term of an unquoted query also matches as a prefix, for
  search-as-you-type, unless the query ends with a space or the term is
  shorter than three characters. A one- or two-letter prefix matches a large
  share of the archive, which is slow to rank and no use to see.
- Ranking is BM25 with `exact` weighted above `stem`. The owner can switch to
  newest-first.
- Filters: account, chat, sender, date range, has attachment, deleted at
  source, include service messages. Filters are SQL conditions joined to the
  FTS match.
- Results are paged 50 at a time. Each shows chat, sender, time, a snippet,
  and any attachment file names the query matched, highlighted.
- Snippets are built in Go: re-tokenize the original text, mark tokens whose
  exact form or stems are in the query's term set, and cut a window around the
  first mark.
- Opening a result goes to the chat view positioned on that message.

Not in v1: typo tolerance, searching text inside documents.

## 6. Telegram connector

### Access and login

The connector signs in as the owner's user account; bots cannot read personal
chats. The owner supplies their own `api_id` and `api_hash` from Telegram's
developer page. The setup flow steps are: API credentials, phone number, login
code, two-step password (only if the account has one).

The session is stored in `connector_state`. It grants full account access.
v1 protects it with file permissions (data folder `0700`, database `0600`) and
a documented warning; encryption at rest is out of scope.

arc5 appears in Telegram's active sessions list and can be revoked from any
other device. On revocation the account's status becomes "needs login".

### Chat mapping

| Telegram | `chats.kind` | Archived by default |
|---|---|---|
| User (including Saved Messages) | private | yes |
| Basic group, supergroup | group | yes |
| Broadcast channel | channel | no |
| Bot | bot | no |

Defaults apply when a chat is first seen. The owner can toggle any chat. The
connector calls `ChatArchived` before fetching or storing anything for a chat.
Secret chats are not visible to the API and are not listed.

### Backfill

Per account, in settings:

- **Age limit:** all history, messages since a date, or the last N months.
  Default: all history.
- **Speed:** slow (one history request per 5 s), normal (one per 2 s, the
  default), fast (one per 0.5 s). Pause and resume.

For each archived chat the connector pages backwards, 100 messages per request,
from the newest message to the age limit, storing a per-chat checkpoint in
`State` after every page. Chats are processed most-recently-active first.

- Extending the age limit resumes each chat from its checkpoint.
- Shortening it stops further fetching and deletes nothing.
- Turning archiving on for a chat queues it for backfill.
- Telegram's flood-wait responses are always obeyed in full, whatever the speed
  setting.
- Speed and age limit affect backfill only.

### Live capture

gotd's updates manager delivers new messages, edits and deletions, and
recovers gaps after a reconnect using Telegram's difference mechanism. Its
state is persisted through `State`. If Telegram reports the gap as too long to
replay, the connector runs a catch-up for each archived chat from the newest
stored message forward.

Deletion updates for private chats and basic groups carry message IDs without
a chat; those IDs are unique per account, so the connector resolves the chat
with `LookupChats`. Supergroup and channel deletions name the channel.

### Media

Attachments are queued for download when their message is stored. The live
queue always drains before the backfill queue, so media on an auto-deleting
message is fetched before it expires. Backfill media downloads follow the
backfill speed setting; live downloads do not. A download is retried three
times with backoff, then marked `failed` and can be retried from the interface.
Media that Telegram no longer has is marked `unavailable`.

### Self-destructing and view-once media

Telegram starts the self-destruct countdown when a client reports the media as
opened (`messages.readMessageContents`). Fetching the file is not a listed
trigger.

- The connector never calls `messages.readMessageContents` or
  `channels.readMessageContents`. This is a hard rule with a test that fails if
  either call appears in the connector's client interface.
- Capturing such media is off by default and enabled per account by a setting
  ("Archive self-destructing media"). When off, the message is stored and the
  attachment is recorded as `skipped` with `ephemeral = 1`.
- When on, the file is downloaded through the live queue and the attachment is
  stored with `ephemeral = 1`, which the chat view shows as a label.
- The claim that downloading alone leaves the media intact for other devices is
  inferred from the documented trigger, not stated outright. It is confirmed by
  a manual test (section 9) before the setting is documented as safe.

Chat-level auto-delete timers run from send time and are unaffected by
anything the connector does.

### What cannot be saved

- Secret chats.
- Anything deleted before the first backfill reached it.
- Anything both sent and deleted while arc5 was not connected.

### Failure handling

| Failure | Behaviour |
|---|---|
| Network drop | Reconnect with backoff; gap recovery on return |
| Flood wait | Sleep the stated time; status shows "rate limited until …" |
| Session revoked or expired | Status "needs login"; no retry until the owner logs in again |
| Media download fails | Three retries, then `failed`, retryable by hand |
| Disk full or write error | Ingestion pauses for all accounts with a visible error; nothing is acknowledged or dropped; resumes when writes succeed |
| Unexpected error in `Run` | Runner restarts the connector with backoff and records the error |

## 7. Alerts

With auto-delete on, an unnoticed outage is permanent loss.

- Every page shows a banner while any enabled account is not connected or
  ingestion is paused.
- An optional webhook URL in settings receives a JSON `POST` when an account
  has been not connected for longer than a threshold (default 15 minutes), when
  ingestion pauses, and once more on recovery. Body: account label, connector,
  status, since, last error. Delivery is retried three times; failures are
  logged and shown on the settings page.

## 8. Web interface

Server-rendered Go templates with CSS classes only (no inline styles or
scripts, so a strict content-security policy holds). JavaScript is Stimulus
controllers loaded as ES modules from files embedded in the binary; they fetch
HTML fragments from the server and swap them in. The approved mockup is at
https://claude.ai/artifact/ErJ5ojBuFs8pTM2CLAUsyj.

### Look

One typeface, Geologica. Two themes:

| | Light | Dark |
|---|---|---|
| Ground / pane | `#EEF3F5` / `#FFFFFF` | `#17181A` / `#1E2022` |
| Raised surface | `#FFFFFF` | `#26282B` |
| Lines | `#CFDBDF` | `#34373B` |
| Text / muted | `#0D2530` / `#4C6470` | `#ECEDEE` / `#A4A8AD` |
| Accent | teal `#0B6E7F` | orange `#FF8A1F` |
| Owner's messages | `#D7ECEF` | `#3A2A17` |
| Search hit | `#F4D38A` | `#5A4A00` on `#FFE98A` text |
| Needs attention | `#7A4700` on `#FBEBCB` | `#FFA79B` on `#472320` |
| Live | `#1E9E6A` | `#5FD08C` |

The theme follows the device unless the owner picks light or dark, with the
button in the top bar or in Settings. The choice is kept in a cookie so the
server renders the right theme with no flash.

### Sources and layouts

A **source** is one account. Its tile shows the service logo in a tinted
square, the name the owner gave it as the title, the service name under it, and
a state mark: a green dot for live, a padlock for needs login, circling arrows
for catching up.

The **source picker** sits in the top bar. Closed, it is the current source's
tile. Open, it lists every source as tiles in a three-column grid, grouped
under "Messengers" and "Mail". The first tile of each group is "Everything at
once", which shows all sources of that kind in one list. The footer has one
link, "Manage sources".

The layout follows the selected source's display type:

- **Chats** (messenger connectors): conversations on the left, newest first,
  each with its last message and date; the open conversation on the right; the
  search box above. The owner's messages sit on the right in the theme's
  "owner" colour. A **year rail** runs down the right edge of a conversation:
  one block per year, four bands per year showing how much was said in each
  quarter, the current position marked in the accent colour; each band jumps to
  that quarter. On a phone the list and the conversation are separate screens
  and the rail becomes a year picker.
- **Mail** (mail connectors): folders on the left (Inbox, Sent, All mail) under
  the mailbox's name and address; conversations as rows with sender name and
  address, message count, subject, snippet, attachment mark and date. A thread
  shows each message as its own card with initials, name, address, recipients,
  date and position ("4 of 5"); earlier messages are folded to one line, and
  the owner's are tinted. The mail layout is built with the email connector.

Searching replaces the left column with results: matching chats and people
first, then message hits with highlighted snippets and matched file names.
Choosing a chat opens it; choosing a person shows their messages; choosing a
hit opens the conversation at that message. Search covers whatever the picker
has selected.

### Pages

| Page | Contents |
|---|---|
| Source view | The chats or mail layout above |
| Sources | Every source as a tile with a line of detail; the selected source's settings in one panel below (name, which chats are archived, history catch-up with age and speed, self-destructing media, sign out, remove); "Add a source" and "Log in again" run the connector's setup flow in that panel |
| Settings | Appearance, search languages, attachment size limit, outage alert, password |
| Login, first run | A password field |

A banner appears on every page while any enabled source is not connected or
ingestion is paused.

Stimulus controllers: theme, picker, search (debounced query, results
fragment), setup-flow, progress (polling), attachment (fetch now, retry).

### Security

- **Password.** If `ARC5_PASSWORD` is set, it is the login password and the
  settings field is read-only. Otherwise the owner sets a password on first
  visit; it is stored as an argon2id hash in `settings`. Until a password
  exists, every route redirects to the first-run page.
- **Sessions.** Random token in an `HttpOnly`, `SameSite=Lax` cookie (`Secure`
  when served over TLS or behind a proxy that says so); only its hash is
  stored. Sessions last 30 days. Changing the password clears them, including
  when `ARC5_PASSWORD` differs at startup from the value last seen (detected by
  comparing against a stored hash).
- **Login throttling.** Five failed attempts start an increasing delay.
- **CSRF.** Every state-changing request carries a per-session token.
- **Untrusted content.** All message text and names are escaped by the
  templates. Pages are served with a content-security policy that allows
  scripts, styles, fonts and images only from arc5 itself. Media is served
  only to a logged-in session, with `nosniff` and a sandboxing policy. Only an
  allowlist of image, audio and video types is served inline; everything else,
  including HTML and SVG, is served as a download.
- **Network.** Listens on `127.0.0.1:8080` by default. Documentation directs
  the owner to a TLS reverse proxy or a VPN for remote access.
- **Outbound.** Only to configured sources and the alert webhook.

## 9. Deployment and operations

- `arc5 serve` runs everything. Flags and environment: `--listen` /
  `ARC5_LISTEN`, `--data` / `ARC5_DATA`, `ARC5_PASSWORD`. Everything else is
  configured in the interface.
- `arc5 backup <destination>` writes a consistent copy of the database while
  the server runs (`VACUUM INTO`). Media is plain files, copied by any tool.
- `arc5 reindex [--languages en,ru,es]` rebuilds the search index, changing
  the language list first if one is given.
- Migrations are embedded and run at startup.
- Distributed as a static binary and a Docker image with the data folder as a
  volume.

## 10. Testing

- **Archive:** against a real SQLite file in a temp directory. Idempotent
  upsert, edit versioning, deletion marks, media de-duplication, size cap.
- **Search:** table-driven with multilingual fixtures: word forms per
  language, phrases, prefixes, each filter, ranking of exact above stemmed,
  snippets, reindex after a language change.
- **Connector contract:** a fake connector emitting duplicates, out-of-order
  events, edits, deletions with and without a chat, and media, verified against
  the archive. Runner restart and backoff with a failing fake.
- **Telegram mapping:** Telegram objects to archive events, on recorded
  fixtures, including service messages and self-destructing media.
- **Telegram behaviour:** backfill paging, checkpoints, resume, age limit,
  speed, flood-wait handling, live-before-backfill media priority, all against
  a fake client behind a narrow interface. That interface has no method for
  reporting media as opened, and a test asserts it stays that way.
- **Web:** handler tests for auth, CSRF, first-run, password from environment,
  media headers; end-to-end tests for login and search.
- **Manual, against a test account:** login including two-step password;
  backfill of a real chat; live message, edit and deletion; auto-delete timer
  expiring after capture; session revocation. With a second account, send a
  timed self-destructing photo and a view-once photo and let arc5 download
  both. Confirm the sender still sees each as unopened, then confirm each opens
  normally on the phone.

## 11. Out of scope for v1

WhatsApp; email; typo-tolerant search; search inside documents; multiple
users; encryption at rest; thumbnails; export to other formats; reactions and
polls as first-class data (kept in `raw`); sending, deleting or otherwise
changing anything at the source; choosing a licence and publishing.

## 12. Build order

The implementation plan should be split into milestones that each leave a
working, tested program:

1. Archive, connector interface, runner, fake connector, `serve` skeleton.
2. Search: analysis, index, query, snippets, `reindex`.
3. Web interface, in two plans:
   a. Shell and chats: login, themes, source picker, the chats layout with the
      year rail, media, and a demo archive to look at.
   b. Search in the interface, the Sources page with setup flows, Settings.
4. Telegram connector: login and setup flow, backfill, live capture, media.
5. Alerts, `backup`, Docker image, self-destructing media option and its
   manual test.

The email connector and the mail layout follow as their own spec, then
WhatsApp.
