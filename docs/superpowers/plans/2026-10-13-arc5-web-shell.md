# arc5 Web Shell and Chats (Milestone 3a) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** A password-protected web interface in the approved Strata look, light and dark, where the owner picks a source and reads its conversations, with a demo archive to try it on.

**Architecture:** `internal/web` renders Go templates with CSS classes only, behind a strict content-security policy, and serves fonts, styles and Stimulus from files embedded in the binary. The archive gains the read queries the interface needs, an `outgoing` flag on messages, and web sessions. Connectors are registered with a display type, which decides the layout a source gets.

**Tech Stack:** Go 1.26 `html/template` and `net/http`, `golang.org/x/crypto/argon2`, Stimulus 3.2.2 (vendored), Geologica variable font (vendored, four subsets).

**Spec:** `docs/superpowers/specs/2026-10-12-message-archive-design.md` (sections 3, 4, 8, 12). Approved mockup: https://claude.ai/artifact/ErJ5ojBuFs8pTM2CLAUsyj. This is plan 3a of milestone 3; search in the interface, the full Sources page with setup flows, and Settings are plan 3b.

## Global Constraints

- No inline styles and no inline scripts in any template. Every HTML response carries exactly this policy: `default-src 'none'; script-src 'self'; style-src 'self'; img-src 'self'; media-src 'self'; font-src 'self'; connect-src 'self'; form-action 'self'; base-uri 'none'; frame-ancestors 'none'`.
- Nothing is fetched from another site at run time. Fonts and Stimulus are files in `internal/web/static`.
- All text from the archive is rendered through `html/template` escaping. No `template.HTML` is built from archive content.
- Passwords are hashed with argon2id (19 MiB, 2 passes, 1 thread). Sessions last 30 days; only the SHA-256 of the session token is stored.
- Every state-changing request needs a session and its CSRF token, except `POST /setup` and `POST /login`.
- `internal/archive` stays the only package that writes to the database.
- Colours are the two palettes in spec section 8; the typeface is Geologica; state marks are a green dot (live), a padlock (needs login), circling arrows (catching up) and a warning triangle (not connected).
- Times are shown in `Deps.Location`, which defaults to the server's local time zone.
- A conversation page shows 60 messages; a chat list shows 100 chats and grows by 100.
- Every task ends with `go vet ./... && go test -race -count=1 -timeout 180s ./...` passing.

## Review Focus

1. **Hostile content in a page.** Message text, chat titles, sender names and file names holding HTML or script must appear as text. Pinned in Task 7.
2. **Unauthenticated and forged requests.** Every page except login, setup, health and static files redirects to login without a session; every `POST` behind login is refused without the session's CSRF token. Pinned in Tasks 5 and 8.
3. **Setup cannot be replayed.** Once a password exists, or `ARC5_PASSWORD` is set, `/setup` must not set a password. Pinned in Task 5.
4. **Paging edges.** An anchor naming a message from another chat or one that does not exist, an empty chat, and a one-message chat must give a sensible page, not an error. Pinned in Tasks 3 and 7.
5. **Dangerous attachments.** An attachment whose type is HTML or SVG must be sent as a download under a sandboxing policy, never rendered. Pinned in Task 8.

## File Structure

```
internal/connector/registry.go            modify: Info, Display
internal/connector/*_test.go              modify: new Register signature
internal/connector/demo/demo.go           create: demo connector and sample archive
internal/model/model.go                   modify: Message.Outgoing
internal/archive/migrations/0003_web.sql  create: outgoing column, web_sessions
internal/archive/messages.go              modify: store and read outgoing
internal/archive/sessions.go              create
internal/archive/read.go                  create: chat list, conversation window, quarters, attachment file
internal/web/server.go                    replace: Server, Deps, routes, render, static
internal/web/auth.go                      create: hashing, sessions, throttle
internal/web/auth_handlers.go             create: setup, login, logout
internal/web/page.go                      create: view types shared by templates
internal/web/sources.go                   create: tiles, picker, banner, sources page
internal/web/chats.go                     create: chat list and conversation handlers
internal/web/media.go                     create
internal/web/templates/*.html             create
internal/web/static/app.css               create
internal/web/static/js/*.js               create (stimulus.js vendored)
internal/web/static/fonts/*.woff2         create (vendored)
internal/config/config.go                 modify: Password, ParseDemo
cmd/arc5/main.go                          modify: web.New, demo command
README.md                                 modify
```

---

### Task 1: Connector info and display type

**Files:**
- Modify: `internal/connector/registry.go`, `internal/connector/registry_test.go`, `internal/connector/runner_test.go`, `internal/connector/contract_test.go`

**Interfaces:**
- Produces:
  - `type Display string` with `DisplayChats`, `DisplayMail`
  - `type Info struct { Name, Title string; Display Display }`
  - `(*Registry).Register(info Info, f Factory)` — replaces `Register(name string, f Factory)`; panics on an empty name or title, an unknown display, or a duplicate name
  - `(*Registry).Info(name string) (Info, bool)`
  - `(*Registry).New`, `(*Registry).Names`, `NewRegistry`, `Default` unchanged

- [ ] **Step 1: Write the failing test**

Replace `internal/connector/registry_test.go` with:

```go
package connector

import (
	"context"
	"slices"
	"testing"
)

// stubConnector is a Connector whose Run is supplied by the test.
type stubConnector struct {
	run func(ctx context.Context, sink Sink, state State, settings map[string]string) error
}

func (s stubConnector) Run(ctx context.Context, sink Sink, state State, settings map[string]string) error {
	return s.run(ctx, sink, state, settings)
}
func (stubConnector) Setup(State) SetupFlow { return nil }
func (stubConnector) Settings() []Field     { return nil }

func stubInfo(name string) Info {
	return Info{Name: name, Title: "Stub", Display: DisplayChats}
}

func TestRegistry(t *testing.T) {
	reg := NewRegistry()
	reg.Register(Info{Name: "zeta", Title: "Zeta Mail", Display: DisplayMail}, func() Connector { return stubConnector{} })
	reg.Register(stubInfo("alpha"), func() Connector { return stubConnector{} })

	if _, ok := reg.New("alpha"); !ok {
		t.Error(`New("alpha") not found`)
	}
	if _, ok := reg.New("missing"); ok {
		t.Error(`New("missing") found`)
	}
	if got, want := reg.Names(), []string{"alpha", "zeta"}; !slices.Equal(got, want) {
		t.Errorf("Names() = %v, want %v", got, want)
	}
	info, ok := reg.Info("zeta")
	if !ok || info.Title != "Zeta Mail" || info.Display != DisplayMail {
		t.Errorf(`Info("zeta") = %+v, %v`, info, ok)
	}
	if _, ok := reg.Info("missing"); ok {
		t.Error(`Info("missing") found`)
	}
}

func TestRegisterRejectsBadInfo(t *testing.T) {
	for name, info := range map[string]Info{
		"no name":    {Title: "X", Display: DisplayChats},
		"no title":   {Name: "x", Display: DisplayChats},
		"no display": {Name: "x", Title: "X"},
		"duplicate":  stubInfo("taken"),
	} {
		t.Run(name, func(t *testing.T) {
			reg := NewRegistry()
			reg.Register(stubInfo("taken"), func() Connector { return stubConnector{} })
			defer func() {
				if recover() == nil {
					t.Error("Register did not panic")
				}
			}()
			reg.Register(info, func() Connector { return stubConnector{} })
		})
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `go test ./internal/connector/`
Expected: FAIL to build with `undefined: Info`, `undefined: DisplayChats`.

- [ ] **Step 3: Write the implementation**

Replace `internal/connector/registry.go` with:

```go
package connector

import (
	"sort"
	"sync"
)

// Display says how a connector's sources are shown.
type Display string

const (
	DisplayChats Display = "chats" // messengers: conversations beside their messages
	DisplayMail  Display = "mail"  // mailboxes: folders and threads
)

// Info describes a kind of connector.
type Info struct {
	Name    string // stored on each account, such as "telegram"
	Title   string // shown to the owner, such as "Telegram"
	Display Display
}

// Factory builds a fresh connector for one run.
type Factory func() Connector

type registered struct {
	info    Info
	factory Factory
}

// Registry holds the connectors built into this binary.
type Registry struct {
	mu      sync.RWMutex
	entries map[string]registered
}

// Default is the registry connector packages add themselves to.
var Default = NewRegistry()

func NewRegistry() *Registry {
	return &Registry{entries: map[string]registered{}}
}

// Register adds a connector. It panics on incomplete info or a duplicate
// name, since both are programming errors caught at startup.
func (r *Registry) Register(info Info, f Factory) {
	r.mu.Lock()
	defer r.mu.Unlock()
	switch {
	case info.Name == "" || info.Title == "":
		panic("connector: Info needs a name and a title")
	case info.Display != DisplayChats && info.Display != DisplayMail:
		panic("connector: " + info.Name + " has no display type")
	}
	if _, dup := r.entries[info.Name]; dup {
		panic("connector: " + info.Name + " registered twice")
	}
	r.entries[info.Name] = registered{info: info, factory: f}
}

func (r *Registry) New(name string) (Connector, bool) {
	r.mu.RLock()
	e, ok := r.entries[name]
	r.mu.RUnlock()
	if !ok {
		return nil, false
	}
	return e.factory(), true
}

func (r *Registry) Info(name string) (Info, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	e, ok := r.entries[name]
	return e.info, ok
}

