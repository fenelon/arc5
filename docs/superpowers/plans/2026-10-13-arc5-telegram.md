# arc5 Telegram Connector and Avatars (Milestone 4) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Archive a Telegram account: log in from the Sources page, backfill history at a chosen speed and age, capture new messages, edits and deletions live, download media (live before backfill), and show chat and people pictures (avatars) in the interface. Record media that an edit replaces instead of ignoring it.

**Architecture:** `internal/connector/telegram` talks to Telegram through gotd/td, behind a narrow `api` interface that the tests fake. Pure mapping functions turn Telegram objects into `model` events. The connector keeps its gotd session, update state, peer access hashes and backfill checkpoints in its `connector.State`. The archive gains avatar columns, attachment source identities and replaced media; the Sink gains `AvatarStored` and `PutAvatar`; the web interface shows avatars and replaced media.

**Tech Stack:** Go 1.26, `github.com/gotd/td` v0.162.0 (new; named by the spec), SQLite (modernc), `html/template`.

**Spec:** `docs/superpowers/specs/2026-10-12-message-archive-design.md`, sections 3, 4, 6, 8 and 10, as amended by this plan's first commit (avatars, media an edit replaces, Telegram chat source IDs).

**How this plan was written.** The owner asked for the Telegram work and avatars while asleep, with "continue implementing, don't stop". This plan was therefore written to be executed straight away rather than reviewed first, and unlike plans 3a and 3b it does not carry pre-run code: each task gives the exact interfaces it produces and consumes and the tests to write first; the code is in the task's commit. The decisions below were taken by the executor and are listed for the owner to confirm afterwards.

## Decisions for the owner to confirm

1. **Media an edit replaces** (open in STATUS). Attachments carry the source's file identity. When an edit brings a different file at a position, the old attachment is kept as replaced (shown with the message's earlier versions) and the new one takes the position; a replaced attachment never downloaded is marked unavailable. Cost if wrong: a migration column and the chat view's earlier-versions block.
2. **Basic groups and supergroups** (open in STATUS). Chat source IDs name the Telegram type (`user:`, `chat:`, `channel:`), so a deletion without a chat is resolved among `user:` and `chat:` chats only. No schema change. Cost if wrong: source IDs are stored, so changing the format later needs a data migration; do it before real accounts are added.
3. **Avatars** are the 160-pixel picture Telegram already provides, kept like media (content-addressed, never deleted because the source removed it). Only the current picture is tracked, not a history. Cost if wrong: a table of picture versions later.
4. **Live capture runs alongside backfill**, not after it, so new messages are never held up by history. Progress says "catching up" until backfill ends.
5. **"Pause catch-up"** is a connector setting (a checkbox), not a separate button: saving it restarts the connector like every other setting.
6. **Login keeps one connection open between steps** (the flow implements `io.Closer`), as the 3b plan foresaw.
7. **A chat Telegram reports as migrated** (basic group upgraded to a supergroup) stays as its own chat; the supergroup is a new chat. Cost if wrong: the two halves of a conversation sit in two chats.
8. **The manual test of spec section 10 cannot run here** (no Telegram account). Everything is tested against the fake client and recorded fixtures; the manual test stays on milestone 5's list, and STATUS says the connector is unverified against the real service.

## Global Constraints

- No cgo. `internal/archive` stays the only database writer.
- The connector never calls `messages.readMessageContents` or `channels.readMessageContents`: the `api` interface has no such method, and a test fails if either name appears in the package's non-test source.
- Self-destructing media capture is opt-in per account; when off, such attachments are recorded `skipped` with `ephemeral`.
- Flood waits are obeyed in full, whatever the speed setting.
- Durability: nothing is acknowledged (checkpoint advanced) before the archive has stored it.
- Every task ends with `gofmt -l internal cmd` printing nothing and `go vet ./... && go test -race -count=1 -timeout 180s ./...` passing.

## Review Focus

1. **The readMessageContents ban** holds in the interface and in the source.
2. **Checkpoints never pass unstored data.** A backfill page's checkpoint is written only after every message of the page is stored; a crash between them replays the page, which the archive's idempotence absorbs.
3. **Deletions without a chat** never mark a message in a supergroup or channel.
4. **Needs login.** A revoked or missing session makes `Run` return `connector.ErrNeedsLogin`, never a retry loop.
5. **Avatars and replaced media** are served under the existing media rules (session required, allowlisted image types, `nosniff`).

## File Structure

