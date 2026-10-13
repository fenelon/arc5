# arc5 status and handoff

Last updated 2026-10-13. Read this first when picking the project up in a new
session.

## Where things stand

| Milestone | State |
|---|---|
| 1. Archive core, connector model, runner | Done, merged. Plan: `plans/2026-10-12-arc5-core.md` |
| 2. Search engine (analyzer, FTS index, names) | Done, merged. Plan: `plans/2026-10-12-arc5-search.md` |
| 3a. Web shell and chats view | Done, merged. Plan: `plans/2026-10-13-arc5-web-shell.md` |
| 3b. Search in the interface, Sources page with setup flows, Settings | Done, merged. Plan: `plans/2026-10-13-arc5-search-sources-settings.md` (its 13 decisions were taken as written) |
| 4. Telegram connector and avatars | Done, merged, **not yet run against a real Telegram account** (tested against a fake client only). Plan: `plans/2026-10-13-arc5-telegram.md`; its 8 decisions were the executor's calls and await the owner's confirmation |
| 5. Alerts, `backup`, Docker image, view-once option, the manual Telegram test | Not planned yet |
| Email (IMAP, Gmail first) and the mail layout | Done, merged, **not yet run against a real mailbox** (tested against a fake client and go-imap's in-process server). Spec: `specs/2026-10-13-email-archive-design.md` (revised for Gmail and HTML-only mail; 10 decisions at its end to confirm). Plan: `plans/2026-10-13-arc5-email.md` |
| Later | WhatsApp |

`master` is pushed and green: `go vet ./... && go test -race ./...`.

To look at the interface: `arc5 demo --data <folder>`, then
`ARC5_PASSWORD=<pw> arc5 serve --data <folder>`.

The design spec is `specs/2026-10-12-message-archive-design.md`. Section 8
describes the approved interface.

## The approved interface

The owner approved the "Strata" design. The mockup sources are in
`mockups/2026-10-13-interface/` (HTML fragments with inline styles; open them
in a browser, or read them for exact colours and spacing):

| File | Shows |
|---|---|
| `Strata.dc.html`, `StrataDark.dc.html` | Chats layout, light; search results, dark |
| `Main.dc.html`, `PickerDark.dc.html` | Source picker open, light and dark |
| `Sources.dc.html`, `Settings.dc.html` | Sources page (light), Settings (dark) |
| `Mail.dc.html`, `MailDark.dc.html` | Mail layout: inbox (light), thread (dark) |
| `PhoneChats.dc.html`, `PhoneChat.dc.html` | Phone layout |

The same mockup was published as a private artifact on the account that made
it, at the address the spec gives; another account cannot open it, so use the
files here.

## How the work is done

- Superpowers workflow: spec, then a plan per milestone, then **native**
  execution (`superpowers:executing-plans`): feature branch, test first for
  every task, one fresh whole-branch review at the end, fix important findings
  test-first, merge to `master` locally, push.
- Push to GitHub without asking (ordinary pushes; never force-push).
- Each executed plan gets a "Post-review changes" section recording what the
  final review changed.
- Stack decisions are fixed: Go, no cgo, single binary, SQLite with FTS5,
  server-rendered templates, Stimulus, no inline styles or scripts.

## Decisions taken while the owner was away (to confirm)

The two decisions this file used to list are settled in the Telegram plan:
media an edit replaces is kept as replaced (attachments carry the source's
file identity), and Telegram chat source IDs name the type (`user:`,
`chat:`, `channel:`) so a deletion without a chat only matches private chats
and basic groups. Chat IDs are stored, so changing their format later needs
a migration: confirm before adding a real account. The plan's "Decisions for
the owner to confirm" lists the other six.

Still to confirm by the manual test in milestone 5: a first real login and
backfill; downloading self-destructing media without calling
`messages.readMessageContents` leaves it unopened for the sender. The
connector never calls that method (a test enforces it).

Email: the spec's ten decisions await confirmation, four of them changed
from the first draft because go-imap v2 beta.8 cannot fetch Gmail's
`X-GM-*` items (Gmail is archived through All Mail, Inbox and Sent by
Message-ID; threads follow `References`). Still to confirm by a manual test
with a Gmail app password: login, a message with several labels stored once,
archiving in Gmail not counted as deletion, Trash counted as deletion,
nothing marked as read, HTML-only newsletters reading cleanly, and whether
drafts in All Mail keep their first text.

## Known gaps, deferred on purpose

- From the Telegram review (listed in its plan's "Post-review changes"): an
  update arriving before the signed-in user is known can title Saved
  Messages with the owner's name until the chat list is read (a data race on
  `selfID`); picture changes are noticed on the next run; requesting
  self-destructing media with the setting off says "Telegram no longer has
  this message"; an unreadable "Since" date means all history.

- From the email review (listed in its plan's "Post-review changes"): the
  same Message-ID twice in one folder tracks only the last UID; drafts in
  All Mail may keep the first draft's text; deletions re-open a folder
  without re-checking UIDVALIDITY; a subject in an unknown charset shows
  raw; the conversation list groups every message of the accounts (slow past
  about 100,000 messages); the mail migration's foreign-key check covers the
  whole database.

- A common single-word search ranked by relevance takes about 1 s at one
  million messages.
- `MediaPath` panics on a hash shorter than expected.
- Shutdown has no timeout for connectors; there is no single-instance guard;
  two processes opening a new data folder at once can race on migrations.
- `« »` are not treated as phrase quotes; snippets can cut inside a grapheme.
- Nothing sweeps `media/tmp` after a crash.
- `arc5 serve --help` exits with an error code.
- From the 3a review (listed in the plan's "Post-review changes"): the global
  login throttle lets anyone keep the owner out with a wrong password every
  few minutes; `localPath` lets a tab through; `verifyPassword` trusts the
  cost numbers of a stored hash; two simultaneous first-run setups can both
  succeed; a media file missing from disk gives 500; "Edited at" has no date;
  lazy images can push the newest message out of view; "Fetch now" never
  completes in the demo; the year rail costs about 100 ms per page on a
  200,000-message chat.
- No `ReadTimeout`/`IdleTimeout` on the HTTP server.
- Removing `ARC5_PASSWORD` makes an earlier password set in the browser active
  again (every session is cleared).

- From the 3b review (listed in its plan's "Post-review changes"):
  - Unfinished logins are swept only when a later request looks at them, and
    none are closed at shutdown.
  - The login page can be opened for a source that is running.
  - "Add a source" creates the account switched on, then switches it off.
  - A name made only of control characters gives a 500 and an orphan account.
  - A mistaken source can be removed but never deleted.
  - The languages card rebuilds the index even when unchanged.
  - A failed save of the language setting leaves the index reported as
    current.
  - Paging a conversation opened from filtered results drops the filters.
  - The Sources page polls itself twice every 5 s, and each render counts the
    messages of every chat.
  - `Runner.SignOut` holds the restart lock across the network call.
  - Refused login steps lose their non-secret input.
  - A cross-site link can open `/sources/{id}/login` and start a connector's
    first login step. Check this when Telegram's first step sends a code.