func (r *Registry) Names() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	names := make([]string, 0, len(r.entries))
	for name := range r.entries {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
```

- [ ] **Step 4: Update the other callers**

In `internal/connector/runner_test.go`, replace both occurrences of `reg.Register("stub", ` with `reg.Register(stubInfo("stub"), `.

In `internal/connector/contract_test.go`, replace

```go
	reg.Register("fake", func() connector.Connector { return fake })
```

with

```go
	reg.Register(connector.Info{Name: "fake", Title: "Fake", Display: connector.DisplayChats}, func() connector.Connector { return fake })
```

- [ ] **Step 5: Run the tests to verify they pass**

Run: `go vet ./... && go test -race -count=1 -timeout 180s ./...`
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/connector
git commit -m "Register connectors with a title and a display type"
```

---

### Task 2: Outgoing messages and web sessions in the archive

**Files:**
- Create: `internal/archive/migrations/0003_web.sql`, `internal/archive/sessions.go`
- Modify: `internal/model/model.go`, `internal/archive/messages.go`, `internal/archive/db_test.go`
- Test: `internal/archive/sessions_test.go`, `internal/archive/messages_test.go`

**Interfaces:**
- Consumes: `Store` internals, `ErrNotFound`, test helpers `openTemp`, `seedChat`, `textMsg`, `mustUpsert`, `mustMessage`.
- Produces:
  - `model.Message.Outgoing bool`
  - `archive.StoredMessage.Outgoing bool`
  - `type Session struct { CSRF string; ExpiresAt time.Time }`
  - `(*Store).CreateSession(tokenHash, csrf string, expiresAt time.Time) error`
  - `(*Store).Session(tokenHash string) (Session, error)` — `ErrNotFound` when missing or expired
  - `(*Store).DeleteSession(tokenHash string) error`, `(*Store).DeleteAllSessions() error`

- [ ] **Step 1: Write the failing tests**

In `internal/archive/db_test.go`, add `"web_sessions"` to the end of the `want` list in `TestOpenCreatesSchema`, and change `migrations != 2` to `migrations != 3` and its message to `want 3 and 1` in `TestReopenKeepsDataAndAppliesMigrationsOnce`.

Append to `internal/archive/messages_test.go`:

```go
func TestOutgoingIsStored(t *testing.T) {
	s := openTemp(t)
	acc := seedChat(t, s)
	mine := textMsg("1", "from me")
	mine.Outgoing = true
	mustUpsert(t, s, acc, mine)
	mustUpsert(t, s, acc, textMsg("2", "from them"))

	if !mustMessage(t, s, acc, "1").Outgoing {
		t.Error("message 1 is not marked outgoing")
	}
	if mustMessage(t, s, acc, "2").Outgoing {
		t.Error("message 2 is marked outgoing")
	}
}
```

`internal/archive/sessions_test.go`:

```go
package archive

import (
	"errors"
	"testing"
	"time"
)

func TestSessionRoundTrip(t *testing.T) {
	s := openTemp(t)
	s.now = func() time.Time { return t0 }
	if err := s.CreateSession("hash-a", "csrf-a", t0.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	got, err := s.Session("hash-a")
	if err != nil {
		t.Fatal(err)
	}
	if got.CSRF != "csrf-a" || !got.ExpiresAt.Equal(t0.Add(time.Hour)) {
		t.Fatalf("session = %+v", got)
	}
	if _, err := s.Session("hash-b"); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown session error = %v, want ErrNotFound", err)
	}
}

func TestExpiredSessionIsNotFound(t *testing.T) {
	s := openTemp(t)
	s.now = func() time.Time { return t0 }
	if err := s.CreateSession("hash-a", "csrf-a", t0.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	s.now = func() time.Time { return t0.Add(time.Hour) } // exactly at expiry
	if _, err := s.Session("hash-a"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expired session error = %v, want ErrNotFound", err)
	}
}

func TestDeleteSessions(t *testing.T) {
	s := openTemp(t)
	for _, h := range []string{"a", "b", "c"} {
		if err := s.CreateSession(h, "csrf", s.now().Add(time.Hour)); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.DeleteSession("a"); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteSession("never-existed"); err != nil {
		t.Fatalf("deleting an unknown session: %v", err)
	}
	if _, err := s.Session("a"); !errors.Is(err, ErrNotFound) {
		t.Error("session a survived DeleteSession")
	}
	if _, err := s.Session("b"); err != nil {
		t.Errorf("session b was deleted too: %v", err)
	}
	if err := s.DeleteAllSessions(); err != nil {
		t.Fatal(err)
	}
	for _, h := range []string{"b", "c"} {
		if _, err := s.Session(h); !errors.Is(err, ErrNotFound) {
			t.Errorf("session %s survived DeleteAllSessions", h)
		}
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/archive/`
Expected: FAIL to build with `mine.Outgoing undefined`, `s.CreateSession undefined`.

- [ ] **Step 3: Write the migration**

`internal/archive/migrations/0003_web.sql`:

```sql
-- 1 for messages the account owner sent. Only the connector knows this.
ALTER TABLE messages ADD COLUMN outgoing INTEGER NOT NULL DEFAULT 0;

-- Browser sessions. token_hash is the SHA-256 of the cookie's token, so a
-- copy of the database does not hold a usable login.
CREATE TABLE web_sessions (
  token_hash TEXT    PRIMARY KEY,
  csrf       TEXT    NOT NULL,
  created_at INTEGER NOT NULL,
  expires_at INTEGER NOT NULL
);
```

- [ ] **Step 4: Carry `Outgoing` through the model and the store**

In `internal/model/model.go`, add a field to `Message` after `Kind`:

```go
	Outgoing        bool        // sent by the account owner
```

In `internal/archive/messages.go`:

Replace the message insert

```go
			INSERT INTO messages (chat_id, source_id, sender_id, kind, sent_at, text,
			                      reply_to_source_id, forwarded_from, edited_at, edit_dated, captured_at, raw)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			chat, m.SourceID, sender, string(m.Kind), m.SentAt.Unix(), text,
			m.ReplyToSourceID, clean(m.ForwardedFrom), nullUnix(incomingEdit), b2i(incomingEdit > 0), now, raw)
```

with

```go
			INSERT INTO messages (chat_id, source_id, sender_id, kind, outgoing, sent_at, text,
			                      reply_to_source_id, forwarded_from, edited_at, edit_dated, captured_at, raw)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			chat, m.SourceID, sender, string(m.Kind), b2i(m.Outgoing), m.SentAt.Unix(), text,
			m.ReplyToSourceID, clean(m.ForwardedFrom), nullUnix(incomingEdit), b2i(incomingEdit > 0), now, raw)
```

Add `Outgoing bool` to `StoredMessage` after `Kind`. In `Message`, add `outgoing int` to the `var` block, change the select list to end with `m.edited_at, m.deleted_at, m.outgoing`, add `&outgoing` as the last `Scan` argument, and set `m.Outgoing = outgoing != 0` beside the other assignments.

- [ ] **Step 5: Write the session store**

`internal/archive/sessions.go`:

```go
package archive

import (
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// Session is one signed-in browser.
type Session struct {
	CSRF      string
	ExpiresAt time.Time
}

func (s *Store) CreateSession(tokenHash, csrf string, expiresAt time.Time) error {
	_, err := s.w.Exec(`INSERT INTO web_sessions (token_hash, csrf, created_at, expires_at) VALUES (?, ?, ?, ?)`,
		tokenHash, csrf, s.now().Unix(), expiresAt.Unix())
	return err
}

// Session returns the session for a token hash, or ErrNotFound if there is
// none or it has expired.
func (s *Store) Session(tokenHash string) (Session, error) {
	var (
		sess    Session
		expires int64
	)
	err := s.r.QueryRow(`SELECT csrf, expires_at FROM web_sessions WHERE token_hash = ? AND expires_at > ?`,
		tokenHash, s.now().Unix()).Scan(&sess.CSRF, &expires)
	if errors.Is(err, sql.ErrNoRows) {
		return Session{}, fmt.Errorf("session: %w", ErrNotFound)
	}
	if err != nil {
		return Session{}, err
	}
	sess.ExpiresAt = time.Unix(expires, 0).UTC()
	return sess, nil
}

func (s *Store) DeleteSession(tokenHash string) error {
	_, err := s.w.Exec(`DELETE FROM web_sessions WHERE token_hash = ?`, tokenHash)
	return err
}

// DeleteAllSessions signs every browser out. It also drops expired rows.
func (s *Store) DeleteAllSessions() error {
	_, err := s.w.Exec(`DELETE FROM web_sessions`)
	return err
}
```

- [ ] **Step 6: Run the tests to verify they pass**

Run: `go vet ./... && go test -race -count=1 -timeout 180s ./...`
Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add internal/model internal/archive
git commit -m "Store who sent a message and keep web sessions"
```

---

### Task 3: Reading conversations

**Files:**
- Create: `internal/archive/read.go`
- Test: `internal/archive/read_test.go`

**Interfaces:**
- Consumes: `Store` internals, `StoredAttachment`, `MediaPath`, `ErrNotFound`, `fromUnix`, package `model`, test helpers.
- Produces:
  - `type ChatSummary struct { ID, AccountID int64; Kind model.ChatKind; Title string; LastAt time.Time; LastText, LastSender string; LastOutgoing bool; LastKind model.MessageKind; LastFiles int }`
  - `(*Store).ChatList(accountIDs []int64, limit, offset int) ([]ChatSummary, error)` — chats that hold messages, most recently active first
  - `type ChatInfo struct { ID, AccountID int64; SourceID string; Kind model.ChatKind; Title string; Archived bool; Messages int; FirstAt time.Time }`
  - `(*Store).Chat(id int64) (ChatInfo, error)`
  - `type MessageView struct { ID int64; SourceID string; Kind model.MessageKind; Outgoing bool; SenderName string; SentAt time.Time; Text string; EditedAt, DeletedAt time.Time; ForwardedFrom, ReplyToSourceID string; Versions int; Attachments []StoredAttachment }`
  - `type Anchor struct { Before, After, Around int64; From time.Time }` — at most one field set; none means the newest messages
  - `type Window struct { Messages []MessageView; HasOlder, HasNewer bool }` — messages oldest first
  - `(*Store).ChatMessages(chatID int64, a Anchor, limit int) (Window, error)`
  - `type Quarter struct { Year, Quarter, Messages int }`, `(*Store).ChatQuarters(chatID int64) ([]Quarter, error)` — newest first, UTC
  - `type AttachmentFile struct { StoredAttachment; MessageID int64; Path string }`, `(*Store).AttachmentFile(id int64) (AttachmentFile, error)` — `Path` is set only when the status is stored

- [ ] **Step 1: Write the failing tests**

`internal/archive/read_test.go`:

```go
package archive

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/fenelon/arc5/internal/model"
)

// conversation stores n messages in chat c1, one minute apart starting at t0,
// with source IDs "1".."n", and returns their database IDs in that order.
func conversation(t *testing.T, s *Store, acc int64, n int) []int64 {
	t.Helper()
	ids := make([]int64, n)
	for i := range n {
		m := textMsg(fmt.Sprint(i+1), fmt.Sprintf("message %d", i+1))
		m.SentAt = t0.Add(time.Duration(i) * time.Minute)
		mustUpsert(t, s, acc, m)
		ids[i] = mustMessage(t, s, acc, m.SourceID).ID
	}
	return ids
}

func chatDBID(t *testing.T, s *Store, acc int64, sourceID string) int64 {
	t.Helper()
	var id int64
	if err := s.r.QueryRow(`SELECT id FROM chats WHERE account_id = ? AND source_id = ?`, acc, sourceID).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

func texts(w Window) string {
	parts := make([]string, len(w.Messages))
	for i, m := range w.Messages {
		parts[i] = strings.TrimPrefix(m.Text, "message ")
	}
	return strings.Join(parts, " ")
}

func TestChatListIsNewestFirstWithLastMessage(t *testing.T) {
	s := openTemp(t)
	acc := seedChat(t, s)
	other := newAccount(t, s)
	for _, c := range []model.Chat{
		{SourceID: "g1", Kind: model.ChatGroup, Title: "Group"},
		{SourceID: "empty", Kind: model.ChatPrivate, Title: "Nobody wrote"},
	} {
		if err := s.UpsertChat(acc, c); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.UpsertChat(other, model.Chat{SourceID: "x", Kind: model.ChatPrivate, Title: "Elsewhere"}); err != nil {
		t.Fatal(err)
	}

	old := textMsg("1", "an old one")
	mustUpsert(t, s, acc, old)
	last := textMsg("2", "the latest in c1")
	last.SentAt, last.Outgoing = t0.Add(time.Hour), true
	last.Attachments = []model.Attachment{{Kind: model.AttachPhoto, FileName: "a.jpg", Size: 1}, {Kind: model.AttachPhoto, FileName: "b.jpg", Size: 1}}
	mustUpsert(t, s, acc, last)
	inGroup := textMsg("3", "newest overall")
	inGroup.ChatSourceID, inGroup.SentAt = "g1", t0.Add(2*time.Hour)
	mustUpsert(t, s, acc, inGroup)
	elsewhere := model.Message{ChatSourceID: "x", SourceID: "9", SentAt: t0.Add(3 * time.Hour), Text: "another account"}
	mustUpsert(t, s, other, elsewhere)

	got, err := s.ChatList([]int64{acc}, 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Title != "Group" || got[1].Title != "Alice" {
		t.Fatalf("chat list = %+v; want Group then Alice, without the empty chat or the other account", got)
	}
	c1 := got[1]
	if c1.LastText != "the latest in c1" || !c1.LastOutgoing || c1.LastFiles != 2 || c1.LastSender != "Alice" ||
		!c1.LastAt.Equal(t0.Add(time.Hour)) || c1.Kind != model.ChatPrivate || c1.AccountID != acc {
		t.Errorf("summary = %+v", c1)
	}

	both, err := s.ChatList([]int64{acc, other}, 10, 0)
	if err != nil || len(both) != 3 || both[0].Title != "Elsewhere" {
		t.Errorf("two accounts = %+v, %v", both, err)
	}
	if page, _ := s.ChatList([]int64{acc, other}, 1, 1); len(page) != 1 || page[0].Title != "Group" {
		t.Errorf("limit 1 offset 1 = %+v", page)
	}
	if none, err := s.ChatList(nil, 10, 0); err != nil || len(none) != 0 {
		t.Errorf("no accounts = %+v, %v", none, err)
	}
}

func TestChatInfo(t *testing.T) {
	s := openTemp(t)
	acc := seedChat(t, s)
	conversation(t, s, acc, 3)
	got, err := s.Chat(chatDBID(t, s, acc, "c1"))
	if err != nil {
		t.Fatal(err)
	}
	if got.Title != "Alice" || got.SourceID != "c1" || got.AccountID != acc || got.Kind != model.ChatPrivate ||
		!got.Archived || got.Messages != 3 || !got.FirstAt.Equal(t0) {
		t.Fatalf("chat = %+v", got)
	}
	if _, err := s.Chat(9999); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown chat error = %v, want ErrNotFound", err)
	}
}

func TestChatMessagesWindows(t *testing.T) {
	s := openTemp(t)
	acc := seedChat(t, s)
	ids := conversation(t, s, acc, 10)
	chat := chatDBID(t, s, acc, "c1")

	for _, c := range []struct {
		name             string
		anchor           Anchor
		limit            int
		want             string
		older, newerSide bool
	}{
		{"newest", Anchor{}, 4, "7 8 9 10", true, false},
		{"everything", Anchor{}, 50, "1 2 3 4 5 6 7 8 9 10", false, false},
		{"before 7", Anchor{Before: ids[6]}, 4, "3 4 5 6", true, true},
		{"before 3", Anchor{Before: ids[2]}, 4, "1 2", false, true},
		{"after 7", Anchor{After: ids[6]}, 2, "8 9", true, true},
		{"after 9", Anchor{After: ids[8]}, 4, "10", true, false},
		{"around 5", Anchor{Around: ids[4]}, 4, "3 4 5 6", true, true},
		{"around 1", Anchor{Around: ids[0]}, 4, "1 2", false, true},
		{"around 10", Anchor{Around: ids[9]}, 4, "8 9 10", true, false},
		{"from the fourth minute", Anchor{From: t0.Add(3 * time.Minute)}, 3, "4 5 6", true, true},
		{"from before the start", Anchor{From: t0.Add(-time.Hour)}, 3, "1 2 3", false, true},
		{"from after the end falls back to the newest", Anchor{From: t0.Add(24 * time.Hour)}, 3, "8 9 10", true, false},
	} {
		w, err := s.ChatMessages(chat, c.anchor, c.limit)
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if got := texts(w); got != c.want || w.HasOlder != c.older || w.HasNewer != c.newerSide {
			t.Errorf("%s: got %q older=%v newer=%v; want %q older=%v newer=%v",
				c.name, got, w.HasOlder, w.HasNewer, c.want, c.older, c.newerSide)
		}
	}
}

// Review Focus 4: paging edges.
func TestChatMessagesEdges(t *testing.T) {
	s := openTemp(t)
	acc := seedChat(t, s)
	if err := s.UpsertChat(acc, model.Chat{SourceID: "g1", Kind: model.ChatGroup, Title: "Group"}); err != nil {
		t.Fatal(err)
	}
	chat, group := chatDBID(t, s, acc, "c1"), chatDBID(t, s, acc, "g1")

	if w, err := s.ChatMessages(chat, Anchor{}, 10); err != nil || len(w.Messages) != 0 || w.HasOlder || w.HasNewer {
		t.Errorf("empty chat = %+v, %v", w, err)
	}
	ids := conversation(t, s, acc, 1)
	if w, err := s.ChatMessages(chat, Anchor{Around: ids[0]}, 10); err != nil || texts(w) != "1" || w.HasOlder || w.HasNewer {
		t.Errorf("one message = %+v, %v", w, err)
	}
	// A message of one chat is no anchor in another, and neither is a missing one.
	for name, a := range map[string]Anchor{
		"another chat's message": {Around: ids[0]},
		"a missing message":      {Before: 987654},
		"a missing later one":    {After: 987654},
	} {
		w, err := s.ChatMessages(group, a, 10)
		if err != nil || len(w.Messages) != 0 {
			t.Errorf("%s: %+v, %v; want an empty window", name, w, err)
		}
	}
	if w, err := s.ChatMessages(chat, Anchor{}, 0); err != nil || len(w.Messages) != 1 {
		t.Errorf("limit 0 = %+v, %v; want it treated as 1", w, err)
	}
}

func TestChatMessagesCarryWhatAPageShows(t *testing.T) {
	s := openTemp(t)
	acc := seedChat(t, s)
	m := textMsg("1", "first wording")
	m.ForwardedFrom, m.ReplyToSourceID, m.Outgoing = "Bob", "0", true
	m.Attachments = []model.Attachment{{Kind: model.AttachDocument, FileName: "a.pdf", MimeType: "application/pdf", Size: 5}}
	mustUpsert(t, s, acc, m)
	edit := textMsg("1", "second wording")
	edit.EditedAt = t0.Add(time.Minute)
	mustUpsert(t, s, acc, edit)
	if err := s.MarkDeleted(acc, "c1", []string{"1"}); err != nil {
		t.Fatal(err)
	}
	service := model.Message{ChatSourceID: "c1", SourceID: "2", Kind: model.KindService, SentAt: t0.Add(time.Hour), Text: "Alice pinned a message"}
	mustUpsert(t, s, acc, service)

	w, err := s.ChatMessages(chatDBID(t, s, acc, "c1"), Anchor{}, 10)
	if err != nil || len(w.Messages) != 2 {
		t.Fatalf("window = %+v, %v", w, err)
	}
	got := w.Messages[0]
	if got.Text != "second wording" || got.SenderName != "Alice" || !got.Outgoing || got.Versions != 1 ||
		got.ForwardedFrom != "Bob" || got.ReplyToSourceID != "0" || got.SourceID != "1" ||
		!got.EditedAt.Equal(t0.Add(time.Minute)) || got.DeletedAt.IsZero() || !got.SentAt.Equal(t0) {
		t.Errorf("message = %+v", got)
	}
	if len(got.Attachments) != 1 || got.Attachments[0].FileName != "a.pdf" || got.Attachments[0].Status != model.StatusPending {
		t.Errorf("attachments = %+v", got.Attachments)
	}
	if svc := w.Messages[1]; svc.Kind != model.KindService || svc.SenderName != "" || len(svc.Attachments) != 0 {
		t.Errorf("service message = %+v", svc)
	}
}

func TestChatQuarters(t *testing.T) {
	s := openTemp(t)
	acc := seedChat(t, s)
	at := func(id string, year int, month time.Month) {
		m := textMsg(id, "x")
		m.SentAt = time.Date(year, month, 15, 12, 0, 0, 0, time.UTC)
		mustUpsert(t, s, acc, m)
	}
	at("1", 2024, time.January)
	at("2", 2024, time.March)
	at("3", 2024, time.April)
	at("4", 2025, time.December)
	at("5", 2025, time.October)
	at("6", 2025, time.November)

	got, err := s.ChatQuarters(chatDBID(t, s, acc, "c1"))
	if err != nil {
		t.Fatal(err)
	}
	want := []Quarter{{2025, 4, 3}, {2024, 2, 1}, {2024, 1, 2}}
	if !slices.Equal(got, want) {
		t.Fatalf("quarters = %v, want %v", got, want)
	}
}

func TestAttachmentFile(t *testing.T) {
	s := openTemp(t)
	acc := seedChat(t, s)
	ref := withAttachment(t, s, acc, "1", 5)
	att := attachment(t, s, acc, "1")

	pending, err := s.AttachmentFile(att.ID)
	if err != nil {
		t.Fatal(err)
	}
	if pending.Path != "" || pending.Status != model.StatusPending || pending.MessageID == 0 {
		t.Errorf("pending file = %+v", pending)
	}
	if err := s.PutMedia(acc, ref, strings.NewReader("hello")); err != nil {
		t.Fatal(err)
	}
	stored, err := s.AttachmentFile(att.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Path != s.MediaPath(stored.SHA256) || stored.Status != model.StatusStored || stored.FileName != "a.bin" {
		t.Errorf("stored file = %+v", stored)
	}
	if _, err := s.AttachmentFile(9999); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown attachment error = %v, want ErrNotFound", err)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/archive/`
Expected: FAIL to build with `undefined: Window`, `s.ChatList undefined`, `undefined: Quarter`.

- [ ] **Step 3: Write the implementation**

`internal/archive/read.go`:

```go
package archive

import (
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/fenelon/arc5/internal/model"
)

func placeholders(n int) string {
	return strings.TrimSuffix(strings.Repeat("?,", n), ",")
}

// ChatSummary is one row of a conversation list: a chat and its last message.
type ChatSummary struct {
	ID           int64
	AccountID    int64
	Kind         model.ChatKind
	Title        string
	LastAt       time.Time
	LastText     string
	LastSender   string
	LastOutgoing bool
	LastKind     model.MessageKind
	LastFiles    int // attachments on the last message
}

// ChatList returns the chats of the given accounts that hold messages, most
// recently active first.
func (s *Store) ChatList(accountIDs []int64, limit, offset int) ([]ChatSummary, error) {
	if len(accountIDs) == 0 {
		return nil, nil
	}
	args := make([]any, 0, len(accountIDs)+2)
	for _, id := range accountIDs {
		args = append(args, id)
	}
	args = append(args, limit, offset)
	rows, err := s.r.Query(`
		SELECT c.id, c.account_id, c.kind, c.title, m.sent_at, m.text, m.kind, m.outgoing,
		       COALESCE(sn.display_name, ''),
		       (SELECT COUNT(*) FROM attachments a WHERE a.message_id = m.id)
		FROM chats c
		JOIN messages m ON m.id = (
			SELECT m2.id FROM messages m2 WHERE m2.chat_id = c.id
			ORDER BY m2.sent_at DESC, m2.id DESC LIMIT 1)
		LEFT JOIN senders sn ON sn.id = m.sender_id
		WHERE c.account_id IN (`+placeholders(len(accountIDs))+`)
		ORDER BY m.sent_at DESC, m.id DESC
		LIMIT ? OFFSET ?`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ChatSummary
	for rows.Next() {
		var (
			c                  ChatSummary
			kind, lastKind     string
			sent               int64
			outgoing           int
		)
		if err := rows.Scan(&c.ID, &c.AccountID, &kind, &c.Title, &sent, &c.LastText, &lastKind, &outgoing, &c.LastSender, &c.LastFiles); err != nil {
			return nil, err
		}
		c.Kind, c.LastKind = model.ChatKind(kind), model.MessageKind(lastKind)
		c.LastAt, c.LastOutgoing = time.Unix(sent, 0).UTC(), outgoing != 0
		out = append(out, c)
	}
	return out, rows.Err()
}

// ChatInfo describes one chat.
type ChatInfo struct {
	ID        int64
	AccountID int64
	SourceID  string
	Kind      model.ChatKind
	Title     string
	Archived  bool
	Messages  int
	FirstAt   time.Time // zero when the chat is empty
}

func (s *Store) Chat(id int64) (ChatInfo, error) {
	var (
		c        ChatInfo
		kind     string
		archived int
		first    sql.NullInt64
	)
	err := s.r.QueryRow(`
		SELECT c.id, c.account_id, c.source_id, c.kind, c.title, c.archived,
		       (SELECT COUNT(*) FROM messages m WHERE m.chat_id = c.id),
		       (SELECT MIN(m.sent_at) FROM messages m WHERE m.chat_id = c.id)
		FROM chats c WHERE c.id = ?`, id).
		Scan(&c.ID, &c.AccountID, &c.SourceID, &kind, &c.Title, &archived, &c.Messages, &first)
	if errors.Is(err, sql.ErrNoRows) {
		return ChatInfo{}, fmt.Errorf("chat %d: %w", id, ErrNotFound)
	}
	if err != nil {
		return ChatInfo{}, err
	}
	c.Kind, c.Archived, c.FirstAt = model.ChatKind(kind), archived != 0, fromUnix(first)
	return c, nil
}

// MessageView is a message as a conversation page shows it.
type MessageView struct {
	ID              int64
	SourceID        string
	Kind            model.MessageKind
	Outgoing        bool
	SenderName      string
	SentAt          time.Time
	Text            string
	EditedAt        time.Time
	DeletedAt       time.Time
	ForwardedFrom   string
	ReplyToSourceID string
	Versions        int // earlier texts kept for this message
	Attachments     []StoredAttachment
}

// Anchor says which part of a conversation to read. At most one field is set;
// with none set, the newest messages are read.
type Anchor struct {
	Before int64     // messages just older than this message ID
	After  int64     // messages just newer than this message ID
	Around int64     // this message with its neighbours
	From   time.Time // messages from this moment on
}

// Window is a run of consecutive messages, oldest first.
type Window struct {
	Messages []MessageView
	HasOlder bool
	HasNewer bool
}

const messageViewSelect = `
	SELECT m.id, m.source_id, m.kind, m.outgoing, COALESCE(sn.display_name, ''), m.sent_at, m.text,
	       m.edited_at, m.deleted_at, m.forwarded_from, m.reply_to_source_id,
	       (SELECT COUNT(*) FROM message_versions v WHERE v.message_id = m.id)
	FROM messages m LEFT JOIN senders sn ON sn.id = m.sender_id
	WHERE m.chat_id = ?`

// pivot is the position of an anchor message, if it belongs to the chat being
// read. A message of another chat, or none, compares as NULL and matches nothing.
const pivot = `(SELECT p.sent_at, p.id FROM messages p WHERE p.id = ? AND p.chat_id = m.chat_id)`

const (
	newestFirst = ` ORDER BY m.sent_at DESC, m.id DESC LIMIT ?`
	oldestFirst = ` ORDER BY m.sent_at ASC, m.id ASC LIMIT ?`
)

func (s *Store) views(query string, args ...any) ([]MessageView, error) {
	rows, err := s.r.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []MessageView
	for rows.Next() {
		var (
			v               MessageView
			kind            string
			outgoing        int
			sent            int64
			edited, deleted sql.NullInt64
		)
		if err := rows.Scan(&v.ID, &v.SourceID, &kind, &outgoing, &v.SenderName, &sent, &v.Text,
			&edited, &deleted, &v.ForwardedFrom, &v.ReplyToSourceID, &v.Versions); err != nil {
			return nil, err
		}
		v.Kind, v.Outgoing = model.MessageKind(kind), outgoing != 0
		v.SentAt, v.EditedAt, v.DeletedAt = time.Unix(sent, 0).UTC(), fromUnix(edited), fromUnix(deleted)
		out = append(out, v)
	}
	return out, rows.Err()
}

// trim cuts v to n messages and reports whether there were more.
func trim(v *[]MessageView, n int) bool {
	if len(*v) > n {
		*v = (*v)[:n]
		return true
	}
	return false
}

// ChatMessages reads up to limit consecutive messages of a chat.
func (s *Store) ChatMessages(chatID int64, a Anchor, limit int) (Window, error) {
	limit = max(limit, 1)
	var (
		w   Window
		err error
	)
	switch {
	case a.Before != 0:
		w.Messages, err = s.views(messageViewSelect+` AND (m.sent_at, m.id) < `+pivot+newestFirst, chatID, a.Before, limit+1)
		if err != nil {
			return Window{}, err
		}
		w.HasOlder = trim(&w.Messages, limit)
		slices.Reverse(w.Messages)
		w.HasNewer = len(w.Messages) > 0 // the anchor itself is newer

	case a.After != 0:
		w.Messages, err = s.views(messageViewSelect+` AND (m.sent_at, m.id) > `+pivot+oldestFirst, chatID, a.After, limit+1)
		if err != nil {
			return Window{}, err
		}
		w.HasNewer = trim(&w.Messages, limit)
		w.HasOlder = len(w.Messages) > 0 // the anchor itself is older

	case a.Around != 0:
		older := limit / 2 // messages shown before the anchor
		before, err := s.views(messageViewSelect+` AND (m.sent_at, m.id) <= `+pivot+newestFirst, chatID, a.Around, older+2)
		if err != nil {
			return Window{}, err
		}
		w.HasOlder = trim(&before, older+1)
		slices.Reverse(before)
		newer := max(limit-older-1, 0)
		after, err := s.views(messageViewSelect+` AND (m.sent_at, m.id) > `+pivot+oldestFirst, chatID, a.Around, newer+1)
		if err != nil {
			return Window{}, err
		}
		w.HasNewer = trim(&after, newer)
		w.Messages = append(before, after...)

	case !a.From.IsZero():
		w.Messages, err = s.views(messageViewSelect+` AND m.sent_at >= ?`+oldestFirst, chatID, a.From.Unix(), limit+1)
		if err != nil {
			return Window{}, err
		}
		if len(w.Messages) == 0 {
			return s.ChatMessages(chatID, Anchor{}, limit) // nothing that late: show the end
		}
		w.HasNewer = trim(&w.Messages, limit)
		if err := s.r.QueryRow(`SELECT EXISTS (SELECT 1 FROM messages WHERE chat_id = ? AND sent_at < ?)`,
			chatID, a.From.Unix()).Scan(&w.HasOlder); err != nil {
			return Window{}, err
		}

	default:
		w.Messages, err = s.views(messageViewSelect+newestFirst, chatID, limit+1)
		if err != nil {
			return Window{}, err
		}
		w.HasOlder = trim(&w.Messages, limit)
		slices.Reverse(w.Messages)
	}
	return w, s.attach(w.Messages)
}

// attach fills in the attachments of each message.
func (s *Store) attach(messages []MessageView) error {
	if len(messages) == 0 {
		return nil
	}
	at := make(map[int64]int, len(messages))
	ids := make([]any, len(messages))
	for i, m := range messages {
		at[m.ID], ids[i] = i, m.ID
	}
	rows, err := s.r.Query(`
		SELECT message_id, id, position, kind, file_name, mime_type, size, sha256, status, reason, ephemeral, requested
		FROM attachments WHERE message_id IN (`+placeholders(len(ids))+`) ORDER BY message_id, position`, ids...)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var (
			messageID            int64
			a                    StoredAttachment
			kind, status         string
			ephemeral, requested int
		)
		if err := rows.Scan(&messageID, &a.ID, &a.Position, &kind, &a.FileName, &a.MimeType, &a.Size, &a.SHA256,
			&status, &a.Reason, &ephemeral, &requested); err != nil {
			return err
		}
		a.Kind, a.Status = model.AttachmentKind(kind), model.AttachmentStatus(status)
		a.Ephemeral, a.Requested = ephemeral != 0, requested != 0
		i := at[messageID]
		messages[i].Attachments = append(messages[i].Attachments, a)
	}
	return rows.Err()
}

// Quarter is how many messages a chat holds in one quarter of a year.
type Quarter struct {
	Year     int
	Quarter  int // 1 to 4
	Messages int
}

// ChatQuarters counts a chat's messages by quarter (UTC), newest first.
// Quarters with no messages are not returned.
func (s *Store) ChatQuarters(chatID int64) ([]Quarter, error) {
	rows, err := s.r.Query(`
		SELECT CAST(strftime('%Y', sent_at, 'unixepoch') AS INTEGER) AS y,
		       (CAST(strftime('%m', sent_at, 'unixepoch') AS INTEGER) + 2) / 3 AS q,
		       COUNT(*)
		FROM messages WHERE chat_id = ?
		GROUP BY y, q ORDER BY y DESC, q DESC`, chatID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Quarter
	for rows.Next() {
		var q Quarter
		if err := rows.Scan(&q.Year, &q.Quarter, &q.Messages); err != nil {
			return nil, err
		}
		out = append(out, q)
	}
	return out, rows.Err()
}

// AttachmentFile is an attachment and, once stored, where its file is.
type AttachmentFile struct {
	StoredAttachment
	MessageID int64
	Path      string
}

func (s *Store) AttachmentFile(id int64) (AttachmentFile, error) {
	var (
		f                    AttachmentFile
		kind, status         string
		ephemeral, requested int
	)
	err := s.r.QueryRow(`
		SELECT id, message_id, position, kind, file_name, mime_type, size, sha256, status, reason, ephemeral, requested
		FROM attachments WHERE id = ?`, id).
		Scan(&f.ID, &f.MessageID, &f.Position, &kind, &f.FileName, &f.MimeType, &f.Size, &f.SHA256,
			&status, &f.Reason, &ephemeral, &requested)
	if errors.Is(err, sql.ErrNoRows) {
		return AttachmentFile{}, fmt.Errorf("attachment %d: %w", id, ErrNotFound)
	}
	if err != nil {
		return AttachmentFile{}, err
	}
	f.Kind, f.Status = model.AttachmentKind(kind), model.AttachmentStatus(status)
	f.Ephemeral, f.Requested = ephemeral != 0, requested != 0
	if f.Status == model.StatusStored && len(f.SHA256) == 64 {
		f.Path = s.MediaPath(f.SHA256)
	}
	return f, nil
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `gofmt -l internal; go vet ./... && go test -race -count=1 -timeout 180s ./...`
Expected: `gofmt -l` prints nothing after you run `gofmt -w internal/archive`; tests PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/archive
git commit -m "Add the archive queries the web interface reads"
```

---

### Task 4: Passwords, sessions and throttling

**Files:**
- Create: `internal/web/auth.go`
- Test: `internal/web/auth_test.go`

**Interfaces:**
- Consumes: `archive.Store` (`Setting`, `SetSetting`, `CreateSession`, `Session`, `DeleteSession`, `DeleteAllSessions`), `archive.Session`.
- Produces, unexported in package `web`:
  - `hashPassword(password string) (string, error)`, `verifyPassword(encoded, password string) bool`
  - `type auth struct { store *archive.Store; envPassword string; now func() time.Time; … }`
  - `(*auth).usesEnv() bool`, `(*auth).hasPassword() (bool, error)`, `(*auth).setPassword(password string) error`, `(*auth).check(password string) (bool, error)`, `(*auth).syncEnv() error`
  - `(*auth).wait() time.Duration`, `(*auth).failed()`, `(*auth).succeeded()`
  - `(*auth).startSession(w http.ResponseWriter, r *http.Request) error`, `(*auth).session(r *http.Request) (archive.Session, bool)`, `(*auth).endSession(w http.ResponseWriter, r *http.Request)`
  - errors `errShortPassword`, `errEnvPassword`; constants `sessionCookie = "arc5_session"`, `minPasswordLen = 8`

- [ ] **Step 1: Add the dependency**

```bash
go get golang.org/x/crypto@latest
```

- [ ] **Step 2: Write the failing tests**

`internal/web/auth_test.go`:

```go
package web

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/fenelon/arc5/internal/archive"
)

func newAuth(t *testing.T, env string) (*auth, *archive.Store) {
	t.Helper()
	store, err := archive.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	return &auth{store: store, envPassword: env, now: time.Now}, store
}

func TestPasswordHashing(t *testing.T) {
	h, err := hashPassword("correct horse")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(h, "argon2id$") || strings.Contains(h, "correct horse") {
		t.Fatalf("hash = %q", h)
	}
	again, _ := hashPassword("correct horse")
	if h == again {
		t.Error("two hashes of one password are identical; the salt is not random")
	}
	if !verifyPassword(h, "correct horse") {
		t.Error("the right password was rejected")
	}
	for _, wrong := range []string{"", "correct horsE", "correct horse "} {
		if verifyPassword(h, wrong) {
			t.Errorf("wrong password %q was accepted", wrong)
		}
	}
	for _, broken := range []string{"", "argon2id$", "plain$1$2$3$4$5", "argon2id$x$2$1$AAAA$AAAA", "argon2id$19456$2$1$***$AAAA"} {
		if verifyPassword(broken, "correct horse") {
			t.Errorf("broken hash %q verified", broken)
		}
	}
}

func TestSetAndCheckPassword(t *testing.T) {
	a, store := newAuth(t, "")
	if has, err := a.hasPassword(); err != nil || has {
		t.Fatalf("hasPassword on a new archive = %v, %v", has, err)
	}
	if ok, err := a.check("anything"); err != nil || ok {
		t.Fatalf("check with no password set = %v, %v", ok, err)
	}
	if err := a.setPassword("short"); !errors.Is(err, errShortPassword) {
		t.Fatalf("short password error = %v", err)
	}
	if err := store.CreateSession("old", "csrf", time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := a.setPassword("correct horse"); err != nil {
		t.Fatal(err)
	}
	if has, _ := a.hasPassword(); !has {
		t.Error("hasPassword is false after setPassword")
	}
	if ok, _ := a.check("correct horse"); !ok {
		t.Error("the new password was rejected")
	}
	if ok, _ := a.check("wrong horse"); ok {
		t.Error("a wrong password was accepted")
	}
	if _, err := store.Session("old"); !errors.Is(err, archive.ErrNotFound) {
		t.Error("changing the password left an old session signed in")
	}
}

func TestEnvironmentPassword(t *testing.T) {
	a, store := newAuth(t, "from-env")
	if has, _ := a.hasPassword(); !has || !a.usesEnv() {
		t.Fatal("an environment password is not treated as set")
	}
	if ok, _ := a.check("from-env"); !ok {
		t.Error("the environment password was rejected")
	}
	if ok, _ := a.check("from-enV"); ok {
		t.Error("a wrong password was accepted")
	}
	if err := a.setPassword("correct horse"); !errors.Is(err, errEnvPassword) {
		t.Errorf("setPassword with an environment password = %v, want errEnvPassword", err)
	}

	// The first start with this password clears sessions; a restart with the same one keeps them.
	if err := store.CreateSession("s1", "csrf", time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := a.syncEnv(); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Session("s1"); !errors.Is(err, archive.ErrNotFound) {
		t.Error("a new environment password kept old sessions")
	}
	if err := store.CreateSession("s2", "csrf", time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := a.syncEnv(); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Session("s2"); err != nil {
		t.Errorf("an unchanged environment password cleared sessions: %v", err)
	}
	changed := &auth{store: store, envPassword: "another", now: time.Now}
	if err := changed.syncEnv(); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Session("s2"); !errors.Is(err, archive.ErrNotFound) {
		t.Error("a changed environment password kept old sessions")
	}
	removed := &auth{store: store, now: time.Now}
	if err := store.CreateSession("s3", "csrf", time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := removed.syncEnv(); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Session("s3"); !errors.Is(err, archive.ErrNotFound) {
		t.Error("removing the environment password kept old sessions")
	}
}

func TestThrottle(t *testing.T) {
	a, _ := newAuth(t, "")
	now := time.Unix(1_700_000_000, 0)
	a.now = func() time.Time { return now }
	for range 4 {
		a.failed()
	}
	if w := a.wait(); w != 0 {
		t.Fatalf("locked after four failures: %v", w)
	}
	a.failed()
	if w := a.wait(); w != time.Second {
		t.Fatalf("wait after five failures = %v, want 1s", w)
	}
	a.failed()
	if w := a.wait(); w != 2*time.Second {
		t.Fatalf("wait after six failures = %v, want 2s", w)
	}
	now = now.Add(2 * time.Second)
	if w := a.wait(); w != 0 {
		t.Fatalf("still locked after the delay passed: %v", w)
	}
	for range 30 {
		a.failed()
	}
	if w := a.wait(); w != 5*time.Minute {
		t.Fatalf("wait after many failures = %v, want the 5m cap", w)
	}
	a.succeeded()
	if w := a.wait(); w != 0 {
		t.Fatalf("locked after a success: %v", w)
	}
}

func TestSessionCookie(t *testing.T) {
	a, store := newAuth(t, "")
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/login", nil)
	req.Header.Set("X-Forwarded-Proto", "https")
	if err := a.startSession(rec, req); err != nil {
		t.Fatal(err)
	}
	cookies := rec.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("%d cookies set", len(cookies))
	}
	c := cookies[0]
	if c.Name != sessionCookie || !c.HttpOnly || !c.Secure || c.SameSite != http.SameSiteLaxMode || c.Path != "/" || c.MaxAge != 30*24*3600 {
		t.Errorf("cookie = %+v", c)
	}
	if _, err := store.Session(c.Value); err == nil {
		t.Error("the raw token is stored; only its hash may be")
	}

	next := httptest.NewRequest(http.MethodGet, "/", nil)
	next.AddCookie(c)
	sess, ok := a.session(next)
	if !ok || sess.CSRF == "" || sess.CSRF == c.Value {
		t.Fatalf("session = %+v, %v", sess, ok)
	}
	if _, ok := a.session(httptest.NewRequest(http.MethodGet, "/", nil)); ok {
		t.Error("a request without a cookie has a session")
	}
	forged := httptest.NewRequest(http.MethodGet, "/", nil)
	forged.AddCookie(&http.Cookie{Name: sessionCookie, Value: "made-up"})
	if _, ok := a.session(forged); ok {
		t.Error("a made-up token has a session")
	}

	out := httptest.NewRecorder()
	a.endSession(out, next)
	if _, ok := a.session(next); ok {
		t.Error("the session survived endSession")
	}
	if cleared := out.Result().Cookies(); len(cleared) != 1 || cleared[0].MaxAge >= 0 {
		t.Errorf("endSession did not clear the cookie: %+v", cleared)
	}

	plain := httptest.NewRecorder()
	if err := a.startSession(plain, httptest.NewRequest(http.MethodPost, "/login", nil)); err != nil {
		t.Fatal(err)
	}
	if plain.Result().Cookies()[0].Secure {
		t.Error("the cookie is Secure on a plain HTTP request, so it would never be sent back")
	}
}
```

- [ ] **Step 3: Run the tests to verify they fail**

Run: `go test ./internal/web/`
Expected: FAIL to build with `undefined: auth`, `undefined: hashPassword`.

- [ ] **Step 4: Write the implementation**

`internal/web/auth.go`:

```go
package web

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"golang.org/x/crypto/argon2"

	"github.com/fenelon/arc5/internal/archive"
)

const (
	sessionCookie   = "arc5_session"
	sessionLifetime = 30 * 24 * time.Hour
	minPasswordLen  = 8

	passwordKey    = "password_hash"
	envPasswordKey = "env_password_hash"

	// argon2id at OWASP's minimum: 19 MiB, 2 passes, 1 thread. Small enough
	// for a modest server, slow enough to make guessing a stolen hash costly.
	argonMemory  = 19 * 1024
	argonPasses  = 2
	argonThreads = 1
	argonKeyLen  = 32

	throttleAfter = 5
	throttleMax   = 5 * time.Minute
)

var (
	errShortPassword = fmt.Errorf("the password needs at least %d characters", minPasswordLen)
	errEnvPassword   = errors.New("the password is set by ARC5_PASSWORD and cannot be changed here")
)

func hashPassword(password string) (string, error) {
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	key := argon2.IDKey([]byte(password), salt, argonPasses, argonMemory, argonThreads, argonKeyLen)
	return fmt.Sprintf("argon2id$%d$%d$%d$%s$%s", argonMemory, argonPasses, argonThreads,
		base64.RawStdEncoding.EncodeToString(salt), base64.RawStdEncoding.EncodeToString(key)), nil
}

func verifyPassword(encoded, password string) bool {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[0] != "argon2id" {
		return false
	}
	memory, err1 := strconv.ParseUint(parts[1], 10, 32)
	passes, err2 := strconv.ParseUint(parts[2], 10, 32)
	threads, err3 := strconv.ParseUint(parts[3], 10, 8)
	salt, err4 := base64.RawStdEncoding.DecodeString(parts[4])
	want, err5 := base64.RawStdEncoding.DecodeString(parts[5])
	if err1 != nil || err2 != nil || err3 != nil || err4 != nil || err5 != nil || len(want) == 0 || threads == 0 {
		return false
	}
	got := argon2.IDKey([]byte(password), salt, uint32(passes), uint32(memory), uint8(threads), uint32(len(want)))
	return subtle.ConstantTimeCompare(got, want) == 1
}

// auth owns the password, browser sessions and the login throttle.
type auth struct {
	store       *archive.Store
	envPassword string
	now         func() time.Time

	mu          sync.Mutex
	failures    int
	lockedUntil time.Time
}

func (a *auth) usesEnv() bool { return a.envPassword != "" }

func (a *auth) hasPassword() (bool, error) {
	if a.usesEnv() {
		return true, nil
	}
	hash, _, err := a.store.Setting(passwordKey)
	return hash != "", err
}

// setPassword stores a new password and signs every browser out.
func (a *auth) setPassword(password string) error {
	if a.usesEnv() {
		return errEnvPassword
	}
	if utf8.RuneCountInString(password) < minPasswordLen {
		return errShortPassword
	}
	hash, err := hashPassword(password)
	if err != nil {
		return err
	}
	if err := a.store.SetSetting(passwordKey, hash); err != nil {
		return err
	}
	return a.store.DeleteAllSessions()
}

func (a *auth) check(password string) (bool, error) {
	if a.usesEnv() {
		got, want := sha256.Sum256([]byte(password)), sha256.Sum256([]byte(a.envPassword))
		return subtle.ConstantTimeCompare(got[:], want[:]) == 1, nil
	}
	hash, _, err := a.store.Setting(passwordKey)
	if err != nil || hash == "" {
		return false, err
	}
	return verifyPassword(hash, password), nil
}

// syncEnv signs every browser out when ARC5_PASSWORD is not what it was at
// the last start: newly set, changed, or removed.
func (a *auth) syncEnv() error {
	seen, _, err := a.store.Setting(envPasswordKey)
	if err != nil {
		return err
	}
	if !a.usesEnv() && seen == "" {
		return nil
	}
	if a.usesEnv() && seen != "" && verifyPassword(seen, a.envPassword) {
		return nil
	}
	next := ""
	if a.usesEnv() {
		if next, err = hashPassword(a.envPassword); err != nil {
			return err
		}
	}
	if err := a.store.SetSetting(envPasswordKey, next); err != nil {
		return err
	}
	return a.store.DeleteAllSessions()
}

// wait is how long until another login attempt is allowed.
func (a *auth) wait() time.Duration {
	a.mu.Lock()
	defer a.mu.Unlock()
	return max(a.lockedUntil.Sub(a.now()), 0)
}

// failed records a wrong password. From the fifth on, each one doubles the
// wait before the next attempt, up to five minutes.
func (a *auth) failed() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.failures++
	if a.failures < throttleAfter {
		return
	}
	delay := throttleMax
	if shift := a.failures - throttleAfter; shift < 9 {
		delay = min(time.Second<<shift, throttleMax)
	}
	a.lockedUntil = a.now().Add(delay)
}

func (a *auth) succeeded() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.failures, a.lockedUntil = 0, time.Time{}
}

func randomToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

func tokenHash(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// isSecure reports whether the browser reached us over TLS, directly or
// through a proxy that says so.
func isSecure(r *http.Request) bool {
	return r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https")
}

func (a *auth) startSession(w http.ResponseWriter, r *http.Request) error {
	token, err := randomToken()
	if err != nil {
		return err
	}
	csrf, err := randomToken()
	if err != nil {
		return err
	}
	expires := a.now().Add(sessionLifetime)
	if err := a.store.CreateSession(tokenHash(token), csrf, expires); err != nil {
		return err
	}
	http.SetCookie(w, &http.Cookie{
		Name: sessionCookie, Value: token, Path: "/",
		Expires: expires, MaxAge: int(sessionLifetime / time.Second),
		HttpOnly: true, SameSite: http.SameSiteLaxMode, Secure: isSecure(r),
	})
	return nil
}

func (a *auth) session(r *http.Request) (archive.Session, bool) {
	c, err := r.Cookie(sessionCookie)
	if err != nil {
		return archive.Session{}, false
	}
	sess, err := a.store.Session(tokenHash(c.Value))
	return sess, err == nil
}

func (a *auth) endSession(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(sessionCookie); err == nil {
		a.store.DeleteSession(tokenHash(c.Value))
	}
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: "", Path: "/", MaxAge: -1, HttpOnly: true, SameSite: http.SameSiteLaxMode, Secure: isSecure(r)})
}
```

- [ ] **Step 5: Run the tests to verify they pass**

Run: `go mod tidy && go vet ./... && go test -race -count=1 -timeout 180s ./...`
Expected: PASS. (`internal/web/server_test.go` from milestone 1 still passes; it is replaced in Task 5.)

- [ ] **Step 6: Commit**

```bash
git add go.mod go.sum internal/web
git commit -m "Add password hashing, sessions and login throttling"
```

---

### Task 5: Assets, styles, templates and login

**Files:**
- Create: `internal/web/static/app.css`, `internal/web/static/js/app.js`, `internal/web/static/js/theme_controller.js`, `internal/web/static/js/picker_controller.js`
- Create (downloaded): `internal/web/static/js/stimulus.js`, `internal/web/static/fonts/*.woff2`, `internal/web/static/fonts/LICENSE`
- Create: `internal/web/page.go`, `internal/web/auth_handlers.go`, `internal/web/templates/partials.html`, `internal/web/templates/login.html`, `internal/web/templates/setup.html`, `internal/web/templates/empty.html`
- Replace: `internal/web/server.go`, `internal/web/server_test.go`
- Modify: `cmd/arc5/main.go` (one line, so the build keeps passing)

**Interfaces:**
- Consumes: `auth` and its methods (Task 4), `archive.Store`, `archive.Session`, `connector.Status`, `connector.Registry`.
- Produces:
  - `type Runner interface { Status() []connector.Status; RequestMedia(attachmentID int64) error }`
  - `type Deps struct { Store *archive.Store; Runner Runner; Registry *connector.Registry; EnvPassword string; Now func() time.Time; Location *time.Location }`
  - `New(deps Deps) (*Server, error)`; `*Server` implements `http.Handler`
  - `(*Server).signedIn(next func(http.ResponseWriter, *http.Request, archive.Session)) http.Handler` — redirects to `/setup` or `/login`, and refuses a `POST` without the session's CSRF token
  - `(*Server).render(w http.ResponseWriter, status int, name string, data any)`, `(*Server).page(r *http.Request, sess archive.Session, title string) Page`
  - view types `Page`, `Picker`, `PickerGroup`, `Tile`, `Alert` (fields in the code below)
  - templates `head`, `brand`, `logo`, `state`, `tile`, `picker`, `topbar`, `banner`, `login`, `setup`, `empty`; template functions `asset` and `dict`
  - routes `GET /healthz`, `GET /static/{version}/{path...}`, `GET|POST /setup`, `GET|POST /login`, `POST /logout`, `GET /`
  - test helpers in `server_test.go`: `newSite(t, env) *site`, `(*site).get`, `(*site).post`, `(*site).signIn`, `fakeRunner`

- [ ] **Step 1: Vendor Stimulus and the font**

```bash
mkdir -p internal/web/static/js internal/web/static/fonts internal/web/templates
curl -sSf -o internal/web/static/js/stimulus.js https://cdn.jsdelivr.net/npm/@hotwired/stimulus@3.2.2/dist/stimulus.js
for subset in latin latin-ext cyrillic cyrillic-ext; do
  curl -sSf -o "internal/web/static/fonts/geologica-$subset-wght-normal.woff2" \
    "https://cdn.jsdelivr.net/npm/@fontsource-variable/geologica@5.2.8/files/geologica-$subset-wght-normal.woff2"
done
curl -sSf -o internal/web/static/fonts/LICENSE https://cdn.jsdelivr.net/npm/@fontsource-variable/geologica@5.2.8/LICENSE
ls -l internal/web/static/js internal/web/static/fonts
```

Expected: `stimulus.js` is about 88 KB; the four font files are about 25, 22, 16 and 11 KB; `LICENSE` is the SIL Open Font License.

- [ ] **Step 2: Write the failing tests**

Replace `internal/web/server_test.go` with:

```go
package web

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/fenelon/arc5/internal/archive"
	"github.com/fenelon/arc5/internal/connector"
)

type fakeRunner struct {
	status    []connector.Status
	requested []int64
	err       error
}

func (f *fakeRunner) Status() []connector.Status { return f.status }
func (f *fakeRunner) RequestMedia(id int64) error {
	f.requested = append(f.requested, id)
	return f.err
}

// site is a server with a browser's memory: it keeps cookies and the CSRF
// token from the last page it saw.
type site struct {
	t       *testing.T
	store   *archive.Store
	runner  *fakeRunner
	reg     *connector.Registry
	srv     *Server
	cookies map[string]*http.Cookie
	csrf    string
}

func newSite(t *testing.T, envPassword string) *site {
	t.Helper()
	store, err := archive.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	s := &site{t: t, store: store, runner: &fakeRunner{}, reg: connector.NewRegistry(), cookies: map[string]*http.Cookie{}}
	s.srv, err = New(Deps{Store: store, Runner: s.runner, Registry: s.reg, EnvPassword: envPassword, Location: time.UTC})
	if err != nil {
		t.Fatal(err)
	}
	return s
}

var csrfField = regexp.MustCompile(`name="csrf" value="([^"]+)"`)

func (s *site) do(req *http.Request) *httptest.ResponseRecorder {
	s.t.Helper()
	for _, c := range s.cookies {
		req.AddCookie(c)
	}
	rec := httptest.NewRecorder()
	s.srv.ServeHTTP(rec, req)
	for _, c := range rec.Result().Cookies() {
		if c.MaxAge < 0 {
			delete(s.cookies, c.Name)
		} else {
			s.cookies[c.Name] = c
		}
	}
	if m := csrfField.FindStringSubmatch(rec.Body.String()); m != nil {
		s.csrf = m[1]
	}
	return rec
}

func (s *site) get(path string) *httptest.ResponseRecorder {
	s.t.Helper()
	return s.do(httptest.NewRequest(http.MethodGet, path, nil))
}

// post sends a form. It adds the CSRF token unless the form already has one.
func (s *site) post(path string, form url.Values) *httptest.ResponseRecorder {
	s.t.Helper()
	if form == nil {
		form = url.Values{}
	}
	if _, set := form["csrf"]; !set && s.csrf != "" {
		form.Set("csrf", s.csrf)
	}
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	return s.do(req)
}

const testPassword = "correct horse"

// signIn sets the password on a new archive and leaves the site signed in.
func (s *site) signIn() {
	s.t.Helper()
	rec := s.post("/setup", url.Values{"password": {testPassword}, "password_again": {testPassword}})
	if rec.Code != http.StatusSeeOther {
		s.t.Fatalf("setup: status %d, body %s", rec.Code, rec.Body)
	}
	s.get("/") // pick up the CSRF token from a page
	if s.csrf == "" {
		s.t.Fatal("no CSRF token on the home page")
	}
}

func redirectsTo(rec *httptest.ResponseRecorder, path string) bool {
	return rec.Code == http.StatusSeeOther && rec.Header().Get("Location") == path
}

func TestHealthIsPublic(t *testing.T) {
	rec := newSite(t, "").get("/healthz")
	if rec.Code != http.StatusOK || strings.TrimSpace(rec.Body.String()) != `{"status":"ok"}` {
		t.Fatalf("healthz = %d %q", rec.Code, rec.Body)
	}
}

func TestStaticFiles(t *testing.T) {
	s := newSite(t, "")
	css := s.get(s.srv.asset("app.css"))
	if css.Code != http.StatusOK || !strings.HasPrefix(css.Header().Get("Content-Type"), "text/css") ||
		!strings.Contains(css.Header().Get("Cache-Control"), "immutable") || !strings.Contains(css.Body.String(), "--accent") {
		t.Errorf("app.css: %d %v", css.Code, css.Header())
	}
	for _, name := range []string{"js/app.js", "js/stimulus.js", "js/theme_controller.js", "js/picker_controller.js",
		"fonts/geologica-latin-wght-normal.woff2", "fonts/geologica-cyrillic-wght-normal.woff2"} {
		if rec := s.get(s.srv.asset(name)); rec.Code != http.StatusOK || rec.Body.Len() == 0 {
			t.Errorf("%s: status %d, %d bytes", name, rec.Code, rec.Body.Len())
		}
	}
	for _, path := range []string{"/static/x/nope.css", "/static/x/", "/static/x/js/", "/static/x/fonts"} {
		if rec := s.get(path); rec.Code != http.StatusNotFound {
			t.Errorf("%s: status %d, want 404", path, rec.Code)
		}
	}
}

func TestFirstRunSetup(t *testing.T) {
	s := newSite(t, "")
	for _, path := range []string{"/", "/login"} {
		if rec := s.get(path); !redirectsTo(rec, "/setup") {
			t.Errorf("GET %s before setup: %d to %q, want a redirect to /setup", path, rec.Code, rec.Header().Get("Location"))
		}
	}
	form := s.get("/setup")
	if form.Code != http.StatusOK || !strings.Contains(form.Body.String(), `name="password_again"`) {
		t.Fatalf("setup form: %d", form.Code)
	}
	if got := form.Header().Get("Content-Security-Policy"); got != csp {
		t.Errorf("policy = %q", got)
	}

	for name, f := range map[string]url.Values{
		"too short": {"password": {"short"}, "password_again": {"short"}},
		"mismatch":  {"password": {testPassword}, "password_again": {"different horse"}},
	} {
		rec := s.post("/setup", f)
		if rec.Code != http.StatusUnprocessableEntity || !strings.Contains(rec.Body.String(), `role="alert"`) {
			t.Errorf("%s: status %d", name, rec.Code)
		}
		if strings.Contains(rec.Body.String(), "different horse") || strings.Contains(rec.Body.String(), testPassword) {
			t.Errorf("%s: the page echoes a password", name)
		}
	}
	s.signIn()
	if rec := s.get("/"); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "No sources yet") {
		t.Fatalf("home after setup: %d", rec.Code)
	}
}

// Review Focus 3: setup cannot be replayed.
func TestSetupIsClosedOnceAPasswordExists(t *testing.T) {
	s := newSite(t, "")
	s.signIn()
	stranger := &site{t: t, store: s.store, srv: s.srv, cookies: map[string]*http.Cookie{}}
	if rec := stranger.get("/setup"); !redirectsTo(rec, "/login") {
		t.Errorf("GET /setup: %d to %q", rec.Code, rec.Header().Get("Location"))
	}
	rec := stranger.post("/setup", url.Values{"password": {"attacker pass"}, "password_again": {"attacker pass"}})
	if !redirectsTo(rec, "/login") || len(stranger.cookies) != 0 {
		t.Fatalf("POST /setup with a password set: %d, cookies %v", rec.Code, stranger.cookies)
	}
	if ok, _ := s.srv.auth.check("attacker pass"); ok {
		t.Fatal("the password was replaced through /setup")
	}
	if ok, _ := s.srv.auth.check(testPassword); !ok {
		t.Fatal("the original password no longer works")
	}

	env := newSite(t, "from-env")
	if rec := env.post("/setup", url.Values{"password": {"attacker pass"}, "password_again": {"attacker pass"}}); !redirectsTo(rec, "/login") {
		t.Errorf("POST /setup with ARC5_PASSWORD set: %d", rec.Code)
	}
}

func TestLoginAndLogout(t *testing.T) {
	s := newSite(t, "")
	s.signIn()
	if rec := s.post("/logout", nil); !redirectsTo(rec, "/login") {
		t.Fatalf("logout: %d to %q", rec.Code, rec.Header().Get("Location"))
	}
	if rec := s.get("/"); !redirectsTo(rec, "/login") {
		t.Fatalf("home after logout: %d", rec.Code)
	}
	wrong := s.post("/login", url.Values{"password": {"wrong horse"}})
	if wrong.Code != http.StatusUnauthorized || !strings.Contains(wrong.Body.String(), `role="alert"`) || len(s.cookies) != 0 {
		t.Fatalf("wrong password: %d, cookies %v", wrong.Code, s.cookies)
	}
	if rec := s.post("/login", url.Values{"password": {testPassword}}); !redirectsTo(rec, "/") {
		t.Fatalf("right password: %d", rec.Code)
	}
	if rec := s.get("/"); rec.Code != http.StatusOK {
		t.Fatalf("home after login: %d", rec.Code)
	}
	if rec := s.get("/login"); !redirectsTo(rec, "/") {
		t.Errorf("login page while signed in: %d", rec.Code)
	}
}

func TestLoginIsThrottled(t *testing.T) {
	s := newSite(t, "from-env")
	for range 5 {
		s.post("/login", url.Values{"password": {"wrong"}})
	}
	rec := s.post("/login", url.Values{"password": {"from-env"}})
	if rec.Code != http.StatusTooManyRequests || len(s.cookies) != 0 {
		t.Fatalf("login while locked out: %d, cookies %v", rec.Code, s.cookies)
	}
	if !strings.Contains(rec.Body.String(), "Try again in") {
		t.Errorf("the page does not say when to try again: %s", rec.Body)
	}
}

// Review Focus 2: no page without a session, no change without the CSRF token.
func TestPagesNeedASessionAndPostsNeedTheToken(t *testing.T) {
	s := newSite(t, "")
	s.signIn()
	token := s.csrf

	stranger := &site{t: t, store: s.store, srv: s.srv, cookies: map[string]*http.Cookie{}}
	if rec := stranger.get("/"); !redirectsTo(rec, "/login") {
		t.Errorf("home without a session: %d", rec.Code)
	}
	if rec := stranger.post("/logout", url.Values{"csrf": {token}}); !redirectsTo(rec, "/login") {
		t.Errorf("logout without a session: %d", rec.Code)
	}

	for name, f := range map[string]url.Values{"no token": {"csrf": {""}}, "wrong token": {"csrf": {"made-up"}}} {
		if rec := s.post("/logout", f); rec.Code != http.StatusForbidden {
			t.Errorf("logout with %s: %d, want 403", name, rec.Code)
		}
	}
	if rec := s.get("/"); rec.Code != http.StatusOK {
		t.Fatal("a refused logout signed the owner out")
	}
}

func TestPagesCarryThemeAndHeaders(t *testing.T) {
	s := newSite(t, "")
	s.signIn()
	plain := s.get("/")
	if strings.Contains(plain.Body.String(), "data-theme=") {
		t.Error("a theme is forced although none was chosen")
	}
	for header, want := range map[string]string{
		"Content-Security-Policy": csp, "X-Content-Type-Options": "nosniff",
		"Referrer-Policy": "same-origin", "Cache-Control": "no-store",
	} {
		if got := plain.Header().Get(header); got != want {
			t.Errorf("%s = %q, want %q", header, got, want)
		}
	}
	if strings.Contains(plain.Body.String(), " style=") || strings.Contains(plain.Body.String(), "<style") {
		t.Error("the page has inline styles, which the policy blocks")
	}

	s.cookies["arc5_theme"] = &http.Cookie{Name: "arc5_theme", Value: "dark"}
	if !strings.Contains(s.get("/").Body.String(), `data-theme="dark"`) {
		t.Error("the dark theme cookie is ignored")
	}
	s.cookies["arc5_theme"] = &http.Cookie{Name: "arc5_theme", Value: `"><script>`}
	if body := s.get("/").Body.String(); strings.Contains(body, "data-theme=") || strings.Contains(body, "<script>") {
		t.Error("an invalid theme cookie reached the page")
	}
}
```

- [ ] **Step 3: Run the tests to verify they fail**

Run: `go test ./internal/web/`
Expected: FAIL to build with `undefined: Server`, `undefined: New`, `undefined: Deps`, `undefined: csp`.

- [ ] **Step 4: Write the styles**

`internal/web/static/app.css`:

```css
@font-face{font-family:"Geologica";font-style:normal;font-weight:100 900;font-display:swap;src:url("fonts/geologica-cyrillic-ext-wght-normal.woff2") format("woff2");unicode-range:U+0460-052F,U+1C80-1C8A,U+20B4,U+2DE0-2DFF,U+A640-A69F,U+FE2E-FE2F}
@font-face{font-family:"Geologica";font-style:normal;font-weight:100 900;font-display:swap;src:url("fonts/geologica-cyrillic-wght-normal.woff2") format("woff2");unicode-range:U+0301,U+0400-045F,U+0490-0491,U+04B0-04B1,U+2116}
@font-face{font-family:"Geologica";font-style:normal;font-weight:100 900;font-display:swap;src:url("fonts/geologica-latin-ext-wght-normal.woff2") format("woff2");unicode-range:U+0100-02BA,U+02BD-02C5,U+02C7-02CC,U+02CE-02D7,U+02DD-02FF,U+0304,U+0308,U+0329,U+1D00-1DBF,U+1E00-1E9F,U+1EF2-1EFF,U+2020,U+20A0-20AB,U+20AD-20C0,U+2113,U+2C60-2C7F,U+A720-A7FF}
@font-face{font-family:"Geologica";font-style:normal;font-weight:100 900;font-display:swap;src:url("fonts/geologica-latin-wght-normal.woff2") format("woff2");unicode-range:U+0000-00FF,U+0131,U+0152-0153,U+02BB-02BC,U+02C6,U+02DA,U+02DC,U+0304,U+0308,U+0329,U+2000-206F,U+20AC,U+2122,U+2191,U+2193,U+2212,U+2215,U+FEFF,U+FFFD}

/* Light theme: cool paper and teal. */
:root{
  --bg:#EEF3F5;--pane:#FFFFFF;--raised:#FFFFFF;--line:#CFDBDF;--line-soft:#E4ECEE;
  --ink:#0D2530;--muted:#4C6470;--accent:#0B6E7F;--accent-ink:#FFFFFF;--accent-soft:#F1F8F9;
  --own:#D7ECEF;--own-line:#B9DADF;--own-meta:#3A525D;
  --hit:#F4D38A;--hit-ink:#0D2530;--warn-bg:#FBEBCB;--warn-ink:#7A4700;--warn-line:#E9CF95;
  --live:#1E9E6A;--live-halo:#CFEEDD;
  --band-0:#E4ECEE;--band-1:#D7ECEF;--band-2:#9CCBD3;--band-3:#3F97A6;--band-4:#0B6E7F;--band-now:#E0A93B;
  --tg-bg:#E0F2FB;--wa-bg:#DFF7E8;--mail-bg:#E3F1F4;--all-bg:#E4ECEE;
  --mark-1:#9CCBD3;--mark-2:#3F97A6;--mark-3:#0B6E7F;--mark-4:#0D2530;
  --shadow:0 12px 32px rgba(13,37,48,.14);
  color-scheme:light;
}
/* Dark theme: neutral grey and orange. Declared twice: once for a device set
   to dark with no choice made, once for an explicit choice. */
@media (prefers-color-scheme:dark){:root:not([data-theme="light"]){
  --bg:#17181A;--pane:#1E2022;--raised:#26282B;--line:#34373B;--line-soft:#2C2E31;
  --ink:#ECEDEE;--muted:#A4A8AD;--accent:#FF8A1F;--accent-ink:#17181A;--accent-soft:#2E2519;
  --own:#3A2A17;--own-line:#6B4520;--own-meta:#C9B8A4;
  --hit:#5A4A00;--hit-ink:#FFE98A;--warn-bg:#472320;--warn-ink:#FFA79B;--warn-line:#6B3A34;
  --live:#5FD08C;--live-halo:#24402E;
  --band-0:#2C2E31;--band-1:#3C3F43;--band-2:#5A5E63;--band-3:#8A8F95;--band-4:#C9CCD0;--band-now:#FF8A1F;
  --tg-bg:#1B3442;--wa-bg:#1A3A27;--mail-bg:#3A2A17;--all-bg:#3C3F43;
  --mark-1:#ECEDEE;--mark-2:#FF8A1F;--mark-3:#8A8F95;--mark-4:#6B4520;
  --shadow:0 12px 32px rgba(0,0,0,.5);
  color-scheme:dark;
}}
:root[data-theme="dark"]{
  --bg:#17181A;--pane:#1E2022;--raised:#26282B;--line:#34373B;--line-soft:#2C2E31;
  --ink:#ECEDEE;--muted:#A4A8AD;--accent:#FF8A1F;--accent-ink:#17181A;--accent-soft:#2E2519;
  --own:#3A2A17;--own-line:#6B4520;--own-meta:#C9B8A4;
  --hit:#5A4A00;--hit-ink:#FFE98A;--warn-bg:#472320;--warn-ink:#FFA79B;--warn-line:#6B3A34;
  --live:#5FD08C;--live-halo:#24402E;
  --band-0:#2C2E31;--band-1:#3C3F43;--band-2:#5A5E63;--band-3:#8A8F95;--band-4:#C9CCD0;--band-now:#FF8A1F;
  --tg-bg:#1B3442;--wa-bg:#1A3A27;--mail-bg:#3A2A17;--all-bg:#3C3F43;
  --mark-1:#ECEDEE;--mark-2:#FF8A1F;--mark-3:#8A8F95;--mark-4:#6B4520;
  --shadow:0 12px 32px rgba(0,0,0,.5);
  color-scheme:dark;
}

*{box-sizing:border-box}
html{font-family:"Geologica",system-ui,sans-serif;font-weight:350;font-size:15px;line-height:1.45;color:var(--ink);background:var(--bg)}
body{margin:0;min-height:100vh}
h1,h2,h3,p{margin:0}
a{color:var(--accent)}
:focus-visible{outline:2px solid var(--accent);outline-offset:2px}
svg{display:block}
.visually-hidden{position:absolute;width:1px;height:1px;overflow:hidden;clip-path:inset(50%)}
.only-phone{display:none}

/* Buttons and links that look like them. */
.button{display:inline-flex;align-items:center;justify-content:center;gap:8px;min-height:44px;padding:0 16px;font:inherit;font-weight:600;color:var(--accent-ink);background:var(--accent);border:0;border-radius:8px;text-decoration:none;cursor:pointer}
.button--quiet{color:var(--ink);background:var(--pane);border:1px solid var(--line)}
.button--warn{color:var(--warn-bg);background:var(--warn-ink)}
.icon-button{display:flex;align-items:center;justify-content:center;width:44px;height:44px;padding:0;color:var(--ink);background:transparent;border:1px solid var(--line);border-radius:8px;cursor:pointer}
.icon-button svg{width:18px;height:18px}
.link-button{min-height:44px;padding:0 4px;font:inherit;font-weight:450;color:var(--ink);background:none;border:0;cursor:pointer}
.more{display:block;margin:12px auto;padding:11px 18px;width:max-content;max-width:100%;font-weight:600;color:var(--ink);background:var(--pane);border:1px solid var(--line);border-radius:8px;text-decoration:none}

/* Top bar. */
.app{display:flex;flex-direction:column}
.topbar{position:relative;display:flex;flex-wrap:wrap;align-items:center;gap:10px 20px;padding:10px 20px;background:var(--pane);border-bottom:1px solid var(--line)}
.topbar__space{flex:1 1 0}
.topbar form{margin:0}
.brand{display:inline-flex;align-items:center;gap:10px;color:var(--ink);text-decoration:none}
.brand__mark{display:flex;flex-direction:column;gap:3px;width:22px}
.brand__mark span{height:4px;border-radius:2px}
.brand__mark span:nth-child(1){width:22px;background:var(--mark-1)}
.brand__mark span:nth-child(2){width:16px;background:var(--mark-2)}
.brand__mark span:nth-child(3){width:22px;background:var(--mark-3)}
.brand__mark span:nth-child(4){width:12px;background:var(--mark-4)}
.brand__name{font-weight:700;font-size:21px;letter-spacing:-.02em}

/* Service logo in a tinted square. */
.logo{display:flex;align-items:center;justify-content:center;flex-shrink:0;width:34px;height:34px;border-radius:8px;background:var(--all-bg);color:var(--ink)}
.logo svg{width:20px;height:20px}
.logo--telegram{background:var(--tg-bg);color:#26A5E4}
.logo--whatsapp{background:var(--wa-bg);color:#25D366}
.logo--email{background:var(--mail-bg);color:var(--accent)}

/* State marks. */
.state{display:flex;align-items:center;justify-content:center;flex-shrink:0;width:24px;height:24px}
.state svg{width:18px;height:18px}
.state--live::before{content:"";width:10px;height:10px;border-radius:50%;background:var(--live);box-shadow:0 0 0 4px var(--live-halo)}
.state--lock,.state--off{border-radius:50%;color:var(--warn-ink);background:var(--warn-bg)}
.state--lock svg,.state--off svg{width:14px;height:14px}
.state--sync{color:var(--muted)}

/* A source tile: logo, name over service, state. */
.tile{display:flex;align-items:center;gap:10px;min-height:52px;padding:8px 10px;color:var(--ink);background:var(--raised);border:1px solid var(--line);border-radius:10px;text-decoration:none}
.tile--current{padding:7px 9px;background:var(--accent-soft);border:2px solid var(--accent)}
.tile__text{display:flex;flex-direction:column;flex:1 1 auto;min-width:0;line-height:1.2}
.tile__name{overflow:hidden;white-space:nowrap;text-overflow:ellipsis;font-weight:600}
.tile__sub{overflow:hidden;white-space:nowrap;text-overflow:ellipsis;font-size:12.5px;color:var(--muted)}

/* Source picker: a <details> whose summary is the current source's tile. */
.picker{position:static}
.picker__button{display:inline-flex;align-items:center;gap:10px;min-height:44px;max-width:100%;padding:4px 10px 4px 6px;background:var(--pane);border:1px solid var(--line);border-radius:8px;cursor:pointer;list-style:none}
.picker__button::-webkit-details-marker{display:none}
.picker__button .logo{width:30px;height:30px;border-radius:7px}
.picker__button .logo svg{width:18px;height:18px}
.picker__button .tile__sub{font-size:12px}
.picker__chevron{width:16px;height:16px;color:var(--muted)}
.picker[open] .picker__button{border-color:var(--accent)}
.picker[open] .picker__chevron{transform:rotate(180deg)}
.picker__panel{position:absolute;z-index:10;top:100%;left:20px;width:min(680px,calc(100vw - 40px));margin-top:8px;padding:12px;background:var(--pane);border:1px solid var(--line);border-radius:12px;box-shadow:var(--shadow)}
.picker__heading{margin:14px 2px 8px;font-size:13.5px;font-weight:600;color:var(--muted)}
.picker__heading:first-child{margin-top:0}
.picker__grid{display:grid;grid-template-columns:repeat(3,minmax(0,1fr));gap:8px}
.picker__foot{margin-top:12px;padding:10px 2px 0;border-top:1px solid var(--line-soft);font-size:13.5px;font-weight:600}

/* Banner for a source that is not archiving. */
.banner{display:flex;flex-wrap:wrap;align-items:center;gap:8px 16px;padding:12px 20px;color:var(--warn-ink);background:var(--warn-bg);border-bottom:1px solid var(--warn-line)}
.banner span{flex:1 1 320px}
.banner strong{font-weight:600}

/* Two panes: chat list and conversation. */
.shell{display:flex;flex:1 1 auto;align-items:stretch;min-height:0}
.list{flex:0 0 360px;max-width:360px;background:var(--pane);border-right:1px solid var(--line);overflow-y:auto}
.conversation{display:flex;flex-direction:column;flex:1 1 0;min-width:0}
.empty{padding:24px 20px;color:var(--muted)}
.empty--center{margin:auto;text-align:center}
.row{display:flex;gap:12px;padding:12px 16px;color:var(--ink);text-decoration:none;border-bottom:1px solid var(--line-soft)}
.row--current{color:var(--accent-ink);background:var(--accent)}
.row__body{flex:1 1 auto;min-width:0}
.row__top{display:flex;align-items:baseline;justify-content:space-between;gap:10px}
.row__name{overflow:hidden;white-space:nowrap;text-overflow:ellipsis;font-weight:600}
.row__when{flex-shrink:0;font-size:13px;color:var(--muted)}
.row__preview{display:block;overflow:hidden;white-space:nowrap;text-overflow:ellipsis;font-size:14px;color:var(--muted)}
.row--current .row__when,.row--current .row__preview{color:inherit;opacity:.85}
.avatar{display:flex;align-items:center;justify-content:center;flex-shrink:0;width:42px;height:42px;border-radius:10px;font-weight:600;color:var(--accent);background:var(--own)}
.row--current .avatar{color:var(--accent);background:var(--pane)}

/* Conversation. */
.conv-head{display:flex;align-items:center;gap:12px;padding:12px 24px;background:var(--pane);border-bottom:1px solid var(--line)}
.conv-head__text{flex:1 1 auto;min-width:0}
.conv-head h1{font-weight:650;font-size:21px;line-height:1.2;letter-spacing:-.01em}
.conv-head p{font-size:14px;color:var(--muted)}
.back{align-items:center;justify-content:center;width:44px;height:44px;margin-left:-12px;color:var(--ink)}
.back svg{width:22px;height:22px}
.jump{align-items:center;gap:8px}
.jump select{height:44px;padding:0 8px;font:inherit;color:var(--ink);background:var(--pane);border:1px solid var(--line);border-radius:8px}
.conv-body{display:flex;flex:1 1 auto;align-items:stretch;min-height:0}
.messages{display:flex;flex-direction:column;gap:8px;flex:1 1 auto;min-width:0;padding:18px 24px 32px}
.day{display:flex;align-items:center;gap:12px;margin:10px 0 2px;font-size:13.5px;font-weight:600;color:var(--muted)}
.day::after{content:"";flex:1 1 auto;height:1px;background:var(--line)}
.service{margin:6px 0;text-align:center;font-size:14px;color:var(--muted)}
.msg{align-self:flex-start;max-width:min(520px,86%);padding:9px 13px 7px;background:var(--raised);border:1px solid var(--line);border-radius:8px}
.msg--out{align-self:flex-end;background:var(--own);border-color:var(--own-line)}
.msg--target{border:1.5px solid var(--band-now)}
.msg__sender{font-size:13.5px;font-weight:600;color:var(--accent)}
.msg__fwd{font-size:13px;color:var(--muted)}
.msg__text{font-size:15.5px;line-height:1.5;white-space:pre-wrap;overflow-wrap:anywhere}
.msg__meta{display:flex;flex-wrap:wrap;align-items:center;gap:6px 10px;margin-top:3px;font-size:12.5px;color:var(--muted)}
.msg--out .msg__meta{justify-content:flex-end;color:var(--own-meta)}
.tag{padding:2px 8px;font-size:12.5px;font-weight:600;color:var(--muted);background:var(--line-soft);border-radius:4px}
.tag--warn{color:var(--warn-ink);background:var(--warn-bg)}
.versions{margin-top:4px;font-size:13px;color:var(--muted)}
.versions summary{cursor:pointer;font-weight:600}
.versions__item{margin-top:4px;padding:6px 10px;color:var(--ink);background:var(--line-soft);border-radius:6px;white-space:pre-wrap;overflow-wrap:anywhere}
.versions__when{color:var(--muted)}
.media{display:block;max-width:100%;margin-bottom:4px;border-radius:4px}
.media--image{max-height:320px}
.file{display:flex;flex-wrap:wrap;align-items:center;gap:8px 14px;margin-bottom:4px}
.file__text{flex:1 1 200px;min-width:0}
.file__name{font-weight:600;overflow-wrap:anywhere}
.file__detail{font-size:13.5px;color:var(--muted)}
.file form{margin:0}

/* Year rail: four bands per year, newest at the top. */
.rail{display:flex;flex-direction:column;gap:10px;flex-shrink:0;width:76px;padding:16px 12px;background:var(--pane);border-left:1px solid var(--line);overflow-y:auto}
.rail__year{display:flex;flex-direction:column;gap:3px}
.rail__label{font-size:13px;font-weight:500;color:var(--muted)}
.rail__year--now .rail__label{font-weight:700;color:var(--ink)}
.band{display:block;height:9px;border-radius:2px;background:var(--band-0)}
.band--1{background:var(--band-1)}
.band--2{background:var(--band-2)}
.band--3{background:var(--band-3)}
.band--4{background:var(--band-4)}
.band--now{background:var(--band-now)}

/* Plain pages: login, first run, sources. */
.plain{max-width:420px;margin:0 auto;padding:10vh 24px 48px}
.plain--wide{max-width:980px;padding-top:28px}
.plain h1{margin-bottom:8px;font-weight:650;font-size:28px;letter-spacing:-.02em}
.plain p{color:var(--muted)}
.card{margin-top:20px;padding:20px;background:var(--pane);border:1px solid var(--line);border-radius:12px}
.field{display:flex;flex-direction:column;gap:4px;margin-bottom:14px}
.field label{font-size:13px;color:var(--muted)}
.field input{height:44px;padding:0 12px;font:inherit;color:var(--ink);background:var(--pane);border:1px solid var(--line);border-radius:8px}
.alert{margin-bottom:14px;padding:10px 12px;color:var(--warn-ink);background:var(--warn-bg);border-radius:8px}
.code{padding:2px 6px;font-family:ui-monospace,Menlo,Consolas,monospace;font-size:13.5px;color:var(--ink);background:var(--line-soft);border-radius:4px}
.group-heading{margin:22px 0 8px;font-size:14px;font-weight:600;color:var(--muted)}
.tiles{display:grid;grid-template-columns:repeat(auto-fill,minmax(280px,1fr));gap:8px}
.tile--wide{min-height:60px}
.tile--wide .tile__name{font-size:16px}
.tile--wide .tile__sub{font-size:13px}
.tile--warn{border-color:var(--warn-line)}
.tile--warn .tile__sub{color:var(--warn-ink)}

/* Phones: one pane at a time, the rail gives way to a year picker. */
@media (max-width:760px){
  .only-phone{display:flex}
  .topbar{padding:8px 14px;gap:8px 10px}
  .picker{flex:1 1 auto;min-width:0}
  .picker__button{width:100%}
  .picker__panel{left:14px;width:calc(100vw - 28px)}
  .picker__grid{grid-template-columns:1fr}
  .list{flex:1 1 auto;max-width:none;border-right:0}
  .conversation{display:none}
  .shell--open .list{display:none}
  .shell--open .conversation{display:flex}
  .conv-head{padding:8px 14px}
  .messages{padding:14px 14px 24px}
  .rail{display:none}
}
```

- [ ] **Step 5: Write the scripts**

`internal/web/static/js/app.js`:

```js
import { Application } from "./stimulus.js"
import ThemeController from "./theme_controller.js"
import PickerController from "./picker_controller.js"

const application = Application.start()
application.register("theme", ThemeController)
application.register("picker", PickerController)
```

`internal/web/static/js/theme_controller.js`:

```js
import { Controller } from "./stimulus.js"

// Switches between the light and dark theme and remembers the choice in a
// cookie, so the server renders the right theme on the next page.
export default class extends Controller {
  toggle() {
    const root = document.documentElement
    const dark = root.dataset.theme
      ? root.dataset.theme === "dark"
      : window.matchMedia("(prefers-color-scheme: dark)").matches
    const next = dark ? "light" : "dark"
    root.dataset.theme = next
    document.cookie = `arc5_theme=${next}; path=/; max-age=31536000; samesite=lax`
  }
}
```

`internal/web/static/js/picker_controller.js`:

```js
import { Controller } from "./stimulus.js"

// The picker is a <details> element and works without this. The controller
// closes it on Escape and on a click anywhere else.
export default class extends Controller {
  connect() {
    this.onClick = (event) => {
      if (!this.element.contains(event.target)) this.element.open = false
    }
    this.onKey = (event) => {
      if (event.key === "Escape") this.element.open = false
    }
    document.addEventListener("click", this.onClick)
    document.addEventListener("keydown", this.onKey)
  }

  disconnect() {
    document.removeEventListener("click", this.onClick)
    document.removeEventListener("keydown", this.onKey)
  }
}
```

- [ ] **Step 6: Write the view types**

`internal/web/page.go`:

```go
package web

// Page is what every signed-in page shows around its own content.
type Page struct {
	Title  string
	Theme  string  // "", "light" or "dark"; empty follows the device
	CSRF   string  // the session's token, for forms
	Picker *Picker // nil when there are no sources
	Alert  *Alert  // nil when every source is archiving
}

// Tile is one source, or "Everything at once", as the picker and the Sources
// page show it.
type Tile struct {
	Name       string // the name the owner gave the source
	Service    string // "Telegram", or a count for "Everything at once"
	Logo       string // "telegram", "whatsapp", "email", "all" or "generic"
	State      string // "live", "catching-up", "needs-login", "offline" or ""
	StateLabel string
	Detail     string // a longer line for the Sources page
	Href       string
	Current    bool
	Warn       bool // drawn with the attention border
}

// PickerGroup is the sources of one display type.
type PickerGroup struct {
	Title string
	Tiles []Tile // "Everything at once" first
}

// Picker is the source picker in the top bar.
type Picker struct {
	Current Tile
	Groups  []PickerGroup
}

// Alert names a source that is not archiving.
type Alert struct {
	Name   string
	Detail string
}
```

- [ ] **Step 7: Write the templates**

`internal/web/templates/partials.html`:

```html
{{define "head"}}<!doctype html>
<html lang="en"{{if .Theme}} data-theme="{{.Theme}}"{{end}}>
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>{{.Title}} - arc5</title>
<link rel="stylesheet" href="{{asset "app.css"}}">
<script type="module" src="{{asset "js/app.js"}}"></script>
</head>
{{end}}

{{define "brand"}}<a class="brand" href="/"><span class="brand__mark" aria-hidden="true"><span></span><span></span><span></span><span></span></span><span class="brand__name">arc5</span></a>{{end}}

{{define "logo"}}<span class="logo logo--{{.}}" aria-hidden="true">{{if eq . "telegram"}}<svg viewBox="0 0 24 24" fill="currentColor"><path d="M11.944 0A12 12 0 0 0 0 12a12 12 0 0 0 12 12 12 12 0 0 0 12-12A12 12 0 0 0 12 0a12 12 0 0 0-.056 0zm4.962 7.224c.1-.002.321.023.465.14a.506.506 0 0 1 .171.325c.016.093.036.306.02.472-.18 1.898-.962 6.502-1.36 8.627-.168.9-.499 1.201-.82 1.23-.696.065-1.225-.46-1.9-.902-1.056-.693-1.653-1.124-2.678-1.8-1.185-.78-.417-1.21.258-1.91.177-.184 3.247-2.977 3.307-3.23.007-.032.014-.15-.056-.212s-.174-.041-.249-.024c-.106.024-1.793 1.14-5.061 3.345-.48.33-.913.49-1.302.48-.428-.008-1.252-.241-1.865-.44-.752-.245-1.349-.374-1.297-.789.027-.216.325-.437.893-.663 3.498-1.524 5.83-2.529 6.998-3.014 3.332-1.386 4.025-1.627 4.476-1.635z"/></svg>{{else if eq . "whatsapp"}}<svg viewBox="0 0 24 24" fill="currentColor"><path d="M17.472 14.382c-.297-.149-1.758-.867-2.03-.967-.273-.099-.471-.148-.67.15-.197.297-.767.966-.94 1.164-.173.199-.347.223-.644.075-.297-.15-1.255-.463-2.39-1.475-.883-.788-1.48-1.761-1.653-2.059-.173-.297-.018-.458.13-.606.134-.133.298-.347.446-.52.149-.174.198-.298.298-.497.099-.198.05-.371-.025-.52-.075-.149-.669-1.612-.916-2.207-.242-.579-.487-.5-.669-.51-.173-.008-.371-.01-.57-.01-.198 0-.52.074-.792.372-.272.297-1.04 1.016-1.04 2.479 0 1.462 1.065 2.875 1.213 3.074.149.198 2.096 3.2 5.077 4.487.709.306 1.262.489 1.694.625.712.227 1.36.195 1.871.118.571-.085 1.758-.719 2.006-1.413.248-.694.248-1.289.173-1.413-.074-.124-.272-.198-.57-.347m-5.421 7.403h-.004a9.87 9.87 0 01-5.031-1.378l-.361-.214-3.741.982.998-3.648-.235-.374a9.86 9.86 0 01-1.51-5.26c.001-5.45 4.436-9.884 9.888-9.884 2.64 0 5.122 1.03 6.988 2.898a9.825 9.825 0 012.893 6.994c-.003 5.45-4.437 9.884-9.885 9.884m8.413-18.297A11.815 11.815 0 0012.05 0C5.495 0 .16 5.335.157 11.892c0 2.096.547 4.142 1.588 5.945L.057 24l6.305-1.654a11.882 11.882 0 005.683 1.448h.005c6.554 0 11.89-5.335 11.893-11.893a11.821 11.821 0 00-3.48-8.413Z"/></svg>{{else if eq . "email"}}<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><rect x="3" y="5" width="18" height="14" rx="2"/><path d="M3 7l9 6 9-6"/></svg>{{else if eq . "all"}}<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M12 3l9 5-9 5-9-5z"/><path d="M3 12l9 5 9-5"/><path d="M3 16l9 5 9-5"/></svg>{{else}}<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M4 5h16v11H10l-5 4v-4H4z"/></svg>{{end}}</span>{{end}}

{{define "state"}}{{if eq .State "live"}}<span class="state state--live" role="img" aria-label="{{.StateLabel}}" title="{{.StateLabel}}"></span>{{else if eq .State "needs-login"}}<span class="state state--lock" role="img" aria-label="{{.StateLabel}}" title="{{.StateLabel}}"><svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.4" stroke-linecap="round" stroke-linejoin="round"><rect x="5" y="11" width="14" height="9" rx="2"/><path d="M8 11V8a4 4 0 018 0v3"/></svg></span>{{else if eq .State "catching-up"}}<span class="state state--sync" role="img" aria-label="{{.StateLabel}}" title="{{.StateLabel}}"><svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.2" stroke-linecap="round" stroke-linejoin="round"><path d="M20 11a8 8 0 00-14.5-4.5L4 8"/><path d="M4 4v4h4"/><path d="M4 13a8 8 0 0014.5 4.5L20 16"/><path d="M20 20v-4h-4"/></svg></span>{{else if .State}}<span class="state state--off" role="img" aria-label="{{.StateLabel}}" title="{{.StateLabel}}"><svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.4" stroke-linecap="round" stroke-linejoin="round"><path d="M12 4l9 16H3z"/><path d="M12 10v4"/><path d="M12 17.5v.5"/></svg></span>{{end}}{{end}}

{{define "tile"}}<a class="tile{{if .Current}} tile--current{{end}}" href="{{.Href}}"{{if .Current}} aria-current="true"{{end}}>{{template "logo" .Logo}}<span class="tile__text"><span class="tile__name">{{.Name}}</span><span class="tile__sub">{{.Service}}</span></span>{{template "state" .}}</a>{{end}}

{{define "picker"}}<details class="picker" data-controller="picker">
<summary class="picker__button" aria-label="Source: {{.Current.Name}}. Choose another source">{{template "logo" .Current.Logo}}<span class="tile__text"><span class="tile__name">{{.Current.Name}}</span><span class="tile__sub">{{.Current.Service}}</span></span>{{template "state" .Current}}<svg class="picker__chevron" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M6 9l6 6 6-6"/></svg></summary>
<nav class="picker__panel" aria-label="Sources">
{{range .Groups}}<h2 class="picker__heading">{{.Title}}</h2>
<div class="picker__grid">{{range .Tiles}}{{template "tile" .}}{{end}}</div>
{{end}}<div class="picker__foot"><a href="/sources">Manage sources</a></div>
</nav>
</details>{{end}}

{{define "topbar"}}<header class="topbar">
{{template "brand"}}
{{if .Picker}}{{template "picker" .Picker}}{{end}}
<span class="topbar__space"></span>
<button class="icon-button" type="button" data-controller="theme" data-action="theme#toggle" aria-label="Switch between the light and dark theme"><svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M20 14.5A8 8 0 019.5 4a8 8 0 1010.5 10.5z"/></svg></button>
<form method="post" action="/logout"><input type="hidden" name="csrf" value="{{.CSRF}}"><button class="link-button" type="submit">Sign out</button></form>
</header>{{end}}

{{define "banner"}}{{if .Alert}}<div class="banner" role="alert"><span><strong>{{.Alert.Name}} is not archiving.</strong> {{.Alert.Detail}}</span><a class="button button--warn" href="/sources">Open sources</a></div>{{end}}{{end}}
```

`internal/web/templates/login.html`:

```html
{{define "login"}}{{template "head" .}}
<body>
<main class="plain">
{{template "brand"}}
<form class="card" method="post" action="/login">
{{if .Error}}<p class="alert" role="alert">{{.Error}}</p>{{end}}
<div class="field"><label for="password">Password</label><input id="password" name="password" type="password" autocomplete="current-password" required autofocus></div>
<button class="button" type="submit">Sign in</button>
</form>
</main>
</body>
</html>{{end}}
```

`internal/web/templates/setup.html`:

```html
{{define "setup"}}{{template "head" .}}
<body>
<main class="plain">
{{template "brand"}}
<form class="card" method="post" action="/setup">
<h1>Choose a password</h1>
<p>It protects everything arc5 archives. Use at least 8 characters.</p>
{{if .Error}}<p class="alert" role="alert">{{.Error}}</p>{{end}}
<div class="field"><label for="password">Password</label><input id="password" name="password" type="password" autocomplete="new-password" required autofocus></div>
<div class="field"><label for="password_again">The same password again</label><input id="password_again" name="password_again" type="password" autocomplete="new-password" required></div>
<button class="button" type="submit">Save password and sign in</button>
</form>
</main>
</body>
</html>{{end}}
```

`internal/web/templates/empty.html`:

```html
{{define "empty"}}{{template "head" .}}
<body class="app">
{{template "topbar" .}}
<main class="plain plain--wide">
<h1>No sources yet</h1>
<p>A source is an account arc5 keeps a copy of. Connecting one arrives with the next release.</p>
<p>To look around first, stop the server, run <span class="code">arc5 demo --data &lt;folder&gt;</span> and start it again on that folder.</p>
</main>
</body>
</html>{{end}}
```

- [ ] **Step 8: Write the server**

Replace `internal/web/server.go` with:

```go
// Package web serves arc5's interface.
package web

import (
	"bytes"
	"crypto/sha256"
	"crypto/subtle"
	"embed"
	"encoding/hex"
	"errors"
	"html/template"
	"io"
	"io/fs"
	"log"
	"net/http"
	"time"

	"github.com/fenelon/arc5/internal/archive"
	"github.com/fenelon/arc5/internal/connector"
)

//go:embed static templates
var files embed.FS

// csp lets a page load scripts, styles, fonts and media from arc5 only, and
// nothing inline. Message text can never run as code.
const csp = "default-src 'none'; script-src 'self'; style-src 'self'; img-src 'self'; media-src 'self'; font-src 'self'; connect-src 'self'; form-action 'self'; base-uri 'none'; frame-ancestors 'none'"

// Runner is what the interface needs from the connector runner.
type Runner interface {
	Status() []connector.Status
	RequestMedia(attachmentID int64) error
}

type Deps struct {
	Store       *archive.Store
	Runner      Runner
	Registry    *connector.Registry
	EnvPassword string           // ARC5_PASSWORD; empty lets the owner choose one
	Now         func() time.Time // defaults to time.Now
	Location    *time.Location   // times are shown in this zone; defaults to time.Local
}

type Server struct {
	deps    Deps
	auth    *auth
	tmpl    *template.Template
	static  fs.FS
	version string
	mux     *http.ServeMux
}

func New(deps Deps) (*Server, error) {
	if deps.Now == nil {
		deps.Now = time.Now
	}
	if deps.Location == nil {
		deps.Location = time.Local
	}
	s := &Server{deps: deps, auth: &auth{store: deps.Store, envPassword: deps.EnvPassword, now: deps.Now}}
	if err := s.auth.syncEnv(); err != nil {
		return nil, err
	}
	var err error
	if s.static, err = fs.Sub(files, "static"); err != nil {
		return nil, err
	}
	if s.version, err = fingerprint(s.static); err != nil {
		return nil, err
	}
	s.tmpl, err = template.New("").Funcs(template.FuncMap{"asset": s.asset, "dict": dict}).ParseFS(files, "templates/*.html")
	if err != nil {
		return nil, err
	}
	s.routes()
	return s, nil
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) { s.mux.ServeHTTP(w, r) }

func (s *Server) routes() {
	s.mux = http.NewServeMux()
	s.mux.HandleFunc("GET /healthz", s.health)
	s.mux.HandleFunc("GET /static/{version}/{path...}", s.serveStatic)
	s.mux.HandleFunc("GET /setup", s.setupForm)
	s.mux.HandleFunc("POST /setup", s.setup)
	s.mux.HandleFunc("GET /login", s.loginForm)
	s.mux.HandleFunc("POST /login", s.login)
	s.mux.Handle("POST /logout", s.signedIn(s.logout))
	s.mux.Handle("GET /{$}", s.signedIn(s.home))
}

// health is unauthenticated and deliberately says nothing about accounts.
func (s *Server) health(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	io.WriteString(w, `{"status":"ok"}`+"\n")
}

// fingerprint hashes every static file, so asset addresses change whenever
// their content does and browsers can cache them for good.
func fingerprint(static fs.FS) (string, error) {
	h := sha256.New()
	err := fs.WalkDir(static, ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		body, err := fs.ReadFile(static, path)
		if err != nil {
			return err
		}
		io.WriteString(h, path)
		h.Write(body)
		return nil
	})
	return hex.EncodeToString(h.Sum(nil))[:12], err
}

// asset is the address of a static file.
func (s *Server) asset(name string) string { return "/static/" + s.version + "/" + name }

func (s *Server) serveStatic(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("path")
	info, err := fs.Stat(s.static, name)
	if !fs.ValidPath(name) || err != nil || info.IsDir() {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	http.ServeFileFS(w, r, s.static, name)
}

// dict builds a map in a template, to hand a partial more than one value.
func dict(pairs ...any) (map[string]any, error) {
	if len(pairs)%2 != 0 {
		return nil, errors.New("dict needs pairs of key and value")
	}
	m := make(map[string]any, len(pairs)/2)
	for i := 0; i < len(pairs); i += 2 {
		key, ok := pairs[i].(string)
		if !ok {
			return nil, errors.New("dict keys must be strings")
		}
		m[key] = pairs[i+1]
	}
	return m, nil
}

// render builds the whole page before sending any of it, so a template error
// becomes a clean 500 and never half a page.
func (s *Server) render(w http.ResponseWriter, status int, name string, data any) {
	var buf bytes.Buffer
	if err := s.tmpl.ExecuteTemplate(&buf, name, data); err != nil {
		log.Printf("web: render %s: %v", name, err)
		http.Error(w, "arc5 could not build this page. The reason is in the server log.", http.StatusInternalServerError)
		return
	}
	h := w.Header()
	h.Set("Content-Type", "text/html; charset=utf-8")
	h.Set("Content-Security-Policy", csp)
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Referrer-Policy", "same-origin")
	h.Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	buf.WriteTo(w)
}

func (s *Server) fail(w http.ResponseWriter, err error) {
	log.Printf("web: %v", err)
	http.Error(w, "arc5 could not finish this request. The reason is in the server log.", http.StatusInternalServerError)
}

// theme is the theme the owner chose, from its cookie; empty follows the device.
func theme(r *http.Request) string {
	if c, err := r.Cookie("arc5_theme"); err == nil && (c.Value == "light" || c.Value == "dark") {
		return c.Value
	}
	return ""
}

// page starts the data every signed-in page needs.
func (s *Server) page(r *http.Request, sess archive.Session, title string) Page {
	return Page{Title: title, Theme: theme(r), CSRF: sess.CSRF}
}

func redirect(w http.ResponseWriter, r *http.Request, to string) {
	http.Redirect(w, r, to, http.StatusSeeOther)
}

// signedIn guards a handler: no password yet sends the browser to first-run
// setup, no session to the login page, and a POST without the session's CSRF
// token is refused.
func (s *Server) signedIn(next func(http.ResponseWriter, *http.Request, archive.Session)) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		has, err := s.auth.hasPassword()
		if err != nil {
			s.fail(w, err)
			return
		}
		if !has {
			redirect(w, r, "/setup")
			return
		}
		sess, ok := s.auth.session(r)
		if !ok {
			redirect(w, r, "/login")
			return
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			got := r.PostFormValue("csrf")
			if got == "" || subtle.ConstantTimeCompare([]byte(got), []byte(sess.CSRF)) != 1 {
				http.Error(w, "This form has expired. Go back, reload the page and try again.", http.StatusForbidden)
				return
			}
		}
		next(w, r, sess)
	})
}

func (s *Server) home(w http.ResponseWriter, r *http.Request, sess archive.Session) {
	accounts, err := s.deps.Store.Accounts()
	if err != nil {
		s.fail(w, err)
		return
	}
	if len(accounts) == 0 {
		s.render(w, http.StatusOK, "empty", s.page(r, sess, "Welcome"))
		return
	}
	redirect(w, r, "/s/chats")
}
```

`internal/web/auth_handlers.go`:

```go
package web

import (
	"errors"
	"fmt"
	"math"
	"net/http"

	"github.com/fenelon/arc5/internal/archive"
)

// formPage is the login or first-run page.
type formPage struct {
	Page
	Error string
}

func (s *Server) formPage(r *http.Request, title, problem string) formPage {
	return formPage{Page: Page{Title: title, Theme: theme(r)}, Error: problem}
}

// setupOpen reports whether first-run setup may still choose the password.
func (s *Server) setupOpen() (bool, error) {
	has, err := s.auth.hasPassword()
	return !has, err
}

func (s *Server) setupForm(w http.ResponseWriter, r *http.Request) {
	open, err := s.setupOpen()
	if err != nil {
		s.fail(w, err)
		return
	}
	if !open {
		redirect(w, r, "/login")
		return
	}
	s.render(w, http.StatusOK, "setup", s.formPage(r, "Choose a password", ""))
}

func (s *Server) setup(w http.ResponseWriter, r *http.Request) {
	open, err := s.setupOpen()
	if err != nil {
		s.fail(w, err)
		return
	}
	if !open {
		redirect(w, r, "/login")
		return
	}
	password := r.PostFormValue("password")
	problem := ""
	if password != r.PostFormValue("password_again") {
		problem = "The two passwords are not the same. Type the same one twice."
	} else if err := s.auth.setPassword(password); errors.Is(err, errShortPassword) {
		problem = "That password is too short. Use at least 8 characters."
	} else if err != nil {
		s.fail(w, err)
		return
	}
	if problem != "" {
		s.render(w, http.StatusUnprocessableEntity, "setup", s.formPage(r, "Choose a password", problem))
		return
	}
	if err := s.auth.startSession(w, r); err != nil {
		s.fail(w, err)
		return
	}
	redirect(w, r, "/")
}

func (s *Server) loginForm(w http.ResponseWriter, r *http.Request) {
	has, err := s.auth.hasPassword()
	if err != nil {
		s.fail(w, err)
		return
	}
	if !has {
		redirect(w, r, "/setup")
		return
	}
	if _, ok := s.auth.session(r); ok {
		redirect(w, r, "/")
		return
	}
	s.render(w, http.StatusOK, "login", s.formPage(r, "Sign in", ""))
}

func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	has, err := s.auth.hasPassword()
	if err != nil {
		s.fail(w, err)
		return
	}
	if !has {
		redirect(w, r, "/setup")
		return
	}
	if wait := s.auth.wait(); wait > 0 {
		seconds := int(math.Ceil(wait.Seconds()))
		s.render(w, http.StatusTooManyRequests, "login", s.formPage(r, "Sign in",
			fmt.Sprintf("Too many wrong passwords. Try again in %d seconds.", seconds)))
		return
	}
	ok, err := s.auth.check(r.PostFormValue("password"))
	if err != nil {
		s.fail(w, err)
		return
	}
	if !ok {
		s.auth.failed()
		s.render(w, http.StatusUnauthorized, "login", s.formPage(r, "Sign in", "That password is not right."))
		return
	}
	s.auth.succeeded()
	if err := s.auth.startSession(w, r); err != nil {
		s.fail(w, err)
		return
	}
	redirect(w, r, "/")
}

func (s *Server) logout(w http.ResponseWriter, r *http.Request, _ archive.Session) {
	s.auth.endSession(w, r)
	redirect(w, r, "/login")
}
```

- [ ] **Step 9: Keep the command building**

In `cmd/arc5/main.go`, replace

```go
	srv := &http.Server{Handler: web.NewHandler(), ReadHeaderTimeout: 10 * time.Second}
```

with

```go
	site, err := web.New(web.Deps{Store: store, Runner: runner, Registry: connector.Default})
	if err != nil {
		ln.Close()
		return err
	}
	srv := &http.Server{Handler: site, ReadHeaderTimeout: 10 * time.Second}
```

- [ ] **Step 10: Run the tests to verify they pass**

Run: `gofmt -l internal cmd; go vet ./... && go test -race -count=1 -timeout 180s ./...`
Expected: PASS.

- [ ] **Step 11: Commit**

```bash
git add internal/web cmd/arc5
git commit -m "Add the web shell: assets, themes, first-run setup and login"
```

---

### Task 6: Source picker, chat list and sources page

**Files:**
- Create: `internal/web/sources.go`, `internal/web/chats.go`, `internal/web/templates/source.html`, `internal/web/templates/sources.html`
- Modify: `internal/web/server.go` (routes)
- Test: `internal/web/chats_test.go`

**Interfaces:**
- Consumes: `Page`, `Picker`, `PickerGroup`, `Tile`, `Alert`, `(*Server).page`, `render`, `signedIn`, `fail`, `redirect`; `archive.Store` (`Accounts`, `ChatList`); `connector.Registry.Info`, `connector.Status`, run states and phases; test helpers `newSite`, `signIn`, `get`.
- Produces:
  - `type selection struct { Key, Name string; Display connector.Display; AccountIDs []int64 }` — `Key` is `"chats"`, `"mail"` or an account ID in decimal
  - `(*Server).sources() ([]source, error)`, `type source struct { Account archive.Account; Info connector.Info; Known bool; State, StateLabel, Detail string }`
  - `(*Server).selection(key string, all []source) (selection, bool)`
  - `(*Server).chrome(r *http.Request, sess archive.Session, title string, all []source, current string) Page` — a `Page` with the picker and alert filled in
  - `type ChatRow struct { Href, Title, Initials, Preview, When string; Current bool }`
  - `(*Server).chatRows(sel selection, currentChat int64, count int) (rows []ChatRow, more bool, err error)`
  - `sourcePage{ Page; Chats []ChatRow; MoreChatsHref string; Conversation *Conversation }` rendered by template `source` (`Conversation` is an empty placeholder type here; Task 7 replaces it)
  - routes `GET /s/{source}`, `GET /sources`
  - test helper `(*site).account(name, connectorName string) int64` and `(*site).say(acc int64, chat, id, text string, at time.Time, change ...func(*model.Message)) int64`

- [ ] **Step 1: Write the failing tests**

`internal/web/chats_test.go`:

```go
package web

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/fenelon/arc5/internal/connector"
	"github.com/fenelon/arc5/internal/model"
)