```
internal/model/model.go                        modify: Attachment.SourceID, AvatarRef
internal/archive/migrations/0005_avatars.sql   create
internal/archive/{messages,chats,media,read}.go modify: replaced media, avatars
internal/connector/{connector,sink}.go         modify: AvatarStored, PutAvatar
internal/connector/demo/demo.go                modify: demo avatars, an edited photo
internal/connector/telegram/
    ids.go        chat and sender source IDs, peers
    mapping.go    tg objects → model events
    store.go      session, update state, access hashes, checkpoints on State
    api.go        the narrow client interface and its gotd implementation
    setup.go      login flow, sign-out
    telegram.go   Connector: Info, Settings, Run
    sync.go       dialogs → chats
    backfill.go   history paging
    live.go       updates
    media.go      download queues, avatars
internal/web/...                               modify: avatars, replaced media
cmd/arc5/main.go                               modify: register telegram
```

---

### Task 1: Replaced media and avatars in the archive

**Produces:**
- `model.Attachment.SourceID string`
- `type model.AvatarKind string` (`AvatarChat`, `AvatarSender`), `type model.AvatarRef struct { Kind AvatarKind; SourceID, AvatarID string }`
- migration `0005_avatars.sql`: `chats.avatar_id TEXT`, `chats.avatar_sha256 TEXT`, `senders.avatar_id`, `senders.avatar_sha256`, `attachments.source_id TEXT`, `attachments.replaced_at INTEGER`
- `(*Store).AvatarStored(accountID int64, ref model.AvatarRef) (bool, error)`; `(*Store).PutAvatar(accountID int64, ref model.AvatarRef, r io.Reader) error` (stores the file in the media store, durably, then the identity and hash; `ErrUnknownChat`/`ErrNotFound` style errors for an unknown chat or sender; `ErrInvalid` for an empty identity)
- `UpsertMessage`: an attachment with a `SourceID` different from the stored one at its position replaces it (see the spec rule)
- read side: `StoredAttachment.Replaced time.Time`; `ChatMessages` returns replaced attachments separately (`Message.ReplacedAttachments`); `ChatSummary.AvatarSHA`, `Chat.AvatarSHA`, `SenderInfo.AvatarSHA`, `Message.SenderAvatarSHA` (via the sender), `(*Store).Avatar(sha string) (path string, ok bool)` is not needed: avatars are served through `MediaPath`.

**Tests first** (`internal/archive/avatars_test.go`, `messages_test.go`): `TestPutAvatar` (stored, deduplicated, `AvatarStored` true only for the same identity, unknown chat refused, empty identity refused, a later picture replaces the hash, `AvatarStored` false for a new identity); `TestEditReplacesMedia` (same identity: unchanged; new identity: old row replaced with negative position and `replaced_at`, new row pending at the position and returned for download; a pending replaced row becomes unavailable; no identity: first stays); migration test count.

### Task 2: The Sink and the demo

**Produces:** `connector.Sink.AvatarStored(model.AvatarRef) (bool, error)`, `Sink.PutAvatar(model.AvatarRef, io.Reader) error` (account sink classifies errors as before); demo connector gives three chats and two people generated PNG pictures, and one message an edited photo.

**Tests first:** sink test for both methods; demo test that the pictures are stored once (a second run does not re-send them: `AvatarStored` true) and that the edited photo leaves a replaced attachment.

### Task 3: Avatars and replaced media in the interface

**Produces:** routes `GET /avatars/chat/{id}` and `GET /avatars/sender/{id}` (session required; 404 without a picture; served like media: allowlisted image types inline, `nosniff`, sandbox, `Cache-Control: private, max-age=86400`, ETag of the hash); templates show `<img class="avatar__img">` inside the avatar where a picture exists (chat list, conversation header, search chat and person rows, group senders); the conversation's earlier-versions disclosure lists replaced media ("Replaced by an edit") with their links.

**Tests first** (`internal/web/avatars_test.go`): `TestAvatarsAreServed` (headers, 404 cases, session needed); `TestChatListShowsPictures`; `TestReplacedMediaShowsWithEarlierVersions`; the route table test gains the two routes.

### Task 4: Telegram IDs and mapping