var noon = time.Date(2026, time.September, 28, 12, 0, 0, 0, time.UTC)

// account adds a source. Connector "telegram" is registered as a chats source
// the first time it is used; other names are left unregistered.
func (s *site) account(name, connectorName string) int64 {
	s.t.Helper()
	if _, known := s.reg.Info(connectorName); !known && connectorName == "telegram" {
		s.reg.Register(connector.Info{Name: "telegram", Title: "Telegram", Display: connector.DisplayChats}, nil)
	}
	id, err := s.store.CreateAccount(connectorName, name, nil)
	if err != nil {
		s.t.Fatal(err)
	}
	return id
}

// say stores a message, creating the chat as a private one titled like its
// source ID if it is new, and returns the message's database ID.
func (s *site) say(acc int64, chat, id, text string, at time.Time, change ...func(*model.Message)) int64 {
	s.t.Helper()
	if archived, _ := s.store.ChatArchived(acc, chat); !archived {
		if err := s.store.UpsertChat(acc, model.Chat{SourceID: chat, Kind: model.ChatPrivate, Title: chat}); err != nil {
			s.t.Fatal(err)
		}
	}
	m := model.Message{ChatSourceID: chat, SourceID: id, SenderSourceID: "them", SentAt: at, Text: text}
	for _, c := range change {
		c(&m)
	}
	if _, err := s.store.UpsertMessage(acc, m); err != nil {
		s.t.Fatal(err)
	}
	stored, err := s.store.Message(acc, chat, id)
	if err != nil {
		s.t.Fatal(err)
	}
	return stored.ID
}

func (s *site) chatID(acc int64, chat string) int64 {
	s.t.Helper()
	list, err := s.store.ChatList([]int64{acc}, 1000, 0)
	if err != nil {
		s.t.Fatal(err)
	}
	for _, c := range list {
		if c.Title == chat {
			return c.ID
		}
	}
	s.t.Fatalf("no chat titled %q", chat)
	return 0
}

func (s *site) page(path string) string {
	s.t.Helper()
	rec := s.get(path)
	if rec.Code != http.StatusOK {
		s.t.Fatalf("GET %s: status %d, body %s", path, rec.Code, rec.Body)
	}
	return rec.Body.String()
}

func has(t *testing.T, body string, wants ...string) {
	t.Helper()
	for _, want := range wants {
		if !strings.Contains(body, want) {
			t.Errorf("the page lacks %q", want)
		}
	}
}

func lacks(t *testing.T, body string, bans ...string) {
	t.Helper()
	for _, ban := range bans {
		if strings.Contains(body, ban) {
			t.Errorf("the page has %q", ban)
		}
	}
}

func TestHomeOpensTheChats(t *testing.T) {
	s := newSite(t, "")
	s.signIn()
	s.account("Personal", "telegram")
	if rec := s.get("/"); !redirectsTo(rec, "/s/chats") {
		t.Fatalf("home with a source: %d to %q", rec.Code, rec.Header().Get("Location"))
	}
}