**Produces** (package `telegram`, pure functions):
- `chatID(peer tg.PeerClass) string` → `user:1`, `chat:2`, `channel:3`; `parseChatID(string) (kind string, id int64, ok bool)`
- `type entities` (users and chats by ID, from any response's `Users`/`Chats`), `newEntities(users []tg.UserClass, chats []tg.ChatClass)`
- `mapChat(peer, entities, selfID) (model.Chat, bool)`: kinds per spec (user → private, bot user → bot, self → private titled "Saved Messages", basic chat → group, megagroup → group, broadcast → channel); titles from names
- `mapSender(from tg.PeerClass, entities) (model.Sender, bool)`
- `mapMessage(m tg.MessageClass, entities, selfID, keepEphemeral bool) (model.Message, []mediaItem, bool)`: text, outgoing, sent and edit times, reply, forwarded-from name, service messages rendered as short text (joins, leaves, title changes, pins, calls, chat created, photo changed, migrations), `Raw` as JSON; attachments: photo, document kinds (voice, audio, video, sticker, file name, MIME type, size), `SourceID` `photo:<id>`/`document:<id>`; TTL media is `Ephemeral`, and `NoFetch` with reason when `keepEphemeral` is off; unsupported media (geo, contact, poll, web page…) adds no attachment but a line of text where useful
- `type mediaItem struct { Position int; Location tg.InputFileLocationClass; Size int64; DC int }`

**Tests first** (`mapping_test.go`): table tests on constructed tg objects for every kind and service message above, ephemeral on and off, view-once, edit date, forwarded names, and `Raw` holding valid JSON.

### Task 5: State-backed storage

**Produces:** `store.go`: `sessionStorage` (gotd `session.Storage` on key `session`); `updateStorage` implementing `updates.StateStorage`, `updates.ChannelAccessHasher` and `updates.UserAccessHasher` on keys `updates/state`, `updates/channel/<id>`, `hash/user/<id>`, `hash/channel/<id>`; `checkpoint` per chat (`backfill/<chatID>`: oldest message ID fetched, done flag) and `top/<chatID>` (newest seen); `apiCredentials` on key `api` (id and hash).

**Tests first:** round trips through an in-memory State; `ForEachChannels` lists what was set; absent keys report not found.

### Task 6: The client interface

**Produces:** `api.go`:
```go
type api interface {
	Self(ctx) (*tg.User, error)
	SendCode(ctx, phone string) (hash string, err error)
	SignIn(ctx, phone, code, hash string) error // errPasswordNeeded
	CheckPassword(ctx, password string) error
	LogOut(ctx) error
	Dialogs(ctx, offset dialogOffset) (dialogPage, error)
	History(ctx, peer tg.InputPeerClass, offsetID, minID, limit int) (historyPage, error)
	Messages(ctx, peer tg.InputPeerClass, ids []int) (historyPage, error)
	Download(ctx, loc tg.InputFileLocationClass, w io.Writer) error
}
```
plus `dial(ctx, creds, storage, handler) (api, func(), error)` running a gotd client with the updates manager in a goroutine, and `errPasswordNeeded`, `isUnauthorized(err)`, `floodWait(err) (time.Duration, bool)`.

**Tests first:** `TestNoReadMessageContents` (reflect over `api`'s methods; scan the package's non-test `.go` files for `ReadMessageContents`); unit tests of the page conversions on constructed `tg.MessagesDialogsSlice`/`MessagesChannelMessages` values.

### Task 7: Setup flow and sign-out

**Produces:** `setup.go`: steps "Your Telegram API keys" (api_id number, api_hash secret, help naming my.telegram.org), "Your phone number", "The login code", "Your two-step password" (only when Telegram asks); errors say what to do (wrong code, expired code, wrong password, bad API keys, flood wait with its time); the flow keeps its connection and closes it (`io.Closer`); on success the session and credentials are in State. `SignOut(ctx, state)`: logs out at Telegram if it can and clears the session.

**Tests first** (`setup_test.go` with a fake api): the four-step path, the three-step path, each refusal keeps the step, Close closes the connection, SignOut clears the session even when Telegram is unreachable.

### Task 8: Run: connect, sync chats, needs login

**Produces:** `telegram.go`: `Info{Name: "telegram", Title: "Telegram", Display: DisplayChats}`, registered by `init` in the default registry; `Settings()`: age limit (choice: All history, Last 12 months, Last 3 months, Since a date + date field), speed (Slow, Normal, Fast), pause catch-up (bool), keep self-destructing media (bool); `Run`: no session → `ErrNeedsLogin`; dial; `Self` unauthorized → `ErrNeedsLogin`; sync dialogs (every page) into chats and senders, store access hashes; report progress; then backfill and live in parallel until ctx ends.

**Tests first** (`run_test.go`, fake api + `connectortest` sink or an in-memory archive): needs login without a session and on unauthorized; dialogs become chats with the right kinds and default archiving; progress reports.

### Task 9: Backfill

**Produces:** `backfill.go`: archived chats most recently active first; pages of 100 backwards from the checkpoint (or newest) to the age limit; checkpoint after each stored page; pause setting; speed limiter (5 s, 2 s, 0.5 s); flood wait obeyed in full with progress "Rate limited until 14:05"; chats switched off are skipped; done chats are remembered; progress counts chats.

**Tests first** (`backfill_test.go`, fake clock): paging and checkpoints; resume after a restart from the checkpoint; age limit stops and a longer limit resumes; speed spacing; flood wait sleeps the stated time; pause.

### Task 10: Live capture

**Produces:** `live.go`: handlers for new messages (private, group, channel), edits, deletions (with and without a chat, per decision 2), chat and user updates (titles, pictures); the `top/<chat>` mark; `OnTooLong`/`OnChannelTooLong` run a forward catch-up per archived chat from `top`.

**Tests first** (`live_test.go`): each update kind stored; deletion without a chat marks only `user:`/`chat:` chats; catch-up fetches from the top forward.

### Task 11: Media and avatars

**Produces:** `media.go`: a downloader with two queues (live, backfill) and avatars between them; retries three times with backoff, then `failed`; a file Telegram no longer has → `unavailable`; an expired file reference is refreshed by fetching the message again; requested media (`RequestedMedia`) go to the live queue regardless of the cap; avatars downloaded when `AvatarStored` is false, a private chat's picture put for both the chat and its user.

**Tests first** (`media_test.go`): live drains before backfill; retries then failed; unavailable; refreshed reference; requested media; avatars once, private chat shares its user's picture.

### Task 12: Wiring, documentation, look at it

**Produces:** `cmd/arc5` imports the telegram package so it registers; README section "Archiving Telegram" (API keys, login, what is kept, the self-destructing media caution); STATUS updated. Look at the demo archive with avatars and an edited photo in both themes and on a phone; check "Add a source" offers Telegram and its first step renders.

**Tests:** `cmd/arc5` test that `telegram` is registered and not hidden.

---

## After the plan

Add "Post-review changes" and update STATUS: milestone 4 done but unverified against the real service; the manual test joins milestone 5. Then the email spec ("move towards mail integration").

## Post-review changes

Executed on branch `telegram`, one commit per task. The changes made beyond
the tasks as written:

**During the tasks** (each in the ledger as a ruling):
- The sink passes the archive's `ErrNotFound` through instead of treating
  it as a storage failure (Task 2).
- Avatars are served by content hash at `/avatars/{sha}`, and shown only
  where the mockups show pictures: the chat list and search (Task 3).
- A source refuses a login as a different Telegram account than the one it
  archives (Task 7).
- The connector registers through `telegram.Register`, like the demo, and
  its settings add a "Since" date (Task 8).
- gotd saves the update position even when the handler failed; the update
  storage now refuses position changes after a storage failure, so the
  update is asked for again (Task 10).
- From looking at the demo: replaced media is dated by the edit, and a login
  step's secret field no longer says "Unchanged" (Task 12).

**From the whole-branch review.** One critical and seven important
findings, plus two minors re-graded as important; each fixed test-first in
its own commit:

| Finding | Change | Test |
|---|---|---|
| The catch-up after a long disconnection fetched from the newest stored message when it ran, so a live message arriving first hid the gap; the queue was lost on restart | Gap markers in State, written in gotd's too-long callback, filled from the marker, removed after storing, resumed at start, retried after transient errors | `TestCatchUpSurvivesLiveMessagesAndRestarts` |
| One chat Telegram refuses stopped the run, every run | Per-chat refusals (4xx other than flood waits and dead sessions) skip the chat | `TestBackfillSkipsAChatTelegramRefuses` |
| A short age limit marked quiet chats as fully fetched | The checkpoint records the limit that stopped it | `TestALongerLimitReachesQuietChats` |
| A full disk during a download marked files failed | The archive's error is reported before the download's | `TestAFullDiskStopsTheDownloads` |
| Pending downloads were forgotten on restart | `PendingMedia` on the store and the sink; requeued at start | `TestARestartRequeuesWaitingFiles` |
| A late download could land on the file an edit put in its place | `AttachmentRef.SourceID`; stale refs refused and dropped | `TestAStaleRefCannotTouchTheReplacement`, `TestAStaleDownloadIsDropped` |
| An older copy of a message reverted an edited photo | Older copies replace no attachments | `TestAnOlderCopyDoesNotRevertAnEditedPhoto` |
| Flood waits on the chat list were not obeyed | `Dialogs` and `Self` wait them out | `TestTheChatListObeysFloodWaits` |
| The chat list stopped after a page ending on a channel the owner left (re-graded) | Forbidden channels keep their access hash | `TestDialogsPageEndingOnAForbiddenChannel` |
| A refused login as another account left that account's session (re-graded) | Logged out and cleared | `TestLoginRefusesAnotherAccount` |

Deferred minors: the `selfID` race before the first `Self`; picture changes
noticed on the next run; the reason given for requested self-destructing
media when the setting is off; an unreadable "Since" date meaning all
history.