func TestChatListShowsLastMessages(t *testing.T) {
	s := newSite(t, "")
	s.signIn()
	acc := s.account("Personal", "telegram")
	s.say(acc, "Dan Whitfield", "1", "Are we still on for Monday?", noon.Add(-48*time.Hour))
	s.say(acc, "Dan Whitfield", "2", "Got her. Traffic is bad.", noon, func(m *model.Message) { m.Outgoing = true })
	s.say(acc, "Marina Sokolova", "1", "Спасибо, что встретил!", noon.Add(-400*24*time.Hour))
	s.say(acc, "Photos only", "1", "", noon.Add(-time.Hour), func(m *model.Message) {
		m.Attachments = []model.Attachment{{Kind: model.AttachPhoto, FileName: "a.jpg", Size: 1}, {Kind: model.AttachPhoto, FileName: "b.jpg", Size: 1}}
	})
	s.srv.deps.Now = func() time.Time { return noon.Add(time.Hour) }

	body := s.page(fmt.Sprintf("/s/%d", acc))
	has(t, body, "Dan Whitfield", "You: Got her. Traffic is bad.", "Marina Sokolova", "Спасибо, что встретил!",
		"2 attachments", "Choose a chat on the left")
	// Today shows the time, this year the day, earlier years the year too.
	has(t, body, ">12:00<", ">24 Aug 2025<")
	if dan, marina := strings.Index(body, "Dan Whitfield"), strings.Index(body, "Marina Sokolova"); dan > marina {
		t.Error("the most recent chat is not first")
	}
	lacks(t, body, "Show more chats")
}

func TestChatListGrows(t *testing.T) {
	s := newSite(t, "")
	s.signIn()
	acc := s.account("Personal", "telegram")
	for i := range 101 {
		s.say(acc, fmt.Sprintf("chat-%03d", i), "1", "hello", noon.Add(time.Duration(i)*time.Minute))
	}
	first := s.page(fmt.Sprintf("/s/%d", acc))
	has(t, first, "chat-100", "chat-001", fmt.Sprintf(`href="/s/%d?chats=200"`, acc))
	lacks(t, first, "chat-000")
	all := s.page(fmt.Sprintf("/s/%d?chats=200", acc))
	has(t, all, "chat-000")
	lacks(t, all, "Show more chats")
	if rec := s.get(fmt.Sprintf("/s/%d?chats=banana", acc)); rec.Code != http.StatusOK {
		t.Errorf("a bad chats value: status %d", rec.Code)
	}
}

func TestPickerListsSourcesByKind(t *testing.T) {
	s := newSite(t, "")
	s.signIn()
	personal := s.account("Personal", "telegram")
	work := s.account("Work", "telegram")
	s.reg.Register(connector.Info{Name: "imap", Title: "Email", Display: connector.DisplayMail}, nil)
	s.account("Home mail", "imap")
	s.say(personal, "Dan", "1", "in personal", noon)
	s.say(work, "Boss", "1", "in work", noon)
	s.runner.status = []connector.Status{
		{AccountID: personal, State: connector.StateRunning, Progress: connector.Progress{Phase: connector.PhaseLive}},
		{AccountID: work, State: connector.StateNeedsLogin, LastError: "session revoked"},
	}

	body := s.page(fmt.Sprintf("/s/%d", personal))
	has(t, body, `<h2 class="picker__heading">Messengers</h2>`, `<h2 class="picker__heading">Mail</h2>`,
		"Everything at once", "2 messengers", "1 mailbox", "Personal", "Work", "Home mail",
		`aria-label="Live"`, `aria-label="Needs login"`, `href="/sources"`,
		fmt.Sprintf(`class="tile tile--current" href="/s/%d"`, personal), "logo--telegram", "logo--email",
		"<strong>Work is not archiving.</strong>")
	has(t, body, "in personal")
	lacks(t, body, "in work")

	everything := s.page("/s/chats")
	has(t, everything, "in personal", "in work", `class="tile tile--current" href="/s/chats"`)
}

func TestSourceStates(t *testing.T) {
	s := newSite(t, "")
	s.signIn()
	ids := map[string]int64{}
	for _, name := range []string{"Live", "Backfill", "Connecting", "Backoff", "Paused", "Off", "Unknown"} {
		ids[name] = s.account(name, "telegram")
	}
	if err := s.store.SetAccountEnabled(ids["Off"], false); err != nil {
		t.Fatal(err)
	}
	s.runner.status = []connector.Status{
		{AccountID: ids["Live"], State: connector.StateRunning, Progress: connector.Progress{Phase: connector.PhaseLive}},
		{AccountID: ids["Backfill"], State: connector.StateRunning, Progress: connector.Progress{Phase: connector.PhaseBackfill, ChatsDone: 3, ChatsTotal: 9}},
		{AccountID: ids["Connecting"], State: connector.StateRunning},
		{AccountID: ids["Backoff"], State: connector.StateBackoff, LastError: "connection reset"},
		{AccountID: ids["Paused"], State: connector.StatePaused, LastError: "disk full"},
	}
	body := s.page("/sources")
	has(t, body, "Catching up on history, 3 of 9 chats", "Connecting", "Not connected: connection reset",
		"Paused, the archive could not be written: disk full", "Switched off", "Not running",
		`aria-label="Live"`, `aria-label="Catching up on history"`)
	if strings.Count(body, "tile--warn") != 2 {
		t.Errorf("want the two failing sources drawn with the attention border, got %d", strings.Count(body, "tile--warn"))
	}
}

func TestUnknownSourceIs404(t *testing.T) {
	s := newSite(t, "")
	s.signIn()
	s.account("Personal", "telegram")
	for _, path := range []string{"/s/999", "/s/banana", "/s/-1", "/s/mail"} {
		if rec := s.get(path); rec.Code != http.StatusNotFound {
			t.Errorf("GET %s: status %d, want 404", path, rec.Code)
		}
	}
}

func TestSourceOfAnUnregisteredConnectorStillOpens(t *testing.T) {
	s := newSite(t, "")
	s.signIn()
	acc := s.account("Old import", "retired")
	s.say(acc, "Someone", "1", "still readable", noon)
	body := s.page(fmt.Sprintf("/s/%d", acc))
	has(t, body, "still readable", "Old import", "retired", "logo--generic")
}

func TestSourcePagesNeedASession(t *testing.T) {
	s := newSite(t, "")
	s.signIn()
	s.account("Personal", "telegram")
	stranger := &site{t: t, store: s.store, srv: s.srv, cookies: map[string]*http.Cookie{}}
	for _, path := range []string{"/s/chats", "/sources"} {
		if rec := stranger.get(path); !redirectsTo(rec, "/login") {
			t.Errorf("GET %s without a session: %d", path, rec.Code)
		}
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/web/`
Expected: FAIL: `/s/chats`, `/s/{id}` and `/sources` answer 404.

- [ ] **Step 3: Write the source model**

`internal/web/sources.go`:

```go
package web

import (
	"fmt"
	"net/http"
	"strconv"

	"github.com/fenelon/arc5/internal/archive"
	"github.com/fenelon/arc5/internal/connector"
)

const (
	stateLive       = "live"
	stateCatchingUp = "catching-up"
	stateNeedsLogin = "needs-login"
	stateOffline    = "offline"
)

// source is one account with what its connector and the runner say about it.
type source struct {
	Account    archive.Account
	Info       connector.Info
	Known      bool // its connector is built into this binary
	State      string
	StateLabel string
	Detail     string // StateLabel with the reason, for the Sources page
	Failing    bool   // enabled, and not archiving because something went wrong
}

func (s *Server) sources() ([]source, error) {
	accounts, err := s.deps.Store.Accounts()
	if err != nil {
		return nil, err
	}
	status := map[int64]connector.Status{}
	for _, st := range s.deps.Runner.Status() {
		status[st.AccountID] = st
	}
	out := make([]source, 0, len(accounts))
	for _, acc := range accounts {
		src := source{Account: acc}
		src.Info, src.Known = s.deps.Registry.Info(acc.Connector)
		if !src.Known {
			// An account whose connector is gone is still an archive to read.
			src.Info = connector.Info{Name: acc.Connector, Title: acc.Connector, Display: connector.DisplayChats}
		}
		st, running := status[acc.ID]
		describe(&src, st, running)
		out = append(out, src)
	}
	return out, nil
}

// describe turns the runner's status into a state mark and a sentence.
func describe(src *source, st connector.Status, running bool) {
	set := func(state, label string) { src.State, src.StateLabel, src.Detail = state, label, label }
	switch {
	case !src.Account.Enabled:
		set(stateOffline, "Switched off")
	case !running, st.State == connector.StateStopped:
		set(stateOffline, "Not running")
	case st.State == connector.StateNeedsLogin:
		set(stateNeedsLogin, "Needs login")
		src.Failing = true
	case st.State == connector.StatePaused:
		set(stateOffline, "Paused, the archive could not be written")
		src.Failing = true
	case st.State == connector.StateRunning && st.Progress.Phase == connector.PhaseLive:
		set(stateLive, "Live")
	case st.State == connector.StateRunning && st.Progress.Phase == connector.PhaseBackfill:
		set(stateCatchingUp, "Catching up on history")
		if st.Progress.ChatsTotal > 0 {
			src.Detail = fmt.Sprintf("Catching up on history, %d of %d chats", st.Progress.ChatsDone, st.Progress.ChatsTotal)
		}
	case st.State == connector.StateRunning && st.Progress.Phase == connector.PhaseRateLimited:
		set(stateCatchingUp, "Waiting, the source asked us to slow down")
	case st.State == connector.StateRunning:
		set(stateOffline, "Connecting")
	default:
		set(stateOffline, "Not connected")
		src.Failing = true
	}
	if st.LastError != "" && src.State != stateLive {
		src.Detail = src.StateLabel + ": " + st.LastError
	}
}

// logoFor is the logo drawn for a connector.
func logoFor(name string) string {
	switch name {
	case "telegram", "whatsapp":
		return name
	case "imap", "email":
		return "email"
	}
	return "generic"
}

func (src source) key() string { return strconv.FormatInt(src.Account.ID, 10) }

func (src source) tile(current string) Tile {
	return Tile{
		Name: src.Account.Label, Service: src.Info.Title, Logo: logoFor(src.Info.Name),
		State: src.State, StateLabel: src.StateLabel, Detail: src.Detail,
		Href: "/s/" + src.key(), Current: src.key() == current, Warn: src.Failing,
	}
}

// selection is what the picker has chosen: one source, or all of one kind.
type selection struct {
	Key        string // "chats", "mail", or an account ID
	Name       string
	Display    connector.Display
	AccountIDs []int64
}

var kinds = []struct {
	display connector.Display
	key     string
	title   string
	one     string
	many    string
}{
	{connector.DisplayChats, "chats", "Messengers", "messenger", "messengers"},
	{connector.DisplayMail, "mail", "Mail", "mailbox", "mailboxes"},
}

func (s *Server) selection(key string, all []source) (selection, bool) {
	for _, k := range kinds {
		if key != k.key {
			continue
		}
		sel := selection{Key: key, Name: "Everything at once", Display: k.display}
		for _, src := range all {
			if src.Info.Display == k.display {
				sel.AccountIDs = append(sel.AccountIDs, src.Account.ID)
			}
		}
		return sel, len(sel.AccountIDs) > 0
	}
	for _, src := range all {
		if src.key() == key {
			return selection{Key: key, Name: src.Account.Label, Display: src.Info.Display, AccountIDs: []int64{src.Account.ID}}, true
		}
	}
	return selection{}, false
}

// chrome is a Page with the picker and the outage alert filled in. current is
// the selection key the picker should mark, or "" for none.
func (s *Server) chrome(r *http.Request, sess archive.Session, title string, all []source, current string) Page {
	p := s.page(r, sess, title)
	if len(all) == 0 {
		return p
	}
	picker := &Picker{}
	for _, k := range kinds {
		var tiles []Tile
		for _, src := range all {
			if src.Info.Display == k.display {
				tiles = append(tiles, src.tile(current))
			}
		}
		if len(tiles) == 0 {
			continue
		}
		count := fmt.Sprintf("%d %s", len(tiles), k.many)
		if len(tiles) == 1 {
			count = "1 " + k.one
		}
		everything := Tile{Name: "Everything at once", Service: count, Logo: "all", Href: "/s/" + k.key, Current: current == k.key}
		picker.Groups = append(picker.Groups, PickerGroup{Title: k.title, Tiles: append([]Tile{everything}, tiles...)})
		for _, t := range append([]Tile{everything}, tiles...) {
			if t.Current {
				picker.Current = t
			}
		}
	}
	if picker.Current.Name == "" {
		picker.Current = Tile{Name: "Sources", Service: "Choose one", Logo: "all"}
	}
	p.Picker = picker
	for _, src := range all {
		if src.Failing {
			p.Alert = &Alert{Name: src.Account.Label, Detail: src.Detail + ". New messages are not being saved, and ones that auto-delete cannot be recovered."}
			break
		}
	}
	return p
}

// sourcesPage lists every source with its state.
type sourcesPage struct {
	Page
	Groups []PickerGroup
}

func (s *Server) sourcesList(w http.ResponseWriter, r *http.Request, sess archive.Session) {
	all, err := s.sources()
	if err != nil {
		s.fail(w, err)
		return
	}
	data := sourcesPage{Page: s.chrome(r, sess, "Sources", all, "")}
	for _, k := range kinds {
		group := PickerGroup{Title: k.title}
		for _, src := range all {
			if src.Info.Display == k.display {
				group.Tiles = append(group.Tiles, src.tile(""))
			}
		}
		if len(group.Tiles) > 0 {
			data.Groups = append(data.Groups, group)
		}
	}
	s.render(w, http.StatusOK, "sources", data)
}
```

- [ ] **Step 4: Write the chat list**

`internal/web/chats.go`:

```go
package web

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/fenelon/arc5/internal/archive"
	"github.com/fenelon/arc5/internal/connector"
	"github.com/fenelon/arc5/internal/model"
)

const (
	chatsPerPage = 100
	maxChatRows  = 5000
	previewRunes = 120
)

// ChatRow is one chat in the list.
type ChatRow struct {
	Href     string
	Title    string
	Initials string
	Preview  string
	When     string
	Current  bool
}

type sourcePage struct {
	Page
	Chats         []ChatRow
	MoreChatsHref string
	Conversation  *Conversation
}

// Conversation is the open chat. Task 7 fills it in.
type Conversation struct{}

// initials are up to two letters that stand for a name in its avatar.
func initials(name string) string {
	var out []rune
	for _, word := range strings.Fields(name) {
		r, _ := utf8.DecodeRuneInString(word)
		if unicode.IsLetter(r) || unicode.IsNumber(r) {
			out = append(out, unicode.ToUpper(r))
		}
		if len(out) == 2 {
			break
		}
	}
	if len(out) == 0 {
		return "?"
	}
	return string(out)
}

// preview is one line that stands for a chat's last message.
func preview(c archive.ChatSummary) string {
	text := strings.Join(strings.Fields(c.LastText), " ")
	if text == "" {
		switch {
		case c.LastFiles == 1:
			text = "1 attachment"
		case c.LastFiles > 1:
			text = fmt.Sprintf("%d attachments", c.LastFiles)
		}
	}
	if utf8.RuneCountInString(text) > previewRunes {
		text = string([]rune(text)[:previewRunes]) + "…"
	}
	switch {
	case c.LastKind == model.KindService:
		return text
	case c.LastOutgoing:
		return "You: " + text
	case c.Kind != model.ChatPrivate && c.LastSender != "":
		return c.LastSender + ": " + text
	}
	return text
}

// when shows a moment briefly: the time today, the day this year, and the
// year as well before that.
func (s *Server) when(t time.Time) string {
	t, now := t.In(s.deps.Location), s.deps.Now().In(s.deps.Location)
	switch {
	case t.Year() == now.Year() && t.YearDay() == now.YearDay():
		return t.Format("15:04")
	case t.Year() == now.Year():
		return t.Format("2 Jan")
	}
	return t.Format("2 Jan 2006")
}

// chatRows reads the first count chats of a selection.
func (s *Server) chatRows(sel selection, currentChat int64, count int) ([]ChatRow, bool, error) {
	list, err := s.deps.Store.ChatList(sel.AccountIDs, count+1, 0)
	if err != nil {
		return nil, false, err
	}
	more := len(list) > count
	if more {
		list = list[:count]
	}
	rows := make([]ChatRow, len(list))
	for i, c := range list {
		rows[i] = ChatRow{
			Href: fmt.Sprintf("/s/%s/c/%d", sel.Key, c.ID), Title: c.Title, Initials: initials(c.Title),
			Preview: preview(c), When: s.when(c.LastAt), Current: c.ID == currentChat,
		}
	}
	return rows, more, nil
}

// chatCount reads how many chats the list should show, from the "chats" parameter.
func chatCount(r *http.Request) int {
	n, err := strconv.Atoi(r.URL.Query().Get("chats"))
	if err != nil || n < chatsPerPage {
		return chatsPerPage
	}
	return min(n, maxChatRows)
}

// sourceShell loads what every page of a source shows: the picker and the
// chat list. It answers the request itself when the source is unknown.
func (s *Server) sourceShell(w http.ResponseWriter, r *http.Request, sess archive.Session, currentChat int64) (sourcePage, selection, bool) {
	all, err := s.sources()
	if err != nil {
		s.fail(w, err)
		return sourcePage{}, selection{}, false
	}
	sel, ok := s.selection(r.PathValue("source"), all)
	if !ok {
		http.NotFound(w, r)
		return sourcePage{}, selection{}, false
	}
	if sel.Display != connector.DisplayChats {
		http.Error(w, "This source is shown as mail, which this version of arc5 cannot display yet.", http.StatusNotImplemented)
		return sourcePage{}, selection{}, false
	}
	count := chatCount(r)
	rows, more, err := s.chatRows(sel, currentChat, count)
	if err != nil {
		s.fail(w, err)
		return sourcePage{}, selection{}, false
	}
	data := sourcePage{Page: s.chrome(r, sess, sel.Name, all, sel.Key), Chats: rows}
	if more {
		data.MoreChatsHref = fmt.Sprintf("%s?chats=%d", r.URL.Path, count+chatsPerPage)
	}
	return data, sel, true
}

func (s *Server) sourceView(w http.ResponseWriter, r *http.Request, sess archive.Session) {
	data, _, ok := s.sourceShell(w, r, sess, 0)
	if !ok {
		return
	}
	s.render(w, http.StatusOK, "source", data)
}
```

- [ ] **Step 5: Write the templates**

`internal/web/templates/source.html`:

```html
{{define "source"}}{{template "head" .}}
<body class="app">
{{template "topbar" .}}
{{template "banner" .}}
<div class="shell{{if .Conversation}} shell--open{{end}}">
<aside class="list" aria-label="Chats">
{{range .Chats}}<a class="row{{if .Current}} row--current{{end}}" href="{{.Href}}"{{if .Current}} aria-current="true"{{end}}><span class="avatar" aria-hidden="true">{{.Initials}}</span><span class="row__body"><span class="row__top"><span class="row__name">{{.Title}}</span><span class="row__when">{{.When}}</span></span><span class="row__preview">{{.Preview}}</span></span></a>
{{else}}<p class="empty">Nothing has been archived here yet.</p>
{{end}}{{if .MoreChatsHref}}<a class="more" href="{{.MoreChatsHref}}">Show more chats</a>{{end}}
</aside>
<section class="conversation" aria-label="Conversation">
<p class="empty empty--center">Choose a chat on the left to read it.</p>
</section>
</div>
</body>
</html>{{end}}
```

`internal/web/templates/sources.html`:

```html
{{define "sources"}}{{template "head" .}}
<body class="app">
{{template "topbar" .}}
{{template "banner" .}}
<main class="plain plain--wide">
<h1>Sources</h1>
<p>Each source is one account arc5 keeps a copy of. Its type decides how it is shown: messengers as chats, mailboxes as mail.</p>
{{range .Groups}}<h2 class="group-heading">{{.Title}}</h2>
<div class="tiles">
{{range .Tiles}}<a class="tile tile--wide{{if .Warn}} tile--warn{{end}}" href="{{.Href}}">{{template "logo" .Logo}}<span class="tile__text"><span class="tile__name">{{.Name}}</span><span class="tile__sub">{{.Service}}. {{.Detail}}</span></span>{{template "state" .}}</a>
{{end}}</div>
{{else}}<p class="empty">No sources yet.</p>
{{end}}
</main>
</body>
</html>{{end}}
```

- [ ] **Step 6: Add the routes**

In `internal/web/server.go`, add to `routes` after the `GET /{$}` line:

```go
	s.mux.Handle("GET /s/{source}", s.signedIn(s.sourceView))
	s.mux.Handle("GET /sources", s.signedIn(s.sourcesList))
```

- [ ] **Step 7: Run the tests to verify they pass**

Run: `gofmt -l internal cmd; go vet ./... && go test -race -count=1 -timeout 180s ./...`
Expected: PASS.

- [ ] **Step 8: Commit**

```bash
git add internal/web
git commit -m "Add the source picker, the chat list and a sources overview"
```

---

### Task 7: Conversation view

**Files:**
- Create: `internal/web/conversation.go`, `internal/web/templates/conversation.html`
- Modify: `internal/web/chats.go` (remove the placeholder `Conversation` type), `internal/web/templates/source.html`, `internal/web/server.go` (route)
- Test: `internal/web/conversation_test.go`

**Interfaces:**
- Consumes: `sourceShell`, `selection`, `sourcePage`, `(*Server).when`; `archive.Store` (`Chat`, `ChatMessages`, `ChatQuarters`, `Versions`); `archive.Anchor`, `archive.Window`, `archive.MessageView`, `archive.StoredAttachment`; test helpers from Tasks 5 and 6.
- Produces:
  - `type Conversation struct { Title, Subtitle, BackHref, SelfHref, OlderHref, NewerHref string; Days []Day; Years []RailYear; YearOptions []YearOption }`
  - `type Day struct { Label string; Messages []Message }`
  - `type Message struct { Anchor string; Service, Outgoing, Target bool; Sender, ForwardedFrom, Text, Time, Edited, Deleted string; Versions []Version; Attachments []Attachment }`
  - `type Version struct { Text, Until string }`
  - `type Attachment struct { ID int64; Mode, Name, Detail, URL string; Ephemeral bool }` — `Mode` is `image`, `audio`, `video`, `file`, `skipped`, `pending`, `failed` or `unavailable`
  - `type RailYear struct { Label string; Now bool; Bands []RailBand }`, `type RailBand struct { Href, Label string; Level int; Now bool }`, `type YearOption struct { Value, Label string; Current bool }`
  - `humanBytes(n int64) string`, `inlineKind(mimeType string) string` (used again in Task 8)
  - templates `conversation`, `message`, `attachment`; route `GET /s/{source}/c/{chat}` with optional `before`, `after`, `at`, `date=YYYY-MM-DD`

- [ ] **Step 1: Write the failing tests**

`internal/web/conversation_test.go`:

```go
package web

import (
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/fenelon/arc5/internal/model"
)

// talk opens a site with one source and one chat, "Dan Whitfield", holding n
// messages a minute apart ending at noon, and returns the chat's page address.
func talk(t *testing.T, n int) (*site, int64, string, []int64) {
	t.Helper()
	s := newSite(t, "")
	s.signIn()
	s.srv.deps.Now = func() time.Time { return noon.Add(time.Hour) }
	acc := s.account("Personal", "telegram")
	ids := make([]int64, n)
	for i := range n {
		at := noon.Add(-time.Duration(n-1-i) * time.Minute)
		ids[i] = s.say(acc, "Dan Whitfield", fmt.Sprint(i+1), fmt.Sprintf("message number %d", i+1), at)
	}
	if n == 0 {
		return s, acc, "", nil
	}
	return s, acc, fmt.Sprintf("/s/%d/c/%d", acc, s.chatID(acc, "Dan Whitfield")), ids
}

func TestConversationShowsTheNewestMessages(t *testing.T) {
	s, acc, path, ids := talk(t, 70)
	body := s.page(path)
	has(t, body, "<h1>Dan Whitfield</h1>", "Private chat on Telegram, 70 messages since September 2026",
		"message number 70", "message number 11", `<h2 class="day"><span>Monday, 28 September 2026</span></h2>`,
		fmt.Sprintf(`href="%s?before=%d"`, path, ids[10]), "Earlier messages",
		`class="row row--current"`, `<div class="shell shell--open">`, fmt.Sprintf(`href="/s/%d"`, acc))
	lacks(t, body, "message number 10<", "Later messages", "Choose a chat on the left")
}

func TestConversationPaging(t *testing.T) {
	s, _, path, ids := talk(t, 70)

	older := s.page(fmt.Sprintf("%s?before=%d", path, ids[10]))
	has(t, older, "message number 10", "message number 1<", fmt.Sprintf(`href="%s?after=%d"`, path, ids[9]), "Later messages")
	lacks(t, older, "Earlier messages", "message number 11<")

	around := s.page(fmt.Sprintf("%s?at=%d", path, ids[34]))
	has(t, around, "message number 35", fmt.Sprintf(`id="m%d"`, ids[34]), "msg--target", "Earlier messages", "Later messages")
	if strings.Count(around, "msg--target") != 1 {
		t.Error("more than one message is marked as the target")
	}

	byDate := s.page(path + "?date=2026-09-28")
	has(t, byDate, "message number 1<")
}

// Review Focus 4: paging edges.
func TestConversationEdges(t *testing.T) {
	s, acc, path, _ := talk(t, 1)
	other := s.say(acc, "Someone else", "1", "in another chat", noon)

	for name, query := range map[string]string{
		"another chat's message": fmt.Sprintf("?at=%d", other),
		"a missing message":      "?before=987654",
		"a later missing one":    "?after=987654",
		"nonsense":               "?at=banana&before=-3",
		"a bad date":             "?date=yesterday",
		"a date far ahead":       "?date=2999-01-01",
	} {
		body := s.page(path + query)
		if !strings.Contains(body, "message number 1") || strings.Count(body, `class="msg__text"`) != 1 {
			t.Errorf("%s: want the chat's one message and nothing else", name)
		}
	}

	empty := s.account("Second", "telegram")
	if err := s.store.UpsertChat(empty, model.Chat{SourceID: "quiet", Kind: model.ChatPrivate, Title: "Quiet"}); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{"/s/chats/c/999999", "/s/chats/c/banana", fmt.Sprintf("/s/%d/c/%d", empty, s.chatID(acc, "Dan Whitfield"))} {
		if rec := s.get(bad); rec.Code != http.StatusNotFound {
			t.Errorf("GET %s: status %d, want 404", bad, rec.Code)
		}
	}
}

// Review Focus 1: hostile content appears as text.
func TestHostileContentIsShownAsText(t *testing.T) {
	s := newSite(t, "")
	s.signIn()
	acc := s.account(`<b>Personal</b>`, "telegram")
	const attack = `<script>alert(1)</script><img src=x onerror=alert(2)>"'`
	if err := s.store.UpsertChat(acc, model.Chat{SourceID: "c", Kind: model.ChatGroup, Title: attack}); err != nil {
		t.Fatal(err)
	}
	if err := s.store.UpsertSender(acc, model.Sender{SourceID: "evil", DisplayName: attack}); err != nil {
		t.Fatal(err)
	}
	s.say(acc, "c", "1", attack, noon, func(m *model.Message) {
		m.SenderSourceID, m.ForwardedFrom = "evil", attack
		m.Attachments = []model.Attachment{{Kind: model.AttachDocument, FileName: attack, Size: 99999999, NoFetch: true, NoFetchReason: attack}}
	})
	s.say(acc, "c", "1", attack+" edited", noon, func(m *model.Message) {
		m.SenderSourceID, m.EditedAt = "evil", noon.Add(time.Minute)
	})

	list, err := s.store.ChatList([]int64{acc}, 10, 0)
	if err != nil || len(list) != 1 {
		t.Fatal(err)
	}
	for _, path := range []string{fmt.Sprintf("/s/%d", acc), fmt.Sprintf("/s/%d/c/%d", acc, list[0].ID), "/sources"} {
		body := s.page(path)
		lacks(t, body, "<script>alert", "<img src=x", `onerror=alert(2)>"`, "<b>Personal</b>")
		has(t, body, "&lt;b&gt;Personal&lt;/b&gt;")
	}
	has(t, s.page(fmt.Sprintf("/s/%d/c/%d", acc, list[0].ID)), "&lt;script&gt;alert(1)&lt;/script&gt;")
}

func TestMessageDetails(t *testing.T) {
	s := newSite(t, "")
	s.signIn()
	acc := s.account("Personal", "telegram")
	if err := s.store.UpsertChat(acc, model.Chat{SourceID: "g", Kind: model.ChatGroup, Title: "Flat"}); err != nil {
		t.Fatal(err)
	}
	if err := s.store.UpsertSender(acc, model.Sender{SourceID: "dan", DisplayName: "Dan"}); err != nil {
		t.Fatal(err)
	}
	from := func(m *model.Message) { m.SenderSourceID = "dan" }

	s.say(acc, "g", "1", "Room 2 from ten.", noon, from)
	s.say(acc, "g", "1", "Room 4 from ten.", noon, from, func(m *model.Message) { m.EditedAt = noon.Add(4 * time.Minute) })
	s.say(acc, "g", "2", "Deleted later", noon.Add(time.Minute), from, func(m *model.Message) { m.ForwardedFrom = "Priya" })
	if err := s.store.MarkDeleted(acc, "g", []string{"2"}); err != nil {
		t.Fatal(err)
	}
	s.say(acc, "g", "3", "On my way", noon.Add(2*time.Minute), func(m *model.Message) { m.Outgoing, m.SenderSourceID = true, "" })
	s.say(acc, "g", "4", "Dan pinned a message", noon.Add(3*time.Minute), func(m *model.Message) { m.Kind, m.SenderSourceID = model.KindService, "" })
	s.say(acc, "g", "5", "line one\nline two", noon.Add(24*time.Hour), from)

	body := s.page(fmt.Sprintf("/s/%d/c/%d", acc, s.chatID(acc, "Flat")))
	has(t, body, "Group on Telegram, 5 messages since September 2026",
		`<div class="msg__sender">Dan</div>`, "Room 4 from ten.", "Edited at 12:04, show earlier", "Room 2 from ten.",
		"Forwarded from Priya", "Deleted at source on", `class="msg msg--out"`, "On my way",
		`<p class="service"`, "Dan pinned a message", "line one\nline two",
		"Monday, 28 September 2026", "Tuesday, 29 September 2026")
	if strings.Count(body, `<div class="msg__sender">`) != 3 {
		t.Errorf("sender shown %d times; want it on Dan's three messages and not on the owner's", strings.Count(body, `<div class="msg__sender">`))
	}
}

func TestAttachmentModes(t *testing.T) {
	s := newSite(t, "")
	s.signIn()
	acc := s.account("Personal", "telegram")
	if err := s.store.SetMediaCap(1000); err != nil {
		t.Fatal(err)
	}
	s.say(acc, "Files", "1", "", noon, func(m *model.Message) {
		m.Attachments = []model.Attachment{
			{Kind: model.AttachPhoto, FileName: "plan.jpg", MimeType: "image/jpeg", Size: 5},
			{Kind: model.AttachVoice, FileName: "note.ogg", MimeType: "audio/ogg", Size: 5},
			{Kind: model.AttachVideo, FileName: "clip.mp4", MimeType: "video/mp4", Size: 5},
			{Kind: model.AttachDocument, FileName: "a.pdf", MimeType: "application/pdf", Size: 5},
			{Kind: model.AttachDocument, FileName: "page.html", MimeType: "text/html", Size: 5},
			{Kind: model.AttachVideo, FileName: "big.mp4", MimeType: "video/mp4", Size: 3 * 1024 * 1024},
			{Kind: model.AttachPhoto, FileName: "once.jpg", MimeType: "image/jpeg", Size: 5, Ephemeral: true, NoFetch: true, NoFetchReason: "self-destructing media is not archived"},
			{Kind: model.AttachDocument, FileName: "waiting.bin", Size: 5},
			{Kind: model.AttachDocument, FileName: "broken.bin", Size: 5},
			{Kind: model.AttachDocument, FileName: "gone.bin", Size: 5},
		}
	})
	ref := func(i int) model.AttachmentRef {
		return model.AttachmentRef{ChatSourceID: "Files", MessageSourceID: "1", Position: i}
	}
	for i := range 5 {
		if err := s.store.PutMedia(acc, ref(i), strings.NewReader("hello")); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.store.SetAttachmentStatus(acc, ref(8), model.StatusFailed, "timed out"); err != nil {
		t.Fatal(err)
	}
	if err := s.store.SetAttachmentStatus(acc, ref(9), model.StatusUnavailable, ""); err != nil {
		t.Fatal(err)
	}

	body := s.page(fmt.Sprintf("/s/%d/c/%d", acc, s.chatID(acc, "Files")))
	has(t, body,
		`<img class="media media--image"`, `alt="plan.jpg"`,
		`<audio class="media" controls preload="none"`, `<video class="media" controls preload="none"`,
		"a.pdf", "5 B", ">Download<", "page.html",
		"big.mp4", "3.0 MB, not downloaded: over size cap", ">Fetch now<",
		"once.jpg", "Sent as view-once", "self-destructing media is not archived",
		"waiting.bin", "Waiting to be downloaded",
		"broken.bin", "The download failed: timed out", ">Try again<",
		"gone.bin", "No longer available at the source")
	// HTML is never offered inline, only as a download.
	if strings.Count(body, "<img ") != 1 {
		t.Error("something other than the one photo is drawn as an image")
	}
	if got := strings.Count(body, `name="csrf"`); got != 4 {
		t.Errorf("%d CSRF fields; want the sign-out form plus three fetch forms", got)
	}
}

func TestYearRail(t *testing.T) {
	s := newSite(t, "")
	s.signIn()
	acc := s.account("Personal", "telegram")
	n := 0
	at := func(year int, month time.Month, count int) {
		for range count {
			n++
			s.say(acc, "Long", fmt.Sprint(n), "x", time.Date(year, month, 10, 12, 0, n%60, 0, time.UTC))
		}
	}
	at(2024, time.February, 1)
	at(2025, time.May, 4)
	at(2026, time.August, 8)
	path := fmt.Sprintf("/s/%d/c/%d", acc, s.chatID(acc, "Long"))

	body := s.page(path)
	has(t, body, `<nav class="rail" aria-label="Jump to a quarter">`,
		`<span class="rail__label">2026</span>`, `<span class="rail__label">2025</span>`, `<span class="rail__label">2024</span>`,
		fmt.Sprintf(`href="%s?date=2026-07-01"`, path), fmt.Sprintf(`href="%s?date=2024-01-01"`, path),
		`aria-label="July to September 2026, 8 messages"`, `class="band band--4 band--now"`,
		`aria-label="April to June 2025, 4 messages"`, `class="band band--2"`,
		`aria-label="January to March 2024, 1 message"`, `class="band band--1"`,
		`<option value="2026-01-01" selected>2026</option>`, `<option value="2024-01-01">2024</option>`)
	if got := strings.Count(body, `class="band`); got != 12 {
		t.Errorf("%d bands, want four for each of three years", got)
	}

	old := s.page(path + "?date=2024-01-01")
	has(t, old, `aria-label="January to March 2024, 1 message"`)
	if !strings.Contains(old, `<div class="rail__year rail__year--now"><span class="rail__label">2024</span>`) {
		t.Error("the rail does not mark 2024 as the year being read")
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/web/`
Expected: FAIL to build with `undefined` fields on `Conversation`, or 404 on `/s/{source}/c/{chat}`.

- [ ] **Step 3: Write the view model**

In `internal/web/chats.go`, delete these two lines:

```go
// Conversation is the open chat. Task 7 fills it in.
type Conversation struct{}
```

`internal/web/conversation.go`:

```go
package web

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/fenelon/arc5/internal/archive"
	"github.com/fenelon/arc5/internal/model"
)

const messagesPerPage = 60

// Conversation is the open chat.
type Conversation struct {
	Title       string
	Subtitle    string
	BackHref    string // the chat list, for phones
	SelfHref    string // this page, for forms that come back to it
	OlderHref   string
	NewerHref   string
	Days        []Day
	Years       []RailYear
	YearOptions []YearOption
}

type Day struct {
	Label    string
	Messages []Message
}

type Message struct {
	Anchor        string // the element's id, "m" and the message ID
	Service       bool
	Outgoing      bool
	Target        bool // the message the page was opened at
	Sender        string
	ForwardedFrom string
	Text          string
	Time          string
	Edited        string // when it was last edited, or ""
	Deleted       string // when the source deleted it, or ""
	Versions      []Version
	Attachments   []Attachment
}

// Version is the text a message had before an edit.
type Version struct {
	Text  string
	Until string
}

type Attachment struct {
	ID        int64
	Mode      string // image, audio, video, file, skipped, pending, failed or unavailable
	Name      string
	Detail    string
	URL       string
	Ephemeral bool
}

// RailYear is one block of the year rail.
type RailYear struct {
	Label string
	Now   bool
	Bands []RailBand // newest quarter first
}

type RailBand struct {
	Href  string
	Label string
	Level int // 0 for no messages, up to 4 for the busiest quarter
	Now   bool
}

type YearOption struct {
	Value   string
	Label   string
	Current bool
}

func humanBytes(n int64) string {
	switch {
	case n < 1024:
		return fmt.Sprintf("%d B", n)
	case n < 1024*1024:
		return fmt.Sprintf("%.1f KB", float64(n)/1024)
	case n < 1024*1024*1024:
		return fmt.Sprintf("%.1f MB", float64(n)/(1024*1024))
	}
	return fmt.Sprintf("%.1f GB", float64(n)/(1024*1024*1024))
}

// inlineKind says how a stored file of this type may be shown in a page:
// "image", "audio", "video", or "" for a download only. The list is short on
// purpose: anything that can carry script, such as HTML or SVG, is not on it.
func inlineKind(mimeType string) string {
	switch strings.ToLower(strings.TrimSpace(strings.SplitN(mimeType, ";", 2)[0])) {
	case "image/jpeg", "image/png", "image/gif", "image/webp":
		return "image"
	case "audio/ogg", "audio/mpeg", "audio/mp4", "audio/aac", "audio/wav", "audio/x-wav", "audio/webm":
		return "audio"
	case "video/mp4", "video/webm":
		return "video"
	}
	return ""
}

func attachmentView(a archive.StoredAttachment) Attachment {
	v := Attachment{ID: a.ID, Name: a.FileName, Ephemeral: a.Ephemeral, URL: fmt.Sprintf("/media/%d", a.ID)}
	if v.Name == "" {
		v.Name = "Unnamed " + string(a.Kind)
	}
	size := humanBytes(a.Size)
	switch a.Status {
	case model.StatusStored:
		v.Mode, v.Detail = inlineKind(a.MimeType), size
		if v.Mode == "" {
			v.Mode = "file"
		}
	case model.StatusSkipped:
		v.Mode, v.Detail = "skipped", size+", not downloaded: "+a.Reason
	case model.StatusFailed:
		v.Mode, v.Detail = "failed", "The download failed: "+a.Reason
	case model.StatusUnavailable:
		v.Mode, v.Detail = "unavailable", "No longer available at the source"
	default:
		v.Mode, v.Detail = "pending", "Waiting to be downloaded"
	}
	return v
}

var quarterNames = [...]string{"January to March", "April to June", "July to September", "October to December"}

func plural(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return fmt.Sprintf("%d %s", n, many)
}

// rail builds the year rail from a chat's quarters. now is the moment the
// page is reading, which marks one band and one year.
func rail(path string, quarters []archive.Quarter, now time.Time) ([]RailYear, []YearOption) {
	if len(quarters) == 0 {
		return nil, nil
	}
	count := map[[2]int]int{}
	busiest, newest, oldest := 0, quarters[0].Year, quarters[0].Year
	for _, q := range quarters {
		count[[2]int{q.Year, q.Quarter}] = q.Messages
		busiest = max(busiest, q.Messages)
		newest, oldest = max(newest, q.Year), min(oldest, q.Year)
	}
	nowYear, nowQuarter := now.UTC().Year(), (int(now.UTC().Month())+2)/3
	var years []RailYear
	var options []YearOption
	for year := newest; year >= oldest; year-- {
		y := RailYear{Label: strconv.Itoa(year), Now: year == nowYear}
		for quarter := 4; quarter >= 1; quarter-- {
			n := count[[2]int{year, quarter}]
			level := 0
			if n > 0 {
				level = max(1, (n*4+busiest-1)/busiest)
			}
			y.Bands = append(y.Bands, RailBand{
				Href:  fmt.Sprintf("%s?date=%d-%02d-01", path, year, quarter*3-2),
				Label: fmt.Sprintf("%s %d, %s", quarterNames[quarter-1], year, plural(n, "message", "messages")),
				Level: level, Now: year == nowYear && quarter == nowQuarter,
			})
		}
		years = append(years, y)
		options = append(options, YearOption{Value: fmt.Sprintf("%d-01-01", year), Label: y.Label, Current: y.Now})
	}
	return years, options
}

func kindLabel(k model.ChatKind) string {
	switch k {
	case model.ChatGroup:
		return "Group"
	case model.ChatChannel:
		return "Channel"
	case model.ChatBot:
		return "Bot"
	}
	return "Private chat"
}

// anchor reads which part of the conversation the address asks for.
func (s *Server) anchor(r *http.Request) (archive.Anchor, int64) {
	q := r.URL.Query()
	id := func(name string) int64 {
		n, err := strconv.ParseInt(q.Get(name), 10, 64)
		if err != nil || n < 1 {
			return 0
		}
		return n
	}
	switch {
	case id("at") != 0:
		return archive.Anchor{Around: id("at")}, id("at")
	case id("before") != 0:
		return archive.Anchor{Before: id("before")}, 0
	case id("after") != 0:
		return archive.Anchor{After: id("after")}, 0
	}
	if day, err := time.ParseInLocation("2006-01-02", q.Get("date"), s.deps.Location); err == nil {
		return archive.Anchor{From: day}, 0
	}
	return archive.Anchor{}, 0
}

func (s *Server) conversationView(w http.ResponseWriter, r *http.Request, sess archive.Session) {
	chatID, err := strconv.ParseInt(r.PathValue("chat"), 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	data, sel, ok := s.sourceShell(w, r, sess, chatID)
	if !ok {
		return
	}
	chat, err := s.deps.Store.Chat(chatID)
	if err != nil || !containsID(sel.AccountIDs, chat.AccountID) {
		http.NotFound(w, r)
		return
	}
	store := s.deps.Store
	anchor, target := s.anchor(r)
	window, err := store.ChatMessages(chatID, anchor, messagesPerPage)
	if err == nil && len(window.Messages) == 0 && anchor != (archive.Anchor{}) {
		// The anchor led nowhere: show the end of the conversation instead.
		anchor, target = archive.Anchor{}, 0
		window, err = store.ChatMessages(chatID, anchor, messagesPerPage)
	}
	if err != nil {
		s.fail(w, err)
		return
	}
	quarters, err := store.ChatQuarters(chatID)
	if err != nil {
		s.fail(w, err)
		return
	}

	path := fmt.Sprintf("/s/%s/c/%d", sel.Key, chatID)
	conv := &Conversation{Title: chat.Title, BackHref: "/s/" + sel.Key, SelfHref: r.URL.RequestURI()}
	conv.Subtitle = kindLabel(chat.Kind)
	if service, known := s.serviceOf(chat.AccountID); known {
		conv.Subtitle += " on " + service
	}
	if chat.Messages > 0 {
		conv.Subtitle += fmt.Sprintf(", %s since %s", plural(chat.Messages, "message", "messages"), chat.FirstAt.In(s.deps.Location).Format("January 2006"))
	}

	// reading is the moment the rail marks: the end of the page, unless the
	// page was opened at a date, a message, or just after one.
	reading := s.deps.Now()
	if len(window.Messages) > 0 {
		reading = window.Messages[len(window.Messages)-1].SentAt
		if !anchor.From.IsZero() || anchor.After != 0 {
			reading = window.Messages[0].SentAt
		}
		for _, m := range window.Messages {
			if m.ID == target {
				reading = m.SentAt
			}
		}
		if window.HasOlder {
			conv.OlderHref = fmt.Sprintf("%s?before=%d", path, window.Messages[0].ID)
		}
		if window.HasNewer {
			conv.NewerHref = fmt.Sprintf("%s?after=%d", path, window.Messages[len(window.Messages)-1].ID)
		}
	}
	conv.Years, conv.YearOptions = rail(path, quarters, reading)

	for _, m := range window.Messages {
		sent := m.SentAt.In(s.deps.Location)
		label := sent.Format("Monday, 2 January 2006")
		if len(conv.Days) == 0 || conv.Days[len(conv.Days)-1].Label != label {
			conv.Days = append(conv.Days, Day{Label: label})
		}
		v := Message{
			Anchor: fmt.Sprintf("m%d", m.ID), Service: m.Kind == model.KindService, Outgoing: m.Outgoing,
			Target: m.ID == target, ForwardedFrom: m.ForwardedFrom, Text: m.Text, Time: sent.Format("15:04"),
		}
		if !m.Outgoing && chat.Kind != model.ChatPrivate {
			v.Sender = m.SenderName
		}
		if !m.EditedAt.IsZero() {
			v.Edited = m.EditedAt.In(s.deps.Location).Format("15:04")
		}
		if !m.DeletedAt.IsZero() {
			v.Deleted = m.DeletedAt.In(s.deps.Location).Format("2 Jan 2006")
		}
		if m.Versions > 0 {
			versions, err := store.Versions(m.ID)
			if err != nil {
				s.fail(w, err)
				return
			}
			for _, old := range versions {
				v.Versions = append(v.Versions, Version{Text: old.Text, Until: old.ReplacedAt.In(s.deps.Location).Format("2 Jan 2006, 15:04")})
			}
		}
		for _, a := range m.Attachments {
			v.Attachments = append(v.Attachments, attachmentView(a))
		}
		day := &conv.Days[len(conv.Days)-1]
		day.Messages = append(day.Messages, v)
	}

	data.Conversation = conv
	data.Title = chat.Title
	s.render(w, http.StatusOK, "source", data)
}

func containsID(ids []int64, id int64) bool {
	for _, x := range ids {
		if x == id {
			return true
		}
	}
	return false
}

// serviceOf is the service name of an account, such as "Telegram".
func (s *Server) serviceOf(accountID int64) (string, bool) {
	acc, err := s.deps.Store.Account(accountID)
	if err != nil {
		return "", false
	}
	if info, ok := s.deps.Registry.Info(acc.Connector); ok {
		return info.Title, true
	}
	return acc.Connector, true
}
```

- [ ] **Step 4: Write the templates**

`internal/web/templates/conversation.html`:

```html
{{define "conversation"}}{{with .Conversation}}
<header class="conv-head">
<a class="back only-phone" href="{{.BackHref}}" aria-label="Back to chats"><svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M15 5l-7 7 7 7"/></svg></a>
<div class="conv-head__text"><h1>{{.Title}}</h1><p>{{.Subtitle}}</p></div>
{{if .YearOptions}}<form class="jump only-phone" method="get"><label class="visually-hidden" for="jump-year">Jump to a year</label><select id="jump-year" name="date">{{range .YearOptions}}<option value="{{.Value}}"{{if .Current}} selected{{end}}>{{.Label}}</option>{{end}}</select><button class="button button--quiet" type="submit">Go</button></form>{{end}}
</header>
<div class="conv-body">
<div class="messages">
{{if .OlderHref}}<a class="more" href="{{.OlderHref}}">Earlier messages</a>{{end}}
{{range .Days}}<h2 class="day"><span>{{.Label}}</span></h2>
{{range .Messages}}{{template "message" (dict "M" . "CSRF" $.CSRF "Back" $.Conversation.SelfHref)}}
{{end}}{{else}}<p class="empty empty--center">Nothing has been archived in this chat yet.</p>
{{end}}{{if .NewerHref}}<a class="more" href="{{.NewerHref}}">Later messages</a>{{end}}
</div>
{{if .Years}}<nav class="rail" aria-label="Jump to a quarter">
{{range .Years}}<div class="rail__year{{if .Now}} rail__year--now{{end}}"><span class="rail__label">{{.Label}}</span>{{range .Bands}}<a class="band{{if .Level}} band--{{.Level}}{{end}}{{if .Now}} band--now{{end}}" href="{{.Href}}" aria-label="{{.Label}}" title="{{.Label}}"></a>{{end}}</div>
{{end}}</nav>{{end}}
</div>
{{end}}{{end}}

{{define "message"}}{{$m := .M}}{{if $m.Service}}<p class="service" id="{{$m.Anchor}}">{{$m.Text}}</p>{{else}}<article class="msg{{if $m.Outgoing}} msg--out{{end}}{{if $m.Target}} msg--target{{end}}" id="{{$m.Anchor}}">
{{if $m.Sender}}<div class="msg__sender">{{$m.Sender}}</div>{{end}}{{if $m.ForwardedFrom}}<div class="msg__fwd">Forwarded from {{$m.ForwardedFrom}}</div>{{end}}
{{range $m.Attachments}}{{template "attachment" (dict "A" . "CSRF" $.CSRF "Back" $.Back)}}
{{end}}{{if $m.Text}}<p class="msg__text">{{$m.Text}}</p>{{end}}
<div class="msg__meta"><time>{{$m.Time}}</time>{{if $m.Deleted}}<span class="tag tag--warn">Deleted at source on {{$m.Deleted}}</span>{{end}}</div>
{{if $m.Versions}}<details class="versions"><summary>Edited at {{$m.Edited}}, show earlier</summary>{{range $m.Versions}}<p class="versions__item">{{.Text}} <span class="versions__when">until {{.Until}}</span></p>{{end}}</details>{{else if $m.Edited}}<div class="versions">Edited at {{$m.Edited}}</div>{{end}}
</article>{{end}}{{end}}

{{define "attachment"}}{{$a := .A}}{{if eq $a.Mode "image"}}<a href="{{$a.URL}}"><img class="media media--image" src="{{$a.URL}}" alt="{{$a.Name}}" loading="lazy"></a>
{{else if eq $a.Mode "audio"}}<audio class="media" controls preload="none" src="{{$a.URL}}"></audio>
{{else if eq $a.Mode "video"}}<video class="media" controls preload="none" src="{{$a.URL}}"></video>
{{else}}<div class="file"><div class="file__text"><div class="file__name">{{$a.Name}}</div><div class="file__detail">{{$a.Detail}}</div></div>{{if eq $a.Mode "file"}}<a class="button button--quiet" href="{{$a.URL}}">Download</a>{{else if eq $a.Mode "skipped"}}<form method="post" action="{{$a.URL}}/fetch"><input type="hidden" name="csrf" value="{{.CSRF}}"><input type="hidden" name="back" value="{{.Back}}"><button class="button" type="submit">Fetch now</button></form>{{else if eq $a.Mode "failed"}}<form method="post" action="{{$a.URL}}/fetch"><input type="hidden" name="csrf" value="{{.CSRF}}"><input type="hidden" name="back" value="{{.Back}}"><button class="button" type="submit">Try again</button></form>{{end}}</div>
{{end}}{{if $a.Ephemeral}}<span class="tag">Sent as view-once</span>{{end}}{{end}}
```

In `internal/web/templates/source.html`, replace

```html
<p class="empty empty--center">Choose a chat on the left to read it.</p>
```

with

```html
{{if .Conversation}}{{template "conversation" .}}{{else}}<p class="empty empty--center">Choose a chat on the left to read it.</p>{{end}}
```

- [ ] **Step 5: Add the route**

In `internal/web/server.go`, add to `routes` after the `GET /s/{source}` line:

```go
	s.mux.Handle("GET /s/{source}/c/{chat}", s.signedIn(s.conversationView))
```

- [ ] **Step 6: Run the tests to verify they pass**

Run: `gofmt -l internal cmd; go vet ./... && go test -race -count=1 -timeout 180s ./...`
Expected: PASS. In `TestAttachmentModes` the skipped view-once photo also gets a "Fetch now" form, which is why three fetch forms are expected (the oversized video, the view-once photo, and the failed file).

- [ ] **Step 7: Commit**

```bash
git add internal/web
git commit -m "Add the conversation view with paging and the year rail"
```

---

### Task 8: Media

**Files:**
- Create: `internal/web/media.go`
- Modify: `internal/web/server.go` (routes)
- Test: `internal/web/media_test.go`

**Interfaces:**
- Consumes: `archive.Store.AttachmentFile`, `inlineKind`, `Runner.RequestMedia`, `signedIn`, `redirect`, `fail`; test helpers.
- Produces: routes `GET /media/{id}` and `POST /media/{id}/fetch`.

- [ ] **Step 1: Write the failing tests**

`internal/web/media_test.go`:

```go
package web

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/fenelon/arc5/internal/archive"
	"github.com/fenelon/arc5/internal/model"
)

// storeFiles stores one message with the given attachments, fills the first
// stored of them with content, and returns their attachment IDs.
func storeFiles(t *testing.T, s *site, stored int, attachments ...model.Attachment) []int64 {
	t.Helper()
	acc := s.account("Personal", "telegram")
	id := s.say(acc, "Files", "1", "", noon, func(m *model.Message) { m.Attachments = attachments })
	for i := range stored {
		ref := model.AttachmentRef{ChatSourceID: "Files", MessageSourceID: "1", Position: i}
		if err := s.store.PutMedia(acc, ref, strings.NewReader(fmt.Sprintf("content %d", i))); err != nil {
			t.Fatal(err)
		}
	}
	list, err := s.store.Attachments(id)
	if err != nil {
		t.Fatal(err)
	}
	ids := make([]int64, len(list))
	for i, a := range list {
		ids[i] = a.ID
	}
	return ids
}

func TestMediaIsServedToTheOwnerOnly(t *testing.T) {
	s := newSite(t, "")
	s.signIn()
	ids := storeFiles(t, s, 1, model.Attachment{Kind: model.AttachPhoto, FileName: "plan.jpg", MimeType: "image/jpeg", Size: 9})
	path := fmt.Sprintf("/media/%d", ids[0])

	rec := s.get(path)
	if rec.Code != http.StatusOK || rec.Body.String() != "content 0" {
		t.Fatalf("media: %d %q", rec.Code, rec.Body)
	}
	for header, want := range map[string]string{
		"Content-Type":            "image/jpeg",
		"X-Content-Type-Options":  "nosniff",
		"Content-Security-Policy": "sandbox; default-src 'none'",
	} {
		if got := rec.Header().Get(header); got != want {
			t.Errorf("%s = %q, want %q", header, got, want)
		}
	}
	if d := rec.Header().Get("Content-Disposition"); !strings.HasPrefix(d, "inline") || !strings.Contains(d, "plan.jpg") {
		t.Errorf("Content-Disposition = %q", d)
	}
	if !strings.Contains(rec.Header().Get("Cache-Control"), "private") {
		t.Errorf("Cache-Control = %q; archived media must not be cached by shared caches", rec.Header().Get("Cache-Control"))
	}

	req := httptest.NewRequest(http.MethodGet, path, nil)
	req.Header.Set("Range", "bytes=0-3")
	ranged := s.do(req)
	if ranged.Code != http.StatusPartialContent || ranged.Body.String() != "cont" {
		t.Errorf("range request: %d %q; players need it to seek", ranged.Code, ranged.Body)
	}

	stranger := &site{t: t, store: s.store, srv: s.srv, cookies: map[string]*http.Cookie{}}
	if got := stranger.get(path); !redirectsTo(got, "/login") {
		t.Fatalf("media without a session: %d", got.Code)
	}
}

// Review Focus 5: files that can carry script are downloads, never pages.
func TestDangerousTypesAreDownloads(t *testing.T) {
	s := newSite(t, "")
	s.signIn()
	ids := storeFiles(t, s, 4,
		model.Attachment{Kind: model.AttachDocument, FileName: "page.html", MimeType: "text/html", Size: 9},
		model.Attachment{Kind: model.AttachPhoto, FileName: "drawing.svg", MimeType: "image/svg+xml", Size: 9},
		model.Attachment{Kind: model.AttachPhoto, FileName: "liar.jpg", MimeType: "IMAGE/JPEG; charset=x", Size: 9},
		model.Attachment{Kind: model.AttachDocument, FileName: "a\"b\r\nSet-Cookie: x=1.txt", MimeType: "", Size: 9},
	)
	for i, wantInline := range []bool{false, false, true, false} {
		rec := s.get(fmt.Sprintf("/media/%d", ids[i]))
		if rec.Code != http.StatusOK {
			t.Fatalf("attachment %d: status %d", i, rec.Code)
		}
		d, ct := rec.Header().Get("Content-Disposition"), rec.Header().Get("Content-Type")
		if inline := strings.HasPrefix(d, "inline"); inline != wantInline {
			t.Errorf("attachment %d: disposition %q, content type %q", i, d, ct)
		}
		if !wantInline && ct != "application/octet-stream" {
			t.Errorf("attachment %d: content type %q, want application/octet-stream", i, ct)
		}
		if rec.Header().Get("Content-Security-Policy") != "sandbox; default-src 'none'" || rec.Header().Get("Set-Cookie") != "" {
			t.Errorf("attachment %d: headers %v", i, rec.Header())
		}
	}
}

func TestMediaThatIsNotStored(t *testing.T) {
	s := newSite(t, "")
	s.signIn()
	ids := storeFiles(t, s, 0, model.Attachment{Kind: model.AttachDocument, FileName: "waiting.bin", Size: 9})
	for _, path := range []string{fmt.Sprintf("/media/%d", ids[0]), "/media/999999", "/media/banana"} {
		if rec := s.get(path); rec.Code != http.StatusNotFound {
			t.Errorf("GET %s: status %d, want 404", path, rec.Code)
		}
	}
}

func TestFetchNow(t *testing.T) {
	s := newSite(t, "")
	s.signIn()
	ids := storeFiles(t, s, 0, model.Attachment{Kind: model.AttachVideo, FileName: "big.mp4", Size: 9})
	path := fmt.Sprintf("/media/%d/fetch", ids[0])

	rec := s.post(path, url.Values{"back": {"/s/chats/c/1?at=5"}})
	if !redirectsTo(rec, "/s/chats/c/1?at=5") || len(s.runner.requested) != 1 || s.runner.requested[0] != ids[0] {
		t.Fatalf("fetch: %d to %q, requested %v", rec.Code, rec.Header().Get("Location"), s.runner.requested)
	}
	// Only an address on this site is followed back.
	for _, back := range []string{"https://evil.example/", "//evil.example/", `/\evil.example`, "", "javascript:alert(1)"} {
		if rec := s.post(path, url.Values{"back": {back}}); !redirectsTo(rec, "/") {
			t.Errorf("back %q: redirected to %q, want /", back, rec.Header().Get("Location"))
		}
	}

	// Review Focus 2: no change without the token, none without a session.
	before := len(s.runner.requested)
	if rec := s.post(path, url.Values{"csrf": {"made-up"}}); rec.Code != http.StatusForbidden {
		t.Errorf("fetch with a wrong token: %d", rec.Code)
	}
	stranger := &site{t: t, store: s.store, srv: s.srv, cookies: map[string]*http.Cookie{}}
	if rec := stranger.post(path, url.Values{"csrf": {s.csrf}}); !redirectsTo(rec, "/login") {
		t.Errorf("fetch without a session: %d", rec.Code)
	}
	if len(s.runner.requested) != before {
		t.Fatal("a refused request still asked for the download")
	}

	s.runner.err = archive.ErrNotFound
	if rec := s.post(path, nil); rec.Code != http.StatusNotFound {
		t.Errorf("fetch of something already stored or missing: %d, want 404", rec.Code)
	}
	s.runner.err = errors.New("disk on fire")
	if rec := s.post(path, nil); rec.Code != http.StatusInternalServerError {
		t.Errorf("fetch that fails: %d, want 500", rec.Code)
	}
	if rec := s.post("/media/banana/fetch", nil); rec.Code != http.StatusNotFound {
		t.Errorf("fetch of a bad id: %d", rec.Code)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/web/`
Expected: FAIL: `/media/…` answers 404 and `/media/…/fetch` 405.

- [ ] **Step 3: Write the implementation**

`internal/web/media.go`:

```go
package web

import (
	"errors"
	"mime"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/fenelon/arc5/internal/archive"
)

// mediaPolicy stops a file from running script or loading anything, even if a
// browser is talked into displaying it.
const mediaPolicy = "sandbox; default-src 'none'"

// serveMedia sends an archived file. Files come from other people, so the
// type they claim is trusted only if it is on a short list of plain media;
// everything else, including HTML and SVG, is a download.
func (s *Server) serveMedia(w http.ResponseWriter, r *http.Request, _ archive.Session) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	file, err := s.deps.Store.AttachmentFile(id)
	if errors.Is(err, archive.ErrNotFound) || (err == nil && file.Path == "") {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		s.fail(w, err)
		return
	}
	f, err := os.Open(file.Path)
	if err != nil {
		s.fail(w, err)
		return
	}
	defer f.Close()

	contentType, disposition := "application/octet-stream", "attachment"
	if inlineKind(file.MimeType) != "" {
		contentType = strings.ToLower(strings.TrimSpace(strings.SplitN(file.MimeType, ";", 2)[0]))
		disposition = "inline"
	}
	name := strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return -1
		}
		return r
	}, file.FileName)
	if name == "" {
		name = "attachment"
	}
	h := w.Header()
	h.Set("Content-Type", contentType)
	// FormatMediaType quotes or encodes the name; it returns "" for one it cannot.
	if withName := mime.FormatMediaType(disposition, map[string]string{"filename": name}); withName != "" {
		disposition = withName
	}
	h.Set("Content-Disposition", disposition)
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Content-Security-Policy", mediaPolicy)
	h.Set("Cache-Control", "private, max-age=31536000, immutable") // a stored attachment never changes
	http.ServeContent(w, r, "", time.Time{}, f)
}

// localPath returns back if it is an address on this site, else "/".
func localPath(back string) string {
	if !strings.HasPrefix(back, "/") || strings.HasPrefix(back, "//") || strings.ContainsAny(back, "\\\r\n") {
		return "/"
	}
	return back
}

// fetchMedia asks the source's connector to download an attachment that was
// skipped or failed, whatever its size.
func (s *Server) fetchMedia(w http.ResponseWriter, r *http.Request, _ archive.Session) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	switch err := s.deps.Runner.RequestMedia(id); {
	case errors.Is(err, archive.ErrNotFound):
		http.NotFound(w, r)
	case err != nil:
		s.fail(w, err)
	default:
		redirect(w, r, localPath(r.PostFormValue("back")))
	}
}
```

In `internal/web/server.go`, add to `routes`:

```go
	s.mux.Handle("GET /media/{id}", s.signedIn(s.serveMedia))
	s.mux.Handle("POST /media/{id}/fetch", s.signedIn(s.fetchMedia))
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `gofmt -l internal cmd; go vet ./... && go test -race -count=1 -timeout 180s ./...`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/web
git commit -m "Serve archived media safely and let the owner fetch skipped files"
```

---

### Task 9: Wiring and a demo archive

**Files:**
- Create: `internal/connector/demo/demo.go`, `internal/connector/demo/demo_test.go`
- Modify: `internal/config/config.go`, `internal/config/config_test.go`, `cmd/arc5/main.go`, `cmd/arc5/main_test.go`, `README.md`

**Interfaces:**
- Consumes: `connector.Registry`, `connector.Info`, `connector.Connector`, `archive.Store` write methods, `model` types, `web.New`, `web.Deps`.
- Produces:
  - `demo.Name = "demo"`, `demo.Register(reg *connector.Registry)`, `demo.Seed(store *archive.Store) (bool, error)` — returns false and does nothing if the archive already has an account
  - `config.Config.Password string` (from `ARC5_PASSWORD`; there is no flag, because flags show in process listings)
  - `config.Demo{ DataDir string }`, `config.ParseDemo(args []string, getenv func(string) string) (Demo, error)`
  - command `arc5 demo [--data folder]`

- [ ] **Step 1: Write the failing tests**

`internal/connector/demo/demo_test.go`:

```go
package demo

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/fenelon/arc5/internal/archive"
	"github.com/fenelon/arc5/internal/connector"
	"github.com/fenelon/arc5/internal/model"
)

func TestSeedBuildsAnArchiveOnce(t *testing.T) {
	store, err := archive.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	seeded, err := Seed(store)
	if err != nil || !seeded {
		t.Fatalf("Seed = %v, %v", seeded, err)
	}
	accounts, err := store.Accounts()
	if err != nil || len(accounts) != 1 || accounts[0].Connector != Name || accounts[0].Label != "Personal" {
		t.Fatalf("accounts = %+v, %v", accounts, err)
	}
	chats, err := store.ChatList([]int64{accounts[0].ID}, 100, 0)
	if err != nil {
		t.Fatal(err)
	}
	titles := map[string]archive.ChatSummary{}
	for _, c := range chats {
		titles[c.Title] = c
	}
	for _, want := range []string{"Dan Whitfield", "Marina Sokolova", "Familia: Marina y Pablo", "Походы 2026", "Saved Messages"} {
		if _, ok := titles[want]; !ok {
			t.Errorf("no chat titled %q in %v", want, chats)
		}
	}

	dan := titles["Dan Whitfield"]
	quarters, err := store.ChatQuarters(dan.ID)
	if err != nil || len(quarters) < 8 {
		t.Errorf("Dan's chat spans %d quarters; the year rail needs several years: %v", len(quarters), err)
	}
	window, err := store.ChatMessages(dan.ID, archive.Anchor{}, 60)
	if err != nil {
		t.Fatal(err)
	}
	var outgoing, edited, deleted, stored, skipped int
	for _, m := range window.Messages {
		if m.Outgoing {
			outgoing++
		}
		if m.Versions > 0 {
			edited++
		}
		if !m.DeletedAt.IsZero() {
			deleted++
		}
		for _, a := range m.Attachments {
			switch a.Status {
			case model.StatusStored:
				stored++
				if _, err := os.Stat(store.MediaPath(a.SHA256)); err != nil {
					t.Errorf("stored attachment has no file: %v", err)
				}
			case model.StatusSkipped:
				skipped++
			}
		}
	}
	if outgoing == 0 || edited == 0 || deleted == 0 || stored == 0 || skipped < 2 {
		t.Errorf("the demo chat lacks variety: outgoing %d, edited %d, deleted %d, stored %d, skipped %d",
			outgoing, edited, deleted, stored, skipped)
	}

	again, err := Seed(store)
	if err != nil || again {
		t.Fatalf("second Seed = %v, %v; want it to leave an archive with accounts alone", again, err)
	}
	if after, _ := store.Accounts(); len(after) != 1 {
		t.Fatalf("%d accounts after a second Seed", len(after))
	}
}

func TestDemoConnectorReportsLiveAndStopsWhenCancelled(t *testing.T) {
	reg := connector.NewRegistry()
	Register(reg)
	info, ok := reg.Info(Name)
	if !ok || info.Display != connector.DisplayChats || info.Title == "" {
		t.Fatalf("info = %+v, %v", info, ok)
	}
	c, _ := reg.New(Name)

	ctx, cancel := context.WithCancel(context.Background())
	sink := &progressSink{reported: make(chan connector.Progress, 1)}
	done := make(chan error, 1)
	go func() { done <- c.Run(ctx, sink, nil, nil) }()
	select {
	case p := <-sink.reported:
		if p.Phase != connector.PhaseLive {
			t.Errorf("reported phase %q, want live", p.Phase)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the demo connector reported nothing")
	}
	cancel()
	select {
	case err := <-done:
		if err == nil {
			t.Error("Run returned nil; a connector must return why it stopped")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the demo connector did not stop")
	}
}

type progressSink struct {
	connector.Sink
	reported chan connector.Progress
}

func (s *progressSink) ReportProgress(p connector.Progress) { s.reported <- p }
```

Append to `internal/config/config_test.go`:

```go
func TestPasswordComesFromTheEnvironmentOnly(t *testing.T) {
	got, err := Parse(nil, env(map[string]string{"ARC5_PASSWORD": "from-env"}))
	if err != nil || got.Password != "from-env" {
		t.Fatalf("Parse = %+v, %v", got, err)
	}
	if _, err := Parse([]string{"--password", "x"}, env(nil)); err == nil {
		t.Error("a --password flag was accepted; flags show in process listings")
	}
}

func TestParseDemo(t *testing.T) {
	got, err := ParseDemo(nil, env(map[string]string{"ARC5_DATA": "/srv/arc5"}))
	if err != nil || got.DataDir != "/srv/arc5" {
		t.Fatalf("from environment = %+v, %v", got, err)
	}
	got, err = ParseDemo([]string{"--data", "/tmp/x"}, env(nil))
	if err != nil || got.DataDir != "/tmp/x" {
		t.Fatalf("flag = %+v, %v", got, err)
	}
	if _, err := ParseDemo([]string{"stray"}, env(nil)); err == nil {
		t.Error("a stray argument was accepted")
	}
}
```

`TestParse` needs no change: its four expected `Config` values leave `Password` empty, which is what `Parse` returns when `ARC5_PASSWORD` is unset.

Append to `cmd/arc5/main_test.go`:

```go
func TestDemoCommandAndTheInterface(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "data")
	if err := run(context.Background(), []string{"demo", "--data", dir}, noEnv, nil); err != nil {
		t.Fatal(err)
	}
	if err := run(context.Background(), []string{"demo", "--data", dir}, noEnv, nil); err == nil || !strings.Contains(err.Error(), "already") {
		t.Fatalf("second demo run error = %v; want it to refuse an archive that has sources", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	addr := make(chan string, 1)
	done := make(chan error, 1)
	env := func(key string) string {
		if key == "ARC5_PASSWORD" {
			return "from-env"
		}
		return ""
	}
	go func() {
		done <- run(ctx, []string{"serve", "--listen", "127.0.0.1:0", "--data", dir}, env, func(a string) { addr <- a })
	}()
	var base string
	select {
	case a := <-addr:
		base = "http://" + a
	case err := <-done:
		t.Fatalf("serve exited early: %v", err)
	case <-time.After(10 * time.Second):
		t.Fatal("serve never became ready")
	}

	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Jar: jar}
	read := func(resp *http.Response, err error) (int, string) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		body, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(body)
	}

	if code, body := read(client.Get(base + "/")); code != http.StatusOK || !strings.Contains(body, `name="password"`) {
		t.Fatalf("home before login: %d, want the login page", code)
	}
	code, body := read(client.PostForm(base+"/login", url.Values{"password": {"from-env"}}))
	if code != http.StatusOK || !strings.Contains(body, "Dan Whitfield") || !strings.Contains(body, "Everything at once") {
		t.Fatalf("after login: %d; want the demo chats and the picker", code)
	}
	// The demo connector reports itself live a moment after the server starts.
	deadline := time.Now().Add(5 * time.Second)
	for !strings.Contains(body, `aria-label="Live"`) {
		if time.Now().After(deadline) {
			t.Fatal("the demo source never showed as live")
		}
		time.Sleep(20 * time.Millisecond)
		_, body = read(client.Get(base + "/s/chats"))
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("serve returned %v after cancel", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("serve did not stop after cancel")
	}
}
```

Add `"net/http/cookiejar"` and `"net/url"` to the imports of `cmd/arc5/main_test.go`.

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go test ./internal/connector/demo/ ./internal/config/ ./cmd/arc5/`
Expected: FAIL to build with `undefined: Seed`, `undefined: ParseDemo`, `got.Password undefined`; then `unknown command "demo"`.

- [ ] **Step 3: Write the demo connector and archive**

`internal/connector/demo/demo.go`:

```go
// Package demo provides a sample archive to look at before connecting a real
// account, and a connector that does nothing but report it as live.
package demo

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"time"

	"github.com/fenelon/arc5/internal/archive"
	"github.com/fenelon/arc5/internal/connector"
	"github.com/fenelon/arc5/internal/model"
)

// Name is the connector name stored on demo accounts.
const Name = "demo"

type idle struct{}

func (idle) Run(ctx context.Context, sink connector.Sink, _ connector.State, _ map[string]string) error {
	sink.ReportProgress(connector.Progress{Phase: connector.PhaseLive})
	<-ctx.Done()
	return ctx.Err()
}
func (idle) Setup(connector.State) connector.SetupFlow { return nil }
func (idle) Settings() []connector.Field               { return nil }

// Register adds the demo connector, so demo accounts show as live.
func Register(reg *connector.Registry) {
	reg.Register(connector.Info{Name: Name, Title: "Demo", Display: connector.DisplayChats}, func() connector.Connector { return idle{} })
}

// day is the last day of the sample conversations.
var day = time.Date(2026, time.September, 28, 0, 0, 0, 0, time.UTC)

func at(daysBefore, hour, minute int) time.Time {
	return day.AddDate(0, 0, -daysBefore).Add(time.Duration(hour)*time.Hour + time.Duration(minute)*time.Minute)
}

// floorPlan draws a small picture to stand for a photo.
func floorPlan() []byte {
	img := image.NewRGBA(image.Rect(0, 0, 320, 200))
	wall, floor := color.RGBA{0x0D, 0x25, 0x30, 0xFF}, color.RGBA{0xD7, 0xEC, 0xEF, 0xFF}
	for y := range 200 {
		for x := range 320 {
			c := floor
			if x < 6 || x >= 314 || y < 6 || y >= 194 || (x > 150 && x < 156 && y < 120) || (y > 116 && y < 122 && x > 150) {
				c = wall
			}
			img.SetRGBA(x, y, c)
		}
	}
	var buf bytes.Buffer
	png.Encode(&buf, img)
	return buf.Bytes()
}

// Seed writes the sample archive. It does nothing, and returns false, if the
// archive already has an account.
func Seed(store *archive.Store) (bool, error) {
	accounts, err := store.Accounts()
	if err != nil || len(accounts) > 0 {
		return false, err
	}
	acc, err := store.CreateAccount(Name, "Personal", nil)
	if err != nil {
		return false, err
	}
	b := &builder{store: store, acc: acc}

	for _, s := range []model.Sender{
		{SourceID: "dan", DisplayName: "Dan Whitfield"}, {SourceID: "marina", DisplayName: "Marina Sokolova", Username: "marina_s"},
		{SourceID: "pablo", DisplayName: "Pablo Ruiz"}, {SourceID: "oleg", DisplayName: "Олег Ветров"},
	} {
		b.do(store.UpsertSender(acc, s))
	}
	for _, c := range []model.Chat{
		{SourceID: "dan", Kind: model.ChatPrivate, Title: "Dan Whitfield"},
		{SourceID: "marina", Kind: model.ChatPrivate, Title: "Marina Sokolova"},
		{SourceID: "familia", Kind: model.ChatGroup, Title: "Familia: Marina y Pablo"},
		{SourceID: "hikes", Kind: model.ChatGroup, Title: "Походы 2026"},
		{SourceID: "saved", Kind: model.ChatPrivate, Title: "Saved Messages"},
	} {
		b.do(store.UpsertChat(acc, c))
	}

	// Years of small talk with Dan, so the year rail has something to show.
	for month := 0; month < 66; month++ {
		when := time.Date(2021, time.March, 5, 9, 0, 0, 0, time.UTC).AddDate(0, month, 0)
		for i := range 1 + (month*7)%5 {
			b.say("dan", "dan", when.Add(time.Duration(i)*time.Hour), fmt.Sprintf("Lunch on the %dth? Usual place.", 5+i), i%2 == 1)
		}
	}
	b.say("dan", "", at(1, 18, 22), "Are we still on for Monday? I can bring the projector.", true)
	first := b.say("dan", "dan", at(1, 18, 40), "Yes. Room 2 from ten.", false)
	first.Text, first.EditedAt = "Yes. Room 4 from ten, the old one with the broken blinds.", at(1, 18, 44)
	b.put(first)
	plan := b.message("dan", "dan", at(1, 18, 41), "Here is the floor plan.", false)
	plan.Attachments = []model.Attachment{{Kind: model.AttachPhoto, FileName: "floor-plan.png", MimeType: "image/png", Size: 2048}}
	b.put(plan)
	b.do(store.PutMedia(acc, model.AttachmentRef{ChatSourceID: "dan", MessageSourceID: plan.SourceID}, bytes.NewReader(floorPlan())))
	b.put(model.Message{ChatSourceID: "dan", SourceID: b.next(), Kind: model.KindService, SentAt: at(1, 18, 42), Text: "Dan Whitfield pinned a message"})
	gone := b.say("dan", "dan", at(0, 10, 2), "Marina's flight lands at 18:40. Can you pick her up, or should I book a taxi?", false)
	b.do(store.MarkDeleted(acc, "dan", []string{gone.SourceID}))
	b.say("dan", "", at(0, 10, 5), "I'll get her. Terminal 1?", true)
	voice := b.message("dan", "dan", at(0, 10, 7), "", false)
	voice.Attachments = []model.Attachment{{Kind: model.AttachVoice, FileName: "voice-note.ogg", MimeType: "audio/ogg", Size: 61_000, NoFetch: true, NoFetchReason: "the demo archive has no audio"}}
	b.put(voice)
	video := b.message("dan", "dan", at(0, 10, 15), "And this is the route from arrivals, in case the signs are down again.", false)
	video.Attachments = []model.Attachment{{Kind: model.AttachVideo, FileName: "arrivals-walkthrough.mp4", MimeType: "video/mp4", Size: 312 << 20}}
	b.put(video)
	once := b.message("dan", "dan", at(0, 10, 16), "", false)
	once.Attachments = []model.Attachment{{Kind: model.AttachPhoto, FileName: "door-code.jpg", MimeType: "image/jpeg", Size: 240_000, Ephemeral: true, NoFetch: true, NoFetchReason: "self-destructing media is not archived"}}
	b.put(once)
	b.say("dan", "", at(0, 19, 3), "Got her. Traffic is bad, we'll be there around eight.", true)

	b.say("marina", "", at(2, 20, 15), "Долетела нормально?", true)
	b.say("marina", "marina", at(2, 20, 31), "Спасибо, что встретил! Чемодан нашёлся, всё хорошо.", false)
	b.say("familia", "marina", at(4, 20, 50), "¿Alguien ha visto mis llaves?", false)
	b.say("familia", "pablo", at(4, 21, 14), "Dile a Marina que las llaves están en el buzón, por favor.", false)
	b.say("hikes", "oleg", at(10, 7, 5), "Маршрут на субботу в закрепе. Встречаемся у причала в восемь.", false)
	b.say("hikes", "", at(10, 7, 40), "Буду. Беру горелку и чай.", true)
	b.say("saved", "", at(25, 9, 0), "Passport renewal: form 11, two photos, old passport.", true)
	return true, b.err
}

// builder stores sample messages and remembers the first error.
type builder struct {
	store *archive.Store
	acc   int64
	n     int
	err   error
}

func (b *builder) do(err error) {
	if b.err == nil {
		b.err = err
	}
}

func (b *builder) next() string {
	b.n++
	return fmt.Sprint(b.n)
}

func (b *builder) message(chat, sender string, when time.Time, text string, outgoing bool) model.Message {
	if outgoing {
		sender = "" // the owner is not one of the chat's senders
	}
	return model.Message{ChatSourceID: chat, SourceID: b.next(), SenderSourceID: sender, SentAt: when, Text: text, Outgoing: outgoing}
}

func (b *builder) put(m model.Message) {
	_, err := b.store.UpsertMessage(b.acc, m)
	b.do(err)
}

func (b *builder) say(chat, sender string, when time.Time, text string, outgoing bool) model.Message {
	m := b.message(chat, sender, when, text, outgoing)
	b.put(m)
	return m
}
```

- [ ] **Step 4: Read the password and the demo arguments**

In `internal/config/config.go`, add a field to `Config`:

```go
	Password string // from ARC5_PASSWORD only; empty lets the owner choose one in the browser
```

In `Parse`, add after the `ARC5_DATA` lines:

```go
	cfg.Password = getenv("ARC5_PASSWORD")
```

Append:

```go
// Demo holds the arguments of `arc5 demo`.
type Demo struct {
	DataDir string
}

// ParseDemo reads `demo` arguments.
func ParseDemo(args []string, getenv func(string) string) (Demo, error) {
	cfg := Demo{DataDir: "data"}
	if v := getenv("ARC5_DATA"); v != "" {
		cfg.DataDir = v
	}
	fs := flag.NewFlagSet("demo", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.StringVar(&cfg.DataDir, "data", cfg.DataDir, "data folder (ARC5_DATA)")
	if err := fs.Parse(args); err != nil {
		return Demo{}, err
	}
	if fs.NArg() > 0 {
		return Demo{}, fmt.Errorf("unexpected argument %q", fs.Arg(0))
	}
	return cfg, nil
}
```

- [ ] **Step 5: Wire the command**

In `cmd/arc5/main.go`:

Add `"github.com/fenelon/arc5/internal/connector/demo"` to the imports, after the `connector` import.

Replace the `usage` constant with:

```go
const usage = `usage:
  arc5 serve   [--listen address] [--data folder]
  arc5 reindex [--data folder] [--languages en,ru,es]
  arc5 demo    [--data folder]`
```

Add after the `languagesKey` constant:

```go
// The demo connector is registered once per process, so the sample archive's
// source shows as live.
func init() { demo.Register(connector.Default) }
```

In `run`, add a case before `default:`:

```go
	case "demo":
		cfg, err := config.ParseDemo(args[1:], getenv)
		if err != nil {
			return fmt.Errorf("%w\n%s", err, usage)
		}
		return seedDemo(cfg)
```

In `serve`, replace

```go
	site, err := web.New(web.Deps{Store: store, Runner: runner, Registry: connector.Default})
```

with

```go
	site, err := web.New(web.Deps{Store: store, Runner: runner, Registry: connector.Default, EnvPassword: cfg.Password})
```

Append:

```go
// seedDemo writes the sample archive into an empty data folder.
func seedDemo(cfg config.Demo) error {
	store, err := archive.Open(cfg.DataDir)
	if err != nil {
		return err
	}
	defer store.Close()

	// With the analyzer set, the sample messages are indexed as they are written.
	analyzer, err := loadAnalyzer(store)
	if err != nil {
		return err
	}
	store.SetAnalyzer(analyzer)

	seeded, err := demo.Seed(store)
	if err != nil {
		return err
	}
	if !seeded {
		return fmt.Errorf("%s already has sources; the demo archive is only written into an empty data folder", cfg.DataDir)
	}
	fmt.Printf("demo archive written to %s; start it with: arc5 serve --data %s\n", cfg.DataDir, cfg.DataDir)
	return nil
}
```

- [ ] **Step 6: Update the README**

In `README.md`, replace the `**Status:**` paragraph with:

```markdown
**Status:** early development. The archive, the search engine and a web
interface for reading chats run. Search in the interface and the Telegram
connector are not built yet, so the only thing to read is the demo archive.
The design is in [`docs/superpowers/specs`](docs/superpowers/specs).
```

Replace the whole `## Run` section (up to, not including, `## Search languages`) with:

````markdown
## Run

```bash
go build -o arc5 ./cmd/arc5
./arc5 demo --data ./demo-data    # optional: a sample archive to look around
./arc5 serve --data ./demo-data
```

Open <http://127.0.0.1:8080>. On the first visit arc5 asks you to choose a
password.

| Flag | Environment | Default | Meaning |
|---|---|---|---|
| `--listen` | `ARC5_LISTEN` | `127.0.0.1:8080` | Address to listen on |
| `--data` | `ARC5_DATA` | `data` | Folder for the database and media |
| | `ARC5_PASSWORD` | none | Login password. When set, it cannot be changed in the browser, and changing it signs every browser out |

`GET /healthz` answers `{"status":"ok"}` without a login.

The data folder holds everything arc5 knows, including connector sessions that
grant full access to your accounts. It is created with mode `0700`; keep it
that way.

arc5 listens on this computer only. To reach it from elsewhere, put it behind
a reverse proxy that adds TLS, or a VPN. Behind a proxy that sends
`X-Forwarded-Proto: https`, the login cookie is marked `Secure`.
````

- [ ] **Step 7: Run the tests to verify they pass**

Run: `gofmt -l internal cmd; go vet ./... && go test -race -count=1 -timeout 180s ./...`
Expected: `gofmt -l` prints nothing; tests PASS.

- [ ] **Step 8: Look at it**

```bash
DEMO=$(mktemp -d)
go run ./cmd/arc5 demo --data "$DEMO"
ARC5_PASSWORD=demo-password go run ./cmd/arc5 serve --data "$DEMO" --listen 127.0.0.1:8085
```

Open <http://127.0.0.1:8085>, sign in with `demo-password`, and compare with the approved mockup. Expected:

- The top bar has the four-bar logo, the picker showing "Personal" over "Demo" with a green dot, the theme button and "Sign out".
- The picker opens to a "Messengers" group whose first tile is "Everything at once", then "Personal", with "Manage sources" below; Escape and a click elsewhere close it.
- Five chats are listed, "Dan Whitfield" first with the preview "You: Got her. Traffic is bad, we'll be there around eight."
- Dan's chat shows day dividers, the owner's messages on the right in the teal tint, the edited message with "show earlier", the "Deleted at source" tag, the floor plan as a picture, the pinned-message line centred, and three files offering "Fetch now".
- The year rail lists 2026 down to 2021 with the current quarter marked; a band from 2023 opens that quarter, with "Earlier messages" and "Later messages" at the ends.
- The theme button switches to dark grey with orange, and a reload keeps the choice.
- In a window narrower than 760 pixels the chat list fills the screen; opening a chat shows only the conversation, with a back arrow and a year picker.
- The browser console reports no content-security-policy violations.

Stop the server with Ctrl-C. Fix anything that differs before committing; a fix to the styles needs no new test, a fix to the markup or a handler does.

- [ ] **Step 9: Commit**

```bash
git add internal/connector/demo internal/config cmd/arc5 README.md
git commit -m "Wire the interface into serve and add a demo archive"
```

## Post-review changes

Executed on branch `web-shell`. The code blocks above show the plan as
written; these are the changes made on top of it.

**From looking at the demo archive beside the mockups (Task 9, Step 8).** One
commit after Task 9, with tests for the markup:

| Difference from the mockups | Change | Test |
|---|---|---|
| The list and the conversation grew with their content, so the whole page scrolled and the rail and top bar scrolled away | `body.app--panes` fills the window; the list, the messages and the rail scroll on their own | (styles) |
| A conversation opened at the oldest of its 60 messages | `Conversation.ScrollTo` (`end`, `start`, or the target message's anchor) and `scroll_controller.js` | `TestConversationSaysWhereToScroll` |
| On a phone the top bar sat above an open chat, wrapped "Sign out" to a second row, and the year picker needed "Go" | `body.app--open` hides the top bar on phones; the brand name is hidden on phones; `jump_controller.js` jumps on choice and hides "Go" | `TestConversationPageIsItsOwnScreenOnPhones` |

Home still opens "Everything at once" (`/s/chats`), as `TestHomeOpensTheChats`
pins, although Step 8's text expected "Personal" in the picker.

**From the whole-branch review.** No critical findings; two important ones,
each fixed in its own commit:

| Finding | Change | Test |
|---|---|---|
| Parallel `POST /login` requests all passed the throttle before the first failure counted (32 of 40 were checked), each running a 19 MiB argon2 check at once | `auth.login` checks one attempt at a time; an attempt that waits more than 3 s is answered 429 | `TestParallelLoginsAreThrottled` |
| The year rail grouped quarters in UTC with `strftime` on every row, while its links are read in `Deps.Location`, so a message near a quarter's end could be missing from the page its band opens | `ChatQuarters(chatID, loc)` counts each quarter in the reader's zone over a range of the `(chat_id, sent_at)` index; about 100 ms on a 200,000-message chat, from about 290 ms | `TestChatQuartersFollowTheTimeZone`, `TestRailFollowsTheTimeZone` |

Not fixed, and listed for plan 3b: the global login throttle lets anyone keep
the owner out with a wrong password every few minutes; `localPath` lets a tab
through; `verifyPassword` trusts the cost numbers in a stored hash; two
simultaneous first-run setups can both succeed; a media file missing from disk
gives 500; "Edited at" has no date; lazy images can push the newest message
out of view; "Fetch now" never completes in the demo; the session test does
not cover every route; the rail still costs about 100 ms per page on a
200,000-message chat.
