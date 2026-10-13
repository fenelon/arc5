# arc5 Search, Sources and Settings (Milestone 3b) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Search the archive from the interface, manage sources on the Sources page (rename, choose archived chats, settings, sign out, remove, add a source through its login flow), and change Settings (theme, search languages, attachment limit, password), in the approved Strata look.

**Architecture:** A new `internal/indexer` package owns the search analyzer and searcher, so the language list can change while the server runs and the index rebuilds in the background. `internal/web` gains a search column that replaces the chat list (server-rendered, with a Stimulus controller that fetches the same column as a fragment while the owner types), a Sources page whose panel renders each connector's settings and setup steps from the connector's own field descriptions, and a Settings page. The archive gains the few writes these pages need; the connector package gains a hidden flag, a checkbox field kind and an optional sign-out.

**Tech Stack:** Go 1.26 `html/template` and `net/http`, SQLite (modernc), Stimulus 3.2.2 (vendored). No new dependencies.

**Spec:** `docs/superpowers/specs/2026-10-12-message-archive-design.md` (sections 3, 4, 5, 8 and 12). Approved mockups: `docs/superpowers/mockups/2026-10-13-interface/` (`StrataDark` for search, `Sources`, `Settings`, `PhoneChats`). This is plan 3b of milestone 3; plan 3a (`plans/2026-10-13-arc5-web-shell.md`) built the shell and the chats view.

## Decisions for the owner to confirm

The spec and mockups leave these open. The plan takes the position shown; each is cheap to change before execution.

1. **Outage alert is left out of Settings.** The mockup's Settings page has an "Outage alert" card, but alerts are milestone 5 (spec section 12). A webhook field that sends nothing would mislead, so the card arrives with the alert sender.
2. **Search has a sort switch and a Filters disclosure** (Task 7). The mockup shows neither, but the spec offers newest-first and filters for attachments, deletions, service messages and dates. They sit in one compact line above the results. Task 7 is self-contained and can be dropped.
3. **"Remove this source and keep its archive"** switches the source off for good, stops its connector, and deletes its connector state (the login session). Its chats and messages stay readable: the source stays in the picker and in search, and the Sources page lists it under "Removed, archive kept" with only its name editable. There is no way to delete an archive.
4. **"Sign this source out"** is offered only for connectors that implement a new optional `connector.SignOuter` interface (Telegram will, in milestone 4). The runner stops the connector, calls `SignOut`, and starts it again, when it reports that it needs login.
5. **Signing the browser out moves to the Settings page**, as in the mockups; the top bar shows "Settings" (an icon on phones) instead of "Sign out".
6. **The picker remembers the last source opened** (a cookie). Home opens it, and Sources and Settings show it in the picker. This replaces 3a's behaviour of always opening "Everything at once".
7. **View-once media the source chose not to keep cannot be fetched on request.** Until milestone 5's manual test confirms that downloading does not report it as opened, the interface shows such files without "Fetch now", and the server refuses the request (409).
8. **The password change form has two fields**, current and new, as in the mockup (first-run setup keeps its "type it twice").
9. **Connector model additions:** `Info.Hidden` (the demo is never offered under "Add a source"), field kind `bool` for checkboxes, and a setup flow may implement `io.Closer`; an unfinished login is closed after 30 minutes.
10. **The demo gains a sign-out and a login** (code 12345), so every part of the Sources page can be tried before Telegram exists, and its "Fetch now" answers "the demo has no files to fetch".
11. **Login steps get a page of their own** (`/sources/{id}/login`), reached from the panel, the banner and "Add a source". The spec says the setup flow runs "in that panel"; the panel already holds a settings form, and a separate page keeps a step's secret fields out of every other form. The step page uses the panel's look.
12. **No setup-flow or attachment Stimulus controllers.** The spec lists them, but the login steps and "Fetch now" work as plain forms that reload the page, which needs no script. The search and progress controllers are added.
13. **The minor findings of the 3a review are fixed here** (Tasks 12 to 14): no tab or control character in a redirect, bounded password-hash parameters, a single first-run password, 404 for a missing media file, dates on edits made another day, the newest message kept in view while pictures load, and a test that every route needs a session and its token. The global login throttle stays global: per-address counting is beaten by rotating addresses, and existing sessions keep working during a lockout.

## Global Constraints

- No inline styles and no inline scripts in any template. Every HTML response, fragments included, carries the 3a content-security policy unchanged.
- All text from the archive, from connectors (field labels, step help, error text) and from the search box is rendered through `html/template` escaping. No `template.HTML` is built anywhere.
- Every state-changing request needs a session and its CSRF token, except `POST /setup` and `POST /login`.
- `internal/archive` stays the only package that writes to the database.
- Secret fields (connector settings and login steps of kind `secret`) are never sent back to the browser.
- A connector's settings change, a chat's archive switch, a finished login and a removal each restart the account's connector through the runner; nothing reconfigures a running connector.
- Colours, type and spacing follow the two palettes of spec section 8 and the mockups; nothing new is invented where a mockup shows the answer.
- Results are paged 50 at a time (spec section 5); at most 40 pages.
- Every task ends with `gofmt -l internal cmd` printing nothing and `go vet ./... && go test -race -count=1 -timeout 180s ./...` passing.

## Review Focus

1. **Hostile text in a query or a result.** A query holding markup, and message text, chat titles and names holding HTML, appear as text in the search box, the results column, the fragment endpoint and the highlighted message. Pinned in Task 6 (`TestSearchShowsHostileTextAsText`).
2. **Forged and unauthenticated requests on the new routes.** Every page added here redirects to login without a session, and every new `POST` is refused without the token. Pinned in Task 13 (`TestEveryRouteNeedsASessionAndEveryChangeTheToken`).
3. **Secrets never return.** A stored secret setting is kept when its field is left empty and never appears in a page; a secret typed into a login step is not echoed when the step is refused. Pinned in Tasks 8 and 10.
4. **Removing a running source.** The connector is stopped before its state is deleted, so it cannot write its session back; actions on a removed source answer 409. Pinned in Task 8 (`TestRemoveSourceKeepsItsArchive`); the ordering itself is a review item.
5. **Changing languages while a rebuild runs.** A second change stops the first rebuild and the index ends current for the last list; search keeps answering meanwhile and says the index is being rebuilt. Pinned in Task 2 (`TestSetLanguagesReplacesARebuildInFlight`).

## How to read the code in this plan

The code here was written and run before the plan was saved, in a scratch copy that was then thrown away: each task's tests were seen to fail as its "Expected" line says and to pass after its implementation, every task leaves `gofmt`, `go vet` and `go test -race` clean, replaying the plan's blocks onto `master` reproduces that code exactly, and the result was compared with the mockups in a browser in both themes and at phone size. Executing it is still test-first: write each task's tests, watch them fail, then implement.

New files are given whole. Changes to existing files are unified diffs against the state the previous task left. Apply a diff by saving the block to a file and running `git apply <file>`, or by making the same edit by hand; the lines without `+` or `-` show where.

## File Structure

```
internal/search/{search,names,snippet}.go   modify: account scope, Highlight, Hit.ChatKind/Outgoing
internal/indexer/indexer.go                 create: analyzer, searcher, background rebuilds
internal/archive/migrations/0004_sources.sql create: accounts.removed_at
internal/archive/accounts.go                modify: RemovedAt, RenameAccount, RemoveAccount
internal/archive/chats.go                   modify: AccountChats, SetChatsArchived, LastMessageTimes, Sender
internal/archive/settings.go                modify: SetSettingIfAbsent
internal/connector/{connector,registry,runner,sink}.go  modify: Hidden, Infos, FieldBool, SignOuter, Runner.SignOut, NewState
internal/connector/demo/demo.go             modify: hidden, settings, sign-out, login, answers fetches
internal/web/settings.go                    create: Settings page
internal/web/search.go, filters.go          create: results column, sort and filters
internal/web/sources_page.go                create: Sources page and panel
internal/web/archived.go                    create: which chats are archived
internal/web/setup.go                       create: add a source, login steps
internal/web/{server,chats,sources,conversation,media,auth,auth_handlers,page}.go  modify
internal/web/templates/{settings,archived,sources_setup}.html  create
internal/web/templates/{partials,source,sources,conversation,empty}.html  modify
internal/web/static/js/{search,progress}_controller.js  create
internal/web/static/js/{app,jump,scroll}_controller.js / app.js  modify
internal/web/static/app.css                 modify
cmd/arc5/main.go                            modify: indexer, Deps.Index
README.md                                   modify
```

---

### Task 1: Search within chosen accounts, highlighting, and result details

The search engine learns to search several accounts at once ("Everything at once"), to find names within those accounts only, to mark the query's words in a whole message, and to tell a result row whether the owner sent the message and what kind of chat it is in.

**Files:**
- Modify: `internal/search/names.go`, `internal/search/search.go`, `internal/search/snippet.go`
- Test: `internal/search/names_test.go`, `internal/search/search_test.go`, `internal/search/snippet_test.go`

**Interfaces:**
- Produces:
  - `search.Query.AccountIDs []int64` — replaces `AccountID int64`; nil means every account
  - `(*search.Searcher).Names(ctx, text string, accountIDs []int64, limit int) (Names, error)` — gains `accountIDs`
  - `(*search.Searcher).Highlight(query, text string) []search.Fragment` — the whole text with matches marked, line breaks kept
  - `search.Hit.ChatKind model.ChatKind`, `search.Hit.Outgoing bool`

- [ ] **Step 1: Write the failing tests**

Change `internal/search/names_test.go`:

````diff
--- a/internal/search/names_test.go
+++ b/internal/search/names_test.go
@@ -36,7 +36,7 @@ func namesFixture(t *testing.T) *fixture {
 
 func (f *fixture) names(query string) Names {
 	f.t.Helper()
-	names, err := f.search.Names(context.Background(), query, 0)
+	names, err := f.search.Names(context.Background(), query, nil, 0)
 	if err != nil {
 		f.t.Fatalf("Names(%q): %v", query, err)
 	}
@@ -138,7 +138,7 @@ func TestNamesLimit(t *testing.T) {
 		f.say("x", func(m *model.Message) { m.ChatSourceID = id })
 		_ = i
 	}
-	got, err := f.search.Names(context.Background(), "team", 2)
+	got, err := f.search.Names(context.Background(), "team", nil, 2)
 	if err != nil {
 		t.Fatal(err)
 	}
@@ -160,3 +160,40 @@ func TestNamesInUnspacedScripts(t *testing.T) {
 		}
 	}
 }
+
+func TestNamesKeepToTheChosenAccounts(t *testing.T) {
+	f := namesFixture(t)
+	other, err := f.store.CreateAccount("test", "Other", nil)
+	if err != nil {
+		t.Fatal(err)
+	}
+	if err := f.store.UpsertChat(other, model.Chat{SourceID: "x", Kind: model.ChatPrivate, Title: "Alice Elsewhere"}); err != nil {
+		t.Fatal(err)
+	}
+	if err := f.store.UpsertSender(other, model.Sender{SourceID: "ux", DisplayName: "Alice Elsewhere"}); err != nil {
+		t.Fatal(err)
+	}
+	if _, err := f.store.UpsertMessage(other, model.Message{ChatSourceID: "x", SourceID: "1", SenderSourceID: "ux", SentAt: day, Text: "hi"}); err != nil {
+		t.Fatal(err)
+	}
+	for name, c := range map[string]struct {
+		accounts      []int64
+		chats, people []string
+	}{
+		"every account":    {nil, []string{"[Alice] Elsewhere", "[Alice] Smith"}, []string{"[Alice] Elsewhere", "[Alice] Smith"}},
+		"the first only":   {[]int64{f.acc}, []string{"[Alice] Smith"}, []string{"[Alice] Smith"}},
+		"the second only":  {[]int64{other}, []string{"[Alice] Elsewhere"}, []string{"[Alice] Elsewhere"}},
+		"an empty account": {[]int64{9999}, nil, nil},
+	} {
+		got, err := f.search.Names(context.Background(), "alice", c.accounts, 0)
+		if err != nil {
+			t.Fatal(err)
+		}
+		chats, people := chatTitles(got), peopleNames(got)
+		slices.Sort(chats)
+		slices.Sort(people)
+		if !slices.Equal(chats, c.chats) || !slices.Equal(people, c.people) {
+			t.Errorf("%s: chats %q, people %q; want %q and %q", name, chats, people, c.chats, c.people)
+		}
+	}
+}
````

Change `internal/search/search_test.go`:

````diff
--- a/internal/search/search_test.go
+++ b/internal/search/search_test.go
@@ -219,9 +219,12 @@ func TestFilters(t *testing.T) {
 	if got := q(func(q *Query) { q.IncludeService = true }); !slices.Contains(got, service) {
 		t.Errorf("with service messages = %v", got)
 	}
-	if got := q(func(q *Query) { q.AccountID = other }); !same(got, "other") {
+	if got := q(func(q *Query) { q.AccountIDs = []int64{other} }); !same(got, "other") {
 		t.Errorf("by account = %v", got)
 	}
+	if got := q(func(q *Query) { q.AccountIDs = []int64{f.acc, other} }); !same(got, plain, inGroup, fromBob, withFile, deleted, "other") {
+		t.Errorf("by two accounts = %v", got)
+	}
 	if got := q(func(q *Query) { q.ChatID = f.id("chats", "g1") }); !same(got, inGroup) {
 		t.Errorf("by chat = %v", got)
 	}
@@ -238,7 +241,7 @@ func TestFilters(t *testing.T) {
 	if got := q(func(q *Query) { q.From, q.To = day.Add(2*time.Minute), day.Add(4*time.Minute) }); !same(got, inGroup, fromBob) {
 		t.Errorf("by date = %v", got)
 	}
-	if got := q(func(q *Query) { q.AccountID, q.HasAttachment, q.DeletedOnly = f.acc, true, true }); len(got) != 0 {
+	if got := q(func(q *Query) { q.AccountIDs, q.HasAttachment, q.DeletedOnly = []int64{f.acc}, true, true }); len(got) != 0 {
 		t.Errorf("filters must combine with AND: %v", got)
 	}
 }
@@ -324,6 +327,13 @@ func TestHitCarriesWhatAResultRowShows(t *testing.T) {
 	if got := render(h.Snippet); got != "The [cats] are sleeping" {
 		t.Errorf("snippet = %q", got)
 	}
+	if h.ChatKind != model.ChatPrivate || h.Outgoing {
+		t.Errorf("chat kind %q, outgoing %v", h.ChatKind, h.Outgoing)
+	}
+	f.say("my cats too", func(m *model.Message) { m.Outgoing = true })
+	if mine := f.page(Query{Text: "my "}).Hits; len(mine) != 1 || !mine[0].Outgoing {
+		t.Errorf("the owner's message = %+v", mine)
+	}
 }
 
 // Review Focus 1: nothing the owner types may produce an error.
````

Change `internal/search/snippet_test.go`:

````diff
--- a/internal/search/snippet_test.go
+++ b/internal/search/snippet_test.go
@@ -94,3 +94,21 @@ func TestSnippetSurvivesOddText(t *testing.T) {
 		}
 	}
 }
+
+func TestHighlightMarksTheWholeText(t *testing.T) {
+	a := mustAnalyzer(t, "en", "ru")
+	s := New(nil, a)
+	long := strings.Repeat("filler words here ", 20) + "the cats sat"
+	for _, c := range []struct{ query, text, want string }{
+		{"cat", "The cats sat.\nThen a cat left.", "The [cats] sat.\nThen a [cat] left."},
+		{"кот", "Коты спят", "[Коты] спят"},
+		{"cats", long, strings.Repeat("filler words here ", 20) + "the [cats] sat"},
+		{"dog", "The cats sat.", "The cats sat."},
+		{"", "The cats sat.", "The cats sat."},
+		{"cat", "", ""},
+	} {
+		if got := render(s.Highlight(c.query, c.text)); got != c.want {
+			t.Errorf("Highlight(%q, %q) = %q, want %q", c.query, c.text, got, c.want)
+		}
+	}
+}
````

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go vet ./internal/search/; go test -count=1 ./internal/search/`
Expected: FAIL to build: `too many arguments in call to f.search.Names` (then `q.AccountIDs undefined`, `s.Highlight undefined`, `h.ChatKind undefined`).

- [ ] **Step 3: Write the implementation**

Change `internal/search/names.go`:

````diff
--- a/internal/search/names.go
+++ b/internal/search/names.go
@@ -41,11 +41,12 @@ type Names struct {
 // must begin a word of the name; case, accents and ё/е are ignored, as in
 // message search. Chats come whole-word matches first, then most recent
 // first. A chat with no messages and a person with no name are not listed.
+// accountIDs limits the search to those accounts; nil means every account.
 // limit caps each list; zero means DefaultNameLimit.
 //
 // Names are matched by reading every chat and sender, which is a few thousand
 // rows for a personal archive, not through the message index.
-func (s *Searcher) Names(ctx context.Context, text string, limit int) (Names, error) {
+func (s *Searcher) Names(ctx context.Context, text string, accountIDs []int64, limit int) (Names, error) {
 	if limit <= 0 {
 		limit = DefaultNameLimit
 	}
@@ -60,6 +61,10 @@ func (s *Searcher) Names(ctx context.Context, text string, limit int) (Names, er
 		words = words[:maxQueryTerms]
 	}
 	highlight := parsed{exact: map[string]bool{}, stems: map[string]bool{}, prefixes: words}
+	scope, args := "", ids(accountIDs)
+	if len(accountIDs) > 0 {
+		scope = ` AND account_id IN (` + placeholders(len(accountIDs)) + `)`
+	}
 
 	type rankedChat struct {
 		ChatMatch
@@ -69,7 +74,7 @@ func (s *Searcher) Names(ctx context.Context, text string, limit int) (Names, er
 	rows, err := s.db.QueryContext(ctx, `
 		SELECT c.id, c.account_id, c.kind, c.title,
 		       (SELECT MAX(m.sent_at) FROM messages m WHERE m.chat_id = c.id)
-		FROM chats c`)
+		FROM chats c WHERE 1`+scope, args...)
 	if err != nil {
 		return Names{}, err
 	}
@@ -116,7 +121,7 @@ func (s *Searcher) Names(ctx context.Context, text string, limit int) (Names, er
 	var people []rankedPerson
 	rows, err = s.db.QueryContext(ctx, `
 		SELECT id, account_id, display_name, username FROM senders
-		WHERE display_name != '' OR username != ''`)
+		WHERE (display_name != '' OR username != '')`+scope, args...)
 	if err != nil {
 		return Names{}, err
 	}
````

Change `internal/search/search.go`:

````diff
--- a/internal/search/search.go
+++ b/internal/search/search.go
@@ -25,7 +25,7 @@ const (
 // Query is a search request. IDs are database IDs; zero means any.
 type Query struct {
 	Text           string
-	AccountID      int64
+	AccountIDs     []int64 // the accounts to search; nil means every account
 	ChatID         int64
 	SenderID       int64
 	From           time.Time // inclusive; zero means unbounded
@@ -44,7 +44,9 @@ type Hit struct {
 	AccountID  int64
 	ChatID     int64
 	ChatTitle  string
+	ChatKind   model.ChatKind
 	SenderName string
+	Outgoing   bool // sent by the account owner
 	Kind       model.MessageKind
 	SentAt     time.Time
 	Edited     bool
@@ -103,8 +105,8 @@ func (s *Searcher) Search(ctx context.Context, q Query) (Page, error) {
 			order = `bm25(messages_fts, 2.0, 1.0), m.sent_at DESC, m.id DESC`
 		}
 	}
-	if q.AccountID != 0 {
-		add(`c.account_id = ?`, q.AccountID)
+	if len(q.AccountIDs) > 0 {
+		add(`c.account_id IN (`+placeholders(len(q.AccountIDs))+`)`, ids(q.AccountIDs)...)
 	}
 	if q.ChatID != 0 {
 		add(`m.chat_id = ?`, q.ChatID)
@@ -129,7 +131,7 @@ func (s *Searcher) Search(ctx context.Context, q Query) (Page, error) {
 	}
 
 	query := `
-		SELECT m.id, c.account_id, c.id, c.title, COALESCE(sn.display_name, ''), m.kind, m.sent_at,
+		SELECT m.id, c.account_id, c.id, c.title, c.kind, COALESCE(sn.display_name, ''), m.outgoing, m.kind, m.sent_at,
 		       m.edited_at IS NOT NULL, m.deleted_at IS NOT NULL, m.text
 		FROM ` + from + `
 		JOIN chats c ON c.id = m.chat_id
@@ -148,19 +150,20 @@ func (s *Searcher) Search(ctx context.Context, q Query) (Page, error) {
 	var page Page
 	for rows.Next() {
 		var (
-			h    Hit
-			kind string
-			sent int64
-			text string
+			h        Hit
+			chatKind string
+			kind     string
+			sent     int64
+			text     string
 		)
-		if err := rows.Scan(&h.MessageID, &h.AccountID, &h.ChatID, &h.ChatTitle, &h.SenderName, &kind, &sent, &h.Edited, &h.Deleted, &text); err != nil {
+		if err := rows.Scan(&h.MessageID, &h.AccountID, &h.ChatID, &h.ChatTitle, &chatKind, &h.SenderName, &h.Outgoing, &kind, &sent, &h.Edited, &h.Deleted, &text); err != nil {
 			return Page{}, err
 		}
 		if len(page.Hits) == limit {
 			page.More = true
 			break
 		}
-		h.Kind = model.MessageKind(kind)
+		h.Kind, h.ChatKind = model.MessageKind(kind), model.ChatKind(chatKind)
 		h.SentAt = time.Unix(sent, 0).UTC()
 		h.Snippet = s.analyzer.snippet(text, p)
 		page.Hits = append(page.Hits, h)
@@ -194,7 +197,7 @@ func (s *Searcher) addFiles(ctx context.Context, hits []Hit, p parsed) error {
 	}
 	rows, err := s.db.QueryContext(ctx, `
 		SELECT message_id, file_name FROM attachments
-		WHERE file_name != '' AND message_id IN (`+strings.TrimSuffix(strings.Repeat("?,", len(ids)), ",")+`)
+		WHERE file_name != '' AND message_id IN (`+placeholders(len(ids))+`)
 		ORDER BY message_id, position`, ids...)
 	if err != nil {
 		return err
@@ -215,3 +218,14 @@ func (s *Searcher) addFiles(ctx context.Context, hits []Hit, p parsed) error {
 	}
 	return rows.Err()
 }
+
+func placeholders(n int) string { return strings.TrimSuffix(strings.Repeat("?,", n), ",") }
+
+// ids turns account IDs into query arguments.
+func ids(accountIDs []int64) []any {
+	out := make([]any, len(accountIDs))
+	for i, id := range accountIDs {
+		out[i] = id
+	}
+	return out
+}
````

Change `internal/search/snippet.go`:

````diff
--- a/internal/search/snippet.go
+++ b/internal/search/snippet.go
@@ -111,3 +111,26 @@ func (a *Analyzer) snippet(text string, p parsed) []Fragment {
 	}
 	return out
 }
+
+// Highlight returns the whole of text with the words query matches marked,
+// as the conversation view shows a message opened from a search. Line breaks
+// are kept.
+func (s *Searcher) Highlight(query, text string) []Fragment {
+	text = prepare(text)
+	if text == "" {
+		return nil
+	}
+	var out []Fragment
+	pos := 0
+	for _, span := range s.analyzer.matchSpans(text, s.analyzer.parse(query)) {
+		if pos < span[0] {
+			out = append(out, Fragment{Text: text[pos:span[0]]})
+		}
+		out = append(out, Fragment{Text: text[span[0]:span[1]], Match: true})
+		pos = span[1]
+	}
+	if pos < len(text) {
+		out = append(out, Fragment{Text: text[pos:]})
+	}
+	return out
+}
````

- [ ] **Step 4: Run the tests to verify they pass**

Run: `gofmt -l internal cmd; go vet ./... && go test -race -count=1 -timeout 180s ./...`
Expected: `gofmt -l` prints nothing; every package PASSES.

- [ ] **Step 5: Commit**

```bash
git add internal/search
git commit -m "Search chosen accounts, highlight whole messages, and say who sent a hit"
```

---

### Task 2: An indexer that changes languages while the server runs

Settings will change the search languages without a restart. `internal/indexer` holds the analyzer the archive indexes with and the searcher that reads the index, swaps both when the languages change, and rebuilds the index in the background. `serve`, `reindex` and `demo` use it instead of their own copy of the start-up logic.

**Files:**
- Create: `internal/indexer/indexer.go`
- Modify: `cmd/arc5/main.go`
- Test: `internal/indexer/indexer_test.go` (new)

**Interfaces:**
- Consumes: `archive.Store` (`Setting`, `SetSetting`, `SetAnalyzer`, `IndexCurrent`, `Reindex`, `Reader`), `search.NewAnalyzer`, `search.ParseLanguages`, `search.New`.
- Produces:
  - `indexer.LanguagesKey = "search_languages"`, `indexer.ErrUnknownLanguage`
  - `type indexer.Status struct { Languages []string; Rebuilding bool; Done, Total int; Err string }`
  - `indexer.Load(store *archive.Store) (*Indexer, error)` — reads the setting (default English) and sets the store's analyzer
  - `(*Indexer).Searcher() *search.Searcher`, `Status() Status`, `Start(ctx) error` (rebuilds in the background if the index is stale), `SetLanguages(codes []string) error` (saves, swaps, rebuilds; stops a rebuild in flight), `Wait()`; field `Logf`
  - `cmd/arc5`: `loadAnalyzer` and `languagesKey` are removed

- [ ] **Step 1: Write the failing tests**

`internal/indexer/indexer_test.go`:

````go
package indexer

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/fenelon/arc5/internal/archive"
	"github.com/fenelon/arc5/internal/model"
	"github.com/fenelon/arc5/internal/search"
)

func openStore(t *testing.T) *archive.Store {
	t.Helper()
	store, err := archive.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	return store
}

// seed stores n messages, each saying "running".
func seed(t *testing.T, store *archive.Store, n int) int64 {
	t.Helper()
	acc, err := store.CreateAccount("test", "Test", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertChat(acc, model.Chat{SourceID: "c", Kind: model.ChatPrivate, Title: "Chat"}); err != nil {
		t.Fatal(err)
	}
	for i := range n {
		m := model.Message{ChatSourceID: "c", SourceID: fmt.Sprint(i), SentAt: time.Unix(1_700_000_000+int64(i), 0), Text: "running"}
		if _, err := store.UpsertMessage(acc, m); err != nil {
			t.Fatal(err)
		}
	}
	return acc
}

// hits counts the messages a search for text finds.
func hits(t *testing.T, x *Indexer, text string) int {
	t.Helper()
	page, err := x.Searcher().Search(context.Background(), search.Query{Text: text + " ", Limit: search.MaxLimit})
	if err != nil {
		t.Fatal(err)
	}
	return len(page.Hits)
}

// settle waits for any rebuild to finish.
func settle(t *testing.T, x *Indexer) Status {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for x.Status().Rebuilding {
		if time.Now().After(deadline) {
			t.Fatal("the rebuild never finished")
		}
		time.Sleep(5 * time.Millisecond)
	}
	return x.Status()
}

func TestLoadUsesTheSavedLanguages(t *testing.T) {
	store := openStore(t)
	x, err := Load(store)
	if err != nil {
		t.Fatal(err)
	}
	if got := x.Status().Languages; !slices.Equal(got, []string{"en"}) {
		t.Fatalf("languages on a new archive = %v, want [en]", got)
	}
	if err := store.SetSetting(LanguagesKey, "ru,es"); err != nil {
		t.Fatal(err)
	}
	x, err = Load(store)
	if err != nil {
		t.Fatal(err)
	}
	if got := x.Status().Languages; !slices.Equal(got, []string{"es", "ru"}) && !slices.Equal(got, []string{"ru", "es"}) {
		t.Fatalf("languages = %v, want ru and es", got)
	}
	if err := store.SetSetting(LanguagesKey, "en,xx"); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(store); err == nil {
		t.Fatal("an unknown language in the setting was accepted")
	}
}

func TestStartRebuildsAStaleIndex(t *testing.T) {
	store := openStore(t)
	seed(t, store, 3) // stored with no analyzer, so not indexed
	x, err := Load(store)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer func() { cancel(); x.Wait() }()
	if err := x.Start(ctx); err != nil {
		t.Fatal(err)
	}
	if st := settle(t, x); st.Err != "" || st.Done != 3 || st.Total != 3 {
		t.Fatalf("status after the rebuild = %+v", st)
	}
	if n := hits(t, x, "run"); n != 3 {
		t.Fatalf("a stem search found %d messages, want 3", n)
	}
	if current, _ := store.IndexCurrent(); !current {
		t.Fatal("the index is not current after the rebuild")
	}

	// A current index is left alone.
	again, err := Load(store)
	if err != nil {
		t.Fatal(err)
	}
	if err := again.Start(ctx); err != nil {
		t.Fatal(err)
	}
	if st := again.Status(); st.Rebuilding || st.Total != 0 {
		t.Fatalf("a current index was rebuilt: %+v", st)
	}
	again.Wait()
}

func TestSetLanguagesSwitchesAndRebuilds(t *testing.T) {
	store := openStore(t)
	x, err := Load(store)
	if err != nil {
		t.Fatal(err)
	}
	acc := seed(t, store, 0)
	ctx, cancel := context.WithCancel(context.Background())
	defer func() { cancel(); x.Wait() }()
	if err := x.Start(ctx); err != nil {
		t.Fatal(err)
	}
	settle(t, x)
	m := model.Message{ChatSourceID: "c", SourceID: "ru", SentAt: time.Unix(1_700_000_000, 0), Text: "кошками"}
	if _, err := store.UpsertMessage(acc, m); err != nil {
		t.Fatal(err)
	}
	if n := hits(t, x, "кошка"); n != 0 {
		t.Fatalf("Russian word forms are found before Russian is chosen: %d", n)
	}

	if err := x.SetLanguages([]string{"ru", "en"}); err != nil {
		t.Fatal(err)
	}
	st := settle(t, x)
	if st.Err != "" || !slices.Contains(st.Languages, "ru") {
		t.Fatalf("status = %+v", st)
	}
	if saved, _, _ := store.Setting(LanguagesKey); saved != "en,ru" {
		t.Errorf("saved languages = %q, want en,ru", saved)
	}
	if n := hits(t, x, "кошка"); n != 1 {
		t.Fatalf("after choosing Russian, a word form found %d messages, want 1", n)
	}
	// New messages are indexed with the new languages too.
	m.SourceID, m.Text = "ru2", "кошкой"
	if _, err := store.UpsertMessage(acc, m); err != nil {
		t.Fatal(err)
	}
	if n := hits(t, x, "кошка"); n != 2 {
		t.Fatalf("a message stored after the switch is not found by word form: %d", n)
	}

	if err := x.SetLanguages([]string{"en", "zz"}); !errors.Is(err, ErrUnknownLanguage) {
		t.Fatalf("an unknown language: %v, want ErrUnknownLanguage", err)
	}
	if err := x.SetLanguages(nil); err != nil {
		t.Fatalf("no languages at all: %v", err)
	}
	settle(t, x)
	if saved, _, _ := store.Setting(LanguagesKey); saved != "" {
		t.Errorf("saved languages = %q, want none", saved)
	}
}

func TestSetLanguagesReplacesARebuildInFlight(t *testing.T) {
	store := openStore(t)
	seed(t, store, 50)
	x, err := Load(store)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer func() { cancel(); x.Wait() }()
	if err := x.Start(ctx); err != nil {
		t.Fatal(err)
	}
	for _, langs := range [][]string{{"es"}, {"ru"}, {"en"}} {
		if err := x.SetLanguages(langs); err != nil {
			t.Fatal(err)
		}
	}
	st := settle(t, x)
	if st.Err != "" || !slices.Equal(st.Languages, []string{"en"}) || st.Done != 50 {
		t.Fatalf("status = %+v", st)
	}
	if current, _ := store.IndexCurrent(); !current {
		t.Fatal("the index is not current after the last rebuild")
	}
}
````

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go vet ./internal/indexer/; go test -count=1 ./internal/indexer/`
Expected: FAIL to build: `undefined: Indexer`, `undefined: Load`.

- [ ] **Step 3: Write the implementation**

Change `cmd/arc5/main.go`:

````diff
--- a/cmd/arc5/main.go
+++ b/cmd/arc5/main.go
@@ -11,7 +11,6 @@ import (
 	"os"
 	"os/signal"
 	"strings"
-	"sync"
 	"syscall"
 	"time"
 
@@ -19,6 +18,7 @@ import (
 	"github.com/fenelon/arc5/internal/config"
 	"github.com/fenelon/arc5/internal/connector"
 	"github.com/fenelon/arc5/internal/connector/demo"
+	"github.com/fenelon/arc5/internal/indexer"
 	"github.com/fenelon/arc5/internal/search"
 	"github.com/fenelon/arc5/internal/web"
 )
@@ -28,10 +28,6 @@ const usage = `usage:
   arc5 reindex [--data folder] [--languages en,ru,es]
   arc5 demo    [--data folder]`
 
-// languagesKey is the setting that holds the languages search stems, as
-// comma-separated codes.
-const languagesKey = "search_languages"
-
 // The demo connector is registered once per process, so the sample archive's
 // source shows as live.
 func init() { demo.Register(connector.Default) }
@@ -75,22 +71,6 @@ func run(ctx context.Context, args []string, getenv func(string) string, ready f
 	}
 }
 
-// loadAnalyzer builds the search analyzer for the languages in settings.
-func loadAnalyzer(store *archive.Store) (*search.Analyzer, error) {
-	setting, ok, err := store.Setting(languagesKey)
-	if err != nil {
-		return nil, err
-	}
-	if !ok {
-		setting = search.DefaultLanguages
-	}
-	analyzer, err := search.NewAnalyzer(search.ParseLanguages(setting))
-	if err != nil {
-		return nil, fmt.Errorf("%s setting: %w", languagesKey, err)
-	}
-	return analyzer, nil
-}
-
 func serve(ctx context.Context, cfg config.Config, ready func(addr string)) error {
 	store, err := archive.Open(cfg.DataDir)
 	if err != nil {
@@ -98,12 +78,7 @@ func serve(ctx context.Context, cfg config.Config, ready func(addr string)) erro
 	}
 	defer store.Close()
 
-	analyzer, err := loadAnalyzer(store)
-	if err != nil {
-		return err
-	}
-	store.SetAnalyzer(analyzer)
-	current, err := store.IndexCurrent()
+	index, err := indexer.Load(store)
 	if err != nil {
 		return err
 	}
@@ -117,26 +92,15 @@ func serve(ctx context.Context, cfg config.Config, ready func(addr string)) erro
 	// whichever way serve returns.
 	ctx, cancel := context.WithCancel(ctx)
 	runner := connector.NewRunner(store, connector.Default)
-	var background sync.WaitGroup
 	defer func() {
 		cancel()
 		runner.Wait()
-		background.Wait()
+		index.Wait()
 	}()
 
-	if !current {
-		background.Add(1)
-		go func() {
-			defer background.Done()
-			log.Printf("rebuilding the search index for %s", analyzer.Signature())
-			if err := store.Reindex(ctx, nil); err != nil {
-				if ctx.Err() == nil {
-					log.Printf("search index rebuild failed: %v", err)
-				}
-				return
-			}
-			log.Printf("search index rebuilt")
-		}()
+	if err := index.Start(ctx); err != nil {
+		ln.Close()
+		return err
 	}
 	if err := runner.Start(ctx); err != nil {
 		ln.Close()
@@ -181,21 +145,20 @@ func reindex(ctx context.Context, cfg config.Reindex) error {
 		if err != nil {
 			return err
 		}
-		if err := store.SetSetting(languagesKey, strings.Join(chosen.Languages(), ",")); err != nil {
+		if err := store.SetSetting(indexer.LanguagesKey, strings.Join(chosen.Languages(), ",")); err != nil {
 			return err
 		}
 	}
-	analyzer, err := loadAnalyzer(store)
+	index, err := indexer.Load(store)
 	if err != nil {
 		return err
 	}
-	store.SetAnalyzer(analyzer)
 
 	indexed := 0
 	if err := store.Reindex(ctx, func(done, total int) { indexed = done }); err != nil {
 		return err
 	}
-	languages := strings.Join(analyzer.Languages(), ", ")
+	languages := strings.Join(index.Status().Languages, ", ")
 	if languages == "" {
 		languages = "none"
 	}
@@ -211,12 +174,11 @@ func seedDemo(cfg config.Demo) error {
 	}
 	defer store.Close()
 
-	// With the analyzer set, the sample messages are indexed as they are written.
-	analyzer, err := loadAnalyzer(store)
-	if err != nil {
+	// Loading the indexer sets the analyzer, so the sample messages are
+	// indexed as they are written.
+	if _, err := indexer.Load(store); err != nil {
 		return err
 	}
-	store.SetAnalyzer(analyzer)
 
 	seeded, err := demo.Seed(store)
 	if err != nil {
````

`internal/indexer/indexer.go`:

````go
// Package indexer keeps the search index in step with the languages the
// owner chose. It holds the analyzer the archive indexes with and the
// searcher that reads the index, swaps both when the languages change, and
// rebuilds the index in the background.
package indexer

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"
	"sync"

	"github.com/fenelon/arc5/internal/archive"
	"github.com/fenelon/arc5/internal/search"
)

// LanguagesKey is the setting that holds the languages search stems, as
// comma-separated codes.
const LanguagesKey = "search_languages"

// ErrUnknownLanguage is returned for a language stemming is not available for.
var ErrUnknownLanguage = errors.New("unknown search language")

// Status says which languages the index is for and how a rebuild is going.
type Status struct {
	Languages  []string // codes, sorted
	Rebuilding bool
	Done       int    // messages indexed by the current or last rebuild
	Total      int    // messages to index in it
	Err        string // why the last rebuild failed, if it did
}

type Indexer struct {
	store *archive.Store
	// Logf records rebuilds. Defaults to log.Printf.
	Logf func(format string, args ...any)

	setMu    sync.Mutex // one language change at a time
	mu       sync.Mutex
	searcher *search.Searcher
	status   Status
	ctx      context.Context    // set by Start; bounds every rebuild
	cancel   context.CancelFunc // stops the rebuild in flight
	done     chan struct{}      // closed when the rebuild in flight ends
}

// Load reads the language setting and makes the archive index with it.
func Load(store *archive.Store) (*Indexer, error) {
	setting, ok, err := store.Setting(LanguagesKey)
	if err != nil {
		return nil, err
	}
	if !ok {
		setting = search.DefaultLanguages
	}
	analyzer, err := search.NewAnalyzer(search.ParseLanguages(setting))
	if err != nil {
		return nil, fmt.Errorf("%s setting: %w", LanguagesKey, err)
	}
	x := &Indexer{store: store, Logf: log.Printf, ctx: context.Background()}
	x.use(analyzer)
	return x, nil
}

// use makes analyzer the one the archive writes and the searcher reads with.
func (x *Indexer) use(analyzer *search.Analyzer) {
	x.store.SetAnalyzer(analyzer)
	x.mu.Lock()
	x.searcher = search.New(x.store.Reader(), analyzer)
	x.status.Languages = analyzer.Languages()
	x.mu.Unlock()
}

// Searcher returns the searcher for the languages now chosen.
func (x *Indexer) Searcher() *search.Searcher {
	x.mu.Lock()
	defer x.mu.Unlock()
	return x.searcher
}

func (x *Indexer) Status() Status {
	x.mu.Lock()
	defer x.mu.Unlock()
	st := x.status
	st.Languages = append([]string(nil), st.Languages...)
	return st
}

// Start rebuilds the index in the background if it was built for other
// languages or written to by a process that indexes differently. Rebuilds
// stop when ctx is cancelled; call Wait afterwards.
func (x *Indexer) Start(ctx context.Context) error {
	x.mu.Lock()
	x.ctx = ctx
	x.mu.Unlock()
	current, err := x.store.IndexCurrent()
	if err != nil || current {
		return err
	}
	x.rebuild()
	return nil
}

// SetLanguages saves a new language list, indexes new messages with it at
// once, and rebuilds the rest of the index in the background. A rebuild
// already running is stopped first. Searches keep working meanwhile, though
// they find only what has been indexed again so far.
func (x *Indexer) SetLanguages(codes []string) error {
	analyzer, err := search.NewAnalyzer(codes)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrUnknownLanguage, err)
	}
	x.setMu.Lock()
	defer x.setMu.Unlock()
	x.stop()
	if err := x.store.SetSetting(LanguagesKey, strings.Join(analyzer.Languages(), ",")); err != nil {
		return err
	}
	x.use(analyzer)
	x.rebuild()
	return nil
}

// stop cancels the rebuild in flight and waits for it to end.
func (x *Indexer) stop() {
	x.mu.Lock()
	cancel, done := x.cancel, x.done
	x.mu.Unlock()
	if cancel != nil {
		cancel()
		<-done
	}
}

// rebuild starts a background rebuild with the analyzer now set.
func (x *Indexer) rebuild() {
	x.mu.Lock()
	ctx, cancel := context.WithCancel(x.ctx)
	done := make(chan struct{})
	x.cancel, x.done = cancel, done
	x.status.Rebuilding, x.status.Done, x.status.Total, x.status.Err = true, 0, 0, ""
	languages := strings.Join(x.status.Languages, ",")
	x.mu.Unlock()

	go func() {
		defer close(done)
		x.Logf("rebuilding the search index for languages %q", languages)
		err := x.store.Reindex(ctx, func(n, total int) {
			x.mu.Lock()
			x.status.Done, x.status.Total = n, total
			x.mu.Unlock()
		})
		x.mu.Lock()
		defer x.mu.Unlock()
		x.status.Rebuilding = false
		switch {
		case err == nil:
			x.Logf("search index rebuilt")
		case ctx.Err() != nil:
			// Stopped for a newer rebuild or for shutdown.
		default:
			x.status.Err = err.Error()
			x.Logf("search index rebuild failed: %v", err)
		}
	}()
}

// Wait blocks until the rebuild in flight, if any, has ended.
func (x *Indexer) Wait() {
	x.mu.Lock()
	done := x.done
	x.mu.Unlock()
	if done != nil {
		<-done
	}
}
````

- [ ] **Step 4: Run the tests to verify they pass**

Run: `gofmt -l internal cmd; go vet ./... && go test -race -count=1 -timeout 180s ./...`
Expected: `gofmt -l` prints nothing; every package PASSES.

- [ ] **Step 5: Commit**

```bash
git add cmd/arc5 internal/indexer
git commit -m "Let the search languages change while the server runs"
```

---

### Task 3: The archive's side of the Sources page

The Sources page renames and removes sources, lists every chat of a source with its archive switch, and says when each source last received a message. The search column needs a person's name by ID.

**Files:**
- Create: `internal/archive/migrations/0004_sources.sql`
- Modify: `internal/archive/accounts.go`, `internal/archive/chats.go`
- Test: `internal/archive/db_test.go`, `internal/archive/sources_test.go` (new)

**Interfaces:**
- Produces:
  - migration `0004_sources.sql`: `accounts.removed_at INTEGER` (NULL unless removed)
  - `archive.Account.RemovedAt time.Time`
  - `(*Store).RenameAccount(id int64, name string) error` — tidies the name (no control characters, single spaces, at most 100 characters); `ErrInvalid` when empty, `ErrNotFound` for no account
  - `(*Store).RemoveAccount(id int64) error` — switches it off, sets `removed_at`, deletes its `connector_state`; keeps chats and messages
  - `(*Store).SetAccountEnabled` refuses to switch a removed account on (`ErrInvalid`)
  - `type archive.ChatEntry struct { ID int64; SourceID string; Kind model.ChatKind; Title string; Archived bool; Messages int; LastAt time.Time }`
  - `(*Store).AccountChats(accountID int64) ([]ChatEntry, error)` — every chat, most recently active first, then empty ones by title
  - `(*Store).SetChatsArchived(accountID int64, archived map[int64]bool) error` — by chat ID; other accounts' chats are ignored
  - `(*Store).LastMessageTimes() (map[int64]time.Time, error)` — per account, when its newest message was sent
  - `type archive.SenderInfo struct { ID, AccountID int64; DisplayName, Username string }`, `(*Store).Sender(id int64) (SenderInfo, error)`

- [ ] **Step 1: Write the failing tests**

Change `internal/archive/db_test.go`:

````diff
--- a/internal/archive/db_test.go
+++ b/internal/archive/db_test.go
@@ -88,8 +88,8 @@ func TestReopenKeepsDataAndAppliesMigrationsOnce(t *testing.T) {
 	if err := s.r.QueryRow(`SELECT COUNT(*) FROM settings WHERE key = 'probe'`).Scan(&probes); err != nil {
 		t.Fatal(err)
 	}
-	if migrations != 3 || probes != 1 {
-		t.Fatalf("migrations = %d, probes = %d, want 3 and 1", migrations, probes)
+	if migrations != 4 || probes != 1 {
+		t.Fatalf("migrations = %d, probes = %d, want 4 and 1", migrations, probes)
 	}
 }
 
````

`internal/archive/sources_test.go`:

````go
package archive

import (
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/fenelon/arc5/internal/model"
)

func TestRenameAccount(t *testing.T) {
	s := openTemp(t)
	acc := newAccount(t, s)
	if err := s.RenameAccount(acc, "  Work\u0000 phone "); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.Account(acc); got.Label != "Work phone" {
		t.Errorf("label = %q, want %q", got.Label, "Work phone")
	}
	for _, bad := range []string{"", "   "} {
		if err := s.RenameAccount(acc, bad); !errors.Is(err, ErrInvalid) {
			t.Errorf("RenameAccount(%q) = %v, want ErrInvalid", bad, err)
		}
	}
	if err := s.RenameAccount(9999, "x"); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown account: %v, want ErrNotFound", err)
	}
}

func TestRemoveAccountKeepsItsArchive(t *testing.T) {
	s := openTemp(t)
	acc := seedChat(t, s)
	mustUpsert(t, s, acc, textMsg("1", "kept"))
	if err := s.StateSet(acc, "session", []byte("secret")); err != nil {
		t.Fatal(err)
	}
	s.now = func() time.Time { return t0 }
	if err := s.RemoveAccount(acc); err != nil {
		t.Fatal(err)
	}
	got, err := s.Account(acc)
	if err != nil {
		t.Fatal(err)
	}
	if got.Enabled || !got.RemovedAt.Equal(t0) {
		t.Errorf("removed account = %+v; want it switched off and marked removed", got)
	}
	if v, _ := s.StateGet(acc, "session"); v != nil {
		t.Error("the connector's session survived removal")
	}
	if mustMessage(t, s, acc, "1").Text != "kept" {
		t.Error("the archive was not kept")
	}
	if err := s.RemoveAccount(9999); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown account: %v, want ErrNotFound", err)
	}
	if err := s.SetAccountEnabled(acc, true); !errors.Is(err, ErrInvalid) {
		t.Errorf("switching a removed account back on: %v, want ErrInvalid", err)
	}
}

func TestAccountChatsAndArchiving(t *testing.T) {
	s := openTemp(t)
	acc := seedChat(t, s) // c1, private, "Alice"
	other := newAccount(t, s)
	for _, c := range []model.Chat{
		{SourceID: "news", Kind: model.ChatChannel, Title: "News"},
		{SourceID: "g1", Kind: model.ChatGroup, Title: "Group"},
	} {
		if err := s.UpsertChat(acc, c); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.UpsertChat(other, model.Chat{SourceID: "x", Kind: model.ChatPrivate, Title: "Elsewhere"}); err != nil {
		t.Fatal(err)
	}
	mustUpsert(t, s, acc, textMsg("1", "hello"))
	later := textMsg("2", "in the group")
	later.ChatSourceID, later.SentAt = "g1", t0.Add(time.Hour)
	mustUpsert(t, s, acc, later)

	chats, err := s.AccountChats(acc)
	if err != nil {
		t.Fatal(err)
	}
	var titles []string
	for _, c := range chats {
		titles = append(titles, c.Title)
	}
	// Most recently active first; chats with nothing archived after, by title.
	if want := []string{"Group", "Alice", "News"}; !slices.Equal(titles, want) {
		t.Fatalf("chats = %v, want %v", titles, want)
	}
	if g := chats[0]; g.Kind != model.ChatGroup || !g.Archived || g.Messages != 1 || !g.LastAt.Equal(t0.Add(time.Hour)) {
		t.Errorf("group = %+v", g)
	}
	if n := chats[2]; n.Archived || n.Messages != 0 || !n.LastAt.IsZero() {
		t.Errorf("channel = %+v", n)
	}

	elsewhere, err := s.AccountChats(other)
	if err != nil || len(elsewhere) != 1 {
		t.Fatalf("other account's chats = %+v, %v", elsewhere, err)
	}
	changes := map[int64]bool{chats[0].ID: false, chats[2].ID: true, elsewhere[0].ID: false}
	if err := s.SetChatsArchived(acc, changes); err != nil {
		t.Fatal(err)
	}
	for source, want := range map[string]bool{"g1": false, "news": true, "c1": true} {
		if got, _ := s.ChatArchived(acc, source); got != want {
			t.Errorf("chat %s archived = %v, want %v", source, got, want)
		}
	}
	if got, _ := s.ChatArchived(other, "x"); !got {
		t.Error("a chat of another account was changed")
	}
}

func TestLastMessageTimes(t *testing.T) {
	s := openTemp(t)
	acc := seedChat(t, s)
	quiet := newAccount(t, s)
	mustUpsert(t, s, acc, textMsg("1", "first"))
	m := textMsg("2", "second")
	m.SentAt = t0.Add(time.Hour)
	mustUpsert(t, s, acc, m)

	got, err := s.LastMessageTimes()
	if err != nil {
		t.Fatal(err)
	}
	if !got[acc].Equal(t0.Add(time.Hour)) {
		t.Errorf("last message of the account = %v", got[acc])
	}
	if _, ok := got[quiet]; ok {
		t.Error("an account with no messages has a last message time")
	}
}

func TestSender(t *testing.T) {
	s := openTemp(t)
	acc := seedChat(t, s) // sender u1, "Alice"
	var id int64
	if err := s.r.QueryRow(`SELECT id FROM senders WHERE source_id = 'u1'`).Scan(&id); err != nil {
		t.Fatal(err)
	}
	got, err := s.Sender(id)
	if err != nil || got.ID != id || got.AccountID != acc || got.DisplayName != "Alice" {
		t.Fatalf("Sender = %+v, %v", got, err)
	}
	if _, err := s.Sender(9999); !errors.Is(err, ErrNotFound) {
		t.Errorf("unknown sender: %v, want ErrNotFound", err)
	}
}
````

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go vet ./internal/archive/; go test -count=1 ./internal/archive/`
Expected: FAIL to build: `s.RenameAccount undefined` (and the other new methods).

- [ ] **Step 3: Write the implementation**

Change `internal/archive/accounts.go`:

````diff
--- a/internal/archive/accounts.go
+++ b/internal/archive/accounts.go
@@ -1,11 +1,14 @@
 package archive
 
 import (
+	"context"
 	"database/sql"
 	"encoding/json"
 	"errors"
 	"fmt"
+	"strings"
 	"time"
+	"unicode"
 )
 
 // Account is one configured instance of a connector.
@@ -16,9 +19,10 @@ type Account struct {
 	Settings  map[string]string
 	Enabled   bool
 	CreatedAt time.Time
+	RemovedAt time.Time // zero unless the owner removed the source
 }
 
-const accountColumns = `id, connector, label, settings, enabled, created_at`
+const accountColumns = `id, connector, label, settings, enabled, created_at, removed_at`
 
 func encodeSettings(settings map[string]string) (string, error) {
 	if settings == nil {
@@ -34,8 +38,9 @@ func scanAccount(row interface{ Scan(dest ...any) error }) (Account, error) {
 		settings string
 		enabled  int
 		created  int64
+		removed  sql.NullInt64
 	)
-	if err := row.Scan(&a.ID, &a.Connector, &a.Label, &settings, &enabled, &created); err != nil {
+	if err := row.Scan(&a.ID, &a.Connector, &a.Label, &settings, &enabled, &created, &removed); err != nil {
 		return Account{}, err
 	}
 	a.Settings = map[string]string{}
@@ -44,6 +49,7 @@ func scanAccount(row interface{ Scan(dest ...any) error }) (Account, error) {
 	}
 	a.Enabled = enabled != 0
 	a.CreatedAt = time.Unix(created, 0).UTC()
+	a.RemovedAt = fromUnix(removed)
 	return a, nil
 }
 
@@ -96,10 +102,67 @@ func (s *Store) UpdateAccountSettings(id int64, settings map[string]string) erro
 	return s.updateAccount(id, `UPDATE accounts SET settings = ? WHERE id = ?`, body, id)
 }
 
+// SetAccountEnabled switches an account on or off. A removed account stays off.
 func (s *Store) SetAccountEnabled(id int64, enabled bool) error {
+	if enabled {
+		acc, err := s.Account(id)
+		if err != nil {
+			return err
+		}
+		if !acc.RemovedAt.IsZero() {
+			return fmt.Errorf("%w: account %d was removed", ErrInvalid, id)
+		}
+	}
 	return s.updateAccount(id, `UPDATE accounts SET enabled = ? WHERE id = ?`, b2i(enabled), id)
 }
 
+// maxLabelRunes keeps a source's name to what fits on its tile and more.
+const maxLabelRunes = 100
+
+// label tidies a name the owner typed: no control characters, single spaces,
+// no more than maxLabelRunes characters.
+func label(name string) string {
+	name = strings.Map(func(r rune) rune {
+		if unicode.IsControl(r) {
+			return -1
+		}
+		return r
+	}, clean(name))
+	name = strings.Join(strings.Fields(name), " ")
+	if r := []rune(name); len(r) > maxLabelRunes {
+		name = string(r[:maxLabelRunes])
+	}
+	return name
+}
+
+// RenameAccount changes the name the owner gave a source.
+func (s *Store) RenameAccount(id int64, name string) error {
+	name = label(name)
+	if name == "" {
+		return fmt.Errorf("%w: a source needs a name", ErrInvalid)
+	}
+	return s.updateAccount(id, `UPDATE accounts SET label = ? WHERE id = ?`, name, id)
+}
+
+// RemoveAccount switches a source off for good and forgets its connector's
+// state, which holds the session that grants access to the account. Its
+// chats and messages stay in the archive.
+func (s *Store) RemoveAccount(id int64) error {
+	return s.inTx(context.Background(), func(tx *sql.Tx) error {
+		res, err := tx.Exec(`UPDATE accounts SET enabled = 0, removed_at = COALESCE(removed_at, ?) WHERE id = ?`, s.now().Unix(), id)
+		if err != nil {
+			return err
+		}
+		if n, err := res.RowsAffected(); err != nil {
+			return err
+		} else if n == 0 {
+			return fmt.Errorf("account %d: %w", id, ErrNotFound)
+		}
+		_, err = tx.Exec(`DELETE FROM connector_state WHERE account_id = ?`, id)
+		return err
+	})
+}
+
 func (s *Store) updateAccount(id int64, query string, args ...any) error {
 	res, err := s.w.Exec(query, args...)
 	if err != nil {
````

Change `internal/archive/chats.go`:

````diff
--- a/internal/archive/chats.go
+++ b/internal/archive/chats.go
@@ -1,9 +1,11 @@
 package archive
 
 import (
+	"context"
 	"database/sql"
 	"errors"
 	"fmt"
+	"time"
 
 	"github.com/fenelon/arc5/internal/model"
 )
@@ -46,3 +48,102 @@ func (s *Store) ChatArchived(accountID int64, chatSourceID string) (bool, error)
 	}
 	return archived != 0, err
 }
+
+// ChatEntry is one chat of an account, as the owner sees it when choosing
+// which chats are archived.
+type ChatEntry struct {
+	ID       int64
+	SourceID string
+	Kind     model.ChatKind
+	Title    string
+	Archived bool
+	Messages int
+	LastAt   time.Time // zero when nothing from it is archived
+}
+
+// AccountChats lists every chat of an account, archived or not: the most
+// recently active first, then the ones with nothing archived, by title.
+func (s *Store) AccountChats(accountID int64) ([]ChatEntry, error) {
+	rows, err := s.r.Query(`
+		SELECT c.id, c.source_id, c.kind, c.title, c.archived,
+		       (SELECT COUNT(*) FROM messages m WHERE m.chat_id = c.id),
+		       (SELECT MAX(m.sent_at) FROM messages m WHERE m.chat_id = c.id) AS last
+		FROM chats c WHERE c.account_id = ?
+		ORDER BY last IS NULL, last DESC, c.title, c.id`, accountID)
+	if err != nil {
+		return nil, err
+	}
+	defer rows.Close()
+	var out []ChatEntry
+	for rows.Next() {
+		var (
+			c        ChatEntry
+			kind     string
+			archived int
+			last     sql.NullInt64
+		)
+		if err := rows.Scan(&c.ID, &c.SourceID, &kind, &c.Title, &archived, &c.Messages, &last); err != nil {
+			return nil, err
+		}
+		c.Kind, c.Archived, c.LastAt = model.ChatKind(kind), archived != 0, fromUnix(last)
+		out = append(out, c)
+	}
+	return out, rows.Err()
+}
+
+// SetChatsArchived switches archiving on or off for chats of one account, by
+// chat ID. IDs of other accounts' chats are ignored.
+func (s *Store) SetChatsArchived(accountID int64, archived map[int64]bool) error {
+	return s.inTx(context.Background(), func(tx *sql.Tx) error {
+		for id, on := range archived {
+			if _, err := tx.Exec(`UPDATE chats SET archived = ? WHERE id = ? AND account_id = ?`, b2i(on), id, accountID); err != nil {
+				return err
+			}
+		}
+		return nil
+	})
+}
+
+// LastMessageTimes returns, per account, when its newest archived message
+// was sent. Accounts with no messages are left out.
+func (s *Store) LastMessageTimes() (map[int64]time.Time, error) {
+	rows, err := s.r.Query(`
+		SELECT account_id, MAX(last) FROM (
+			SELECT c.account_id, (SELECT MAX(m.sent_at) FROM messages m WHERE m.chat_id = c.id) AS last
+			FROM chats c)
+		WHERE last IS NOT NULL GROUP BY account_id`)
+	if err != nil {
+		return nil, err
+	}
+	defer rows.Close()
+	out := map[int64]time.Time{}
+	for rows.Next() {
+		var (
+			id   int64
+			last int64
+		)
+		if err := rows.Scan(&id, &last); err != nil {
+			return nil, err
+		}
+		out[id] = time.Unix(last, 0).UTC()
+	}
+	return out, rows.Err()
+}
+
+// SenderInfo is one person who wrote in an account's chats.
+type SenderInfo struct {
+	ID          int64
+	AccountID   int64
+	DisplayName string
+	Username    string
+}
+
+func (s *Store) Sender(id int64) (SenderInfo, error) {
+	var sn SenderInfo
+	err := s.r.QueryRow(`SELECT id, account_id, display_name, username FROM senders WHERE id = ?`, id).
+		Scan(&sn.ID, &sn.AccountID, &sn.DisplayName, &sn.Username)
+	if errors.Is(err, sql.ErrNoRows) {
+		return SenderInfo{}, fmt.Errorf("sender %d: %w", id, ErrNotFound)
+	}
+	return sn, err
+}
````

`internal/archive/migrations/0004_sources.sql`:

````sql
-- When the owner removed the source. A removed account is switched off and
-- its connector's session is gone, but its messages stay in the archive.
ALTER TABLE accounts ADD COLUMN removed_at INTEGER;
````

- [ ] **Step 4: Run the tests to verify they pass**

Run: `gofmt -l internal cmd; go vet ./... && go test -race -count=1 -timeout 180s ./...`
Expected: `gofmt -l` prints nothing; every package PASSES.

- [ ] **Step 5: Commit**

```bash
git add internal/archive internal/archive/migrations
git commit -m "Rename and remove sources, list a source's chats, and switch their archiving"
```

---

### Task 4: Hidden connectors, checkbox fields, sign-out, and a demo that can log in

"Add a source" must not offer the demo, connector settings need checkboxes, and "Sign this source out" needs the connector's help. The demo connector gains example settings, a sign-out and a one-step login, so all of the Sources page can be tried on it.

**Files:**
- Modify: `internal/connector/connector.go`, `internal/connector/demo/demo.go`, `internal/connector/registry.go`, `internal/connector/runner.go`, `internal/connector/sink.go`
- Test: `internal/connector/demo/demo_test.go`, `internal/connector/registry_test.go`, `internal/connector/runner_test.go`, `internal/connector/sink_test.go`

**Interfaces:**
- Produces:
  - `connector.Info.Hidden bool` — not offered under "Add a source"
  - `(*Registry).Infos() []Info` — every connector, sorted by title
  - `connector.FieldBool FieldKind = "bool"` — value `"true"` when ticked, `""` when not
  - `type connector.SignOuter interface { SignOut(ctx, state State) error }`, `connector.ErrCannotSignOut`
  - `(*Runner).SignOut(ctx, accountID int64) error` — stops the connector, calls `SignOut` with the account's state, starts it again; `ErrCannotSignOut` if not implemented, `archive.ErrNotFound` for no account
  - `connector.NewState(store *archive.Store, accountID int64) State`
  - `SetupFlow.Next` documented: an error keeps the flow on its step and its text is shown; a flow may implement `io.Closer`
  - demo: `Info.Hidden`, two example settings (`speed` choice, `keep_ephemeral` bool), `SignOut`, a login asking for code `12345`

- [ ] **Step 1: Write the failing tests**

Change `internal/connector/demo/demo_test.go`:

````diff
--- a/internal/connector/demo/demo_test.go
+++ b/internal/connector/demo/demo_test.go
@@ -2,6 +2,7 @@ package demo
 
 import (
 	"context"
+	"errors"
 	"os"
 	"testing"
 	"time"
@@ -98,7 +99,7 @@ func TestDemoConnectorReportsLiveAndStopsWhenCancelled(t *testing.T) {
 	ctx, cancel := context.WithCancel(context.Background())
 	sink := &progressSink{reported: make(chan connector.Progress, 1)}
 	done := make(chan error, 1)
-	go func() { done <- c.Run(ctx, sink, nil, nil) }()
+	go func() { done <- c.Run(ctx, sink, memState{}, nil) }()
 	select {
 	case p := <-sink.reported:
 		if p.Phase != connector.PhaseLive {
@@ -124,3 +125,62 @@ type progressSink struct {
 }
 
 func (s *progressSink) ReportProgress(p connector.Progress) { s.reported <- p }
+
+// memState is a connector.State in memory.
+type memState map[string][]byte
+
+func (m memState) Get(key string) ([]byte, error)     { return m[key], nil }
+func (m memState) Set(key string, value []byte) error { m[key] = value; return nil }
+
+func TestDemoIsHiddenButHasSettingsAndALogin(t *testing.T) {
+	reg := connector.NewRegistry()
+	Register(reg)
+	info, _ := reg.Info(Name)
+	if !info.Hidden {
+		t.Error("the demo connector is offered as a source to add")
+	}
+	c, _ := reg.New(Name)
+	if len(c.Settings()) == 0 {
+		t.Error("the demo connector has no settings to show")
+	}
+	if _, ok := c.(connector.SignOuter); !ok {
+		t.Fatal("the demo connector cannot be signed out")
+	}
+
+	state := memState{}
+	if err := c.(connector.SignOuter).SignOut(context.Background(), state); err != nil {
+		t.Fatal(err)
+	}
+	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
+	defer cancel()
+	sink := &progressSink{reported: make(chan connector.Progress, 1)}
+	if err := c.Run(ctx, sink, state, nil); !errors.Is(err, connector.ErrNeedsLogin) {
+		t.Fatalf("Run after signing out = %v, want ErrNeedsLogin", err)
+	}
+
+	flow := c.Setup(state)
+	step, err := flow.Next(ctx, nil)
+	if err != nil || step.Done || len(step.Fields) != 1 || step.Fields[0].Name != "code" {
+		t.Fatalf("first step = %+v, %v", step, err)
+	}
+	if _, err := flow.Next(ctx, map[string]string{"code": "99999"}); err == nil {
+		t.Fatal("a wrong code was accepted")
+	}
+	step, err = flow.Next(ctx, map[string]string{"code": " 12345 "})
+	if err != nil || !step.Done {
+		t.Fatalf("after the right code: %+v, %v", step, err)
+	}
+	runCtx, stop := context.WithCancel(ctx)
+	done := make(chan error, 1)
+	go func() { done <- c.Run(runCtx, sink, state, nil) }()
+	select {
+	case p := <-sink.reported:
+		if p.Phase != connector.PhaseLive {
+			t.Errorf("after logging in again: %+v", p)
+		}
+	case err := <-done:
+		t.Fatalf("Run after logging in again returned %v", err)
+	}
+	stop()
+	<-done
+}
````

Change `internal/connector/registry_test.go`:

````diff
--- a/internal/connector/registry_test.go
+++ b/internal/connector/registry_test.go
@@ -63,3 +63,17 @@ func TestRegisterRejectsBadInfo(t *testing.T) {
 		})
 	}
 }
+
+func TestInfosListsEveryConnectorByTitle(t *testing.T) {
+	reg := NewRegistry()
+	reg.Register(Info{Name: "zeta", Title: "Zeta", Display: DisplayChats}, func() Connector { return stubConnector{} })
+	reg.Register(Info{Name: "alpha", Title: "Mail", Display: DisplayMail}, func() Connector { return stubConnector{} })
+	reg.Register(Info{Name: "demo", Title: "Demo", Display: DisplayChats, Hidden: true}, func() Connector { return stubConnector{} })
+	var titles []string
+	for _, info := range reg.Infos() {
+		titles = append(titles, info.Title)
+	}
+	if want := []string{"Demo", "Mail", "Zeta"}; !slices.Equal(titles, want) {
+		t.Fatalf("Infos titles = %v, want %v", titles, want)
+	}
+}
````

Change `internal/connector/runner_test.go`:

````diff
--- a/internal/connector/runner_test.go
+++ b/internal/connector/runner_test.go
@@ -491,3 +491,59 @@ func TestRunnerLogsFailuresWithPanicStack(t *testing.T) {
 		}
 	}
 }
+
+// signingOut is a connector that can end its session: SignOut records it in
+// state, and Run then reports that it needs login.
+type signingOut struct{ stubConnector }
+
+func (signingOut) SignOut(_ context.Context, state State) error {
+	return state.Set("signed_out", []byte("1"))
+}
+
+func TestSignOutEndsTheSessionAndRestarts(t *testing.T) {
+	store := openStore(t)
+	acc := newAccount(t, store)
+	reg := NewRegistry()
+	run := func(ctx context.Context, _ Sink, state State, _ map[string]string) error {
+		if v, _ := state.Get("signed_out"); len(v) > 0 {
+			return ErrNeedsLogin
+		}
+		return blockUntilCancelled(ctx)
+	}
+	reg.Register(stubInfo("stub"), func() Connector { return signingOut{stubConnector{run: run}} })
+	r := NewRunner(store, reg)
+	r.Logf = func(string, ...any) {}
+	ctx, cancel := context.WithCancel(context.Background())
+	t.Cleanup(func() { cancel(); r.Wait() })
+	if err := r.Start(ctx); err != nil {
+		t.Fatal(err)
+	}
+	eventually(t, "the connector to run", func() bool { return statusOf(r, acc).State == StateRunning })
+
+	if err := r.SignOut(ctx, acc); err != nil {
+		t.Fatal(err)
+	}
+	eventually(t, "the connector to need login", func() bool { return statusOf(r, acc).State == StateNeedsLogin })
+	if v, _ := store.StateGet(acc, "signed_out"); string(v) != "1" {
+		t.Errorf("SignOut did not get the account's state: %q", v)
+	}
+	if err := r.SignOut(ctx, 9999); !errors.Is(err, archive.ErrNotFound) {
+		t.Errorf("unknown account: %v, want ErrNotFound", err)
+	}
+}
+
+func TestSignOutNeedsAConnectorThatCan(t *testing.T) {
+	r, _, acc, ctx := newTestRunner(t, func(ctx context.Context, _ Sink, _ State, _ map[string]string) error {
+		return blockUntilCancelled(ctx)
+	})
+	if err := r.Start(ctx); err != nil {
+		t.Fatal(err)
+	}
+	eventually(t, "the connector to run", func() bool { return statusOf(r, acc).State == StateRunning })
+	if err := r.SignOut(ctx, acc); !errors.Is(err, ErrCannotSignOut) {
+		t.Fatalf("SignOut on a connector without it = %v, want ErrCannotSignOut", err)
+	}
+	if st := statusOf(r, acc).State; st != StateRunning {
+		t.Errorf("the connector was disturbed: %s", st)
+	}
+}
````

Change `internal/connector/sink_test.go`:

````diff
--- a/internal/connector/sink_test.go
+++ b/internal/connector/sink_test.go
@@ -117,3 +117,17 @@ func TestStateIsBoundToItsAccount(t *testing.T) {
 		t.Fatalf("b.Get = %q, %v; want nil", v, err)
 	}
 }
+
+func TestNewStateIsTheAccountsState(t *testing.T) {
+	store := openStore(t)
+	acc := newAccount(t, store)
+	if err := NewState(store, acc).Set("session", []byte("abc")); err != nil {
+		t.Fatal(err)
+	}
+	if v, _ := store.StateGet(acc, "session"); string(v) != "abc" {
+		t.Fatalf("stored state = %q", v)
+	}
+	if v, _ := NewState(store, acc).Get("session"); string(v) != "abc" {
+		t.Fatalf("read back = %q", v)
+	}
+}
````

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go vet ./internal/connector/...; go test -count=1 ./internal/connector/...`
Expected: FAIL to build: `unknown field Hidden in struct literal of type Info`, `reg.Infos undefined`, `r.SignOut undefined`, `undefined: ErrCannotSignOut`, `undefined: NewState`; the demo package fails to build too.

- [ ] **Step 3: Write the implementation**

Change `internal/connector/connector.go`:

````diff
--- a/internal/connector/connector.go
+++ b/internal/connector/connector.go
@@ -57,10 +57,25 @@ type State interface {
 // SetupFlow walks the owner through connecting an account.
 type SetupFlow interface {
 	// Next submits the previous step's input (nil for the first call) and
-	// returns the next step. A step with Done set ends the flow.
+	// returns the next step. A step with Done set ends the flow. An error
+	// means the input was not accepted: the flow stays on the step it was
+	// on, and the interface shows the error's text and asks again, so the
+	// text should say what to do. ctx ends with the request that submitted
+	// the step. A flow that holds something open between steps, such as a
+	// connection, implements io.Closer: the interface closes it when the flow
+	// ends, is cancelled, or is left unfinished for 30 minutes.
 	Next(ctx context.Context, input map[string]string) (Step, error)
 }
 
+// SignOuter is implemented by connectors that can end their session at the
+// source, such as logging out of Telegram. The interface offers to sign a
+// source out only when its connector implements it. The runner stops the
+// connector first and starts it again afterwards, when it should report
+// ErrNeedsLogin.
+type SignOuter interface {
+	SignOut(ctx context.Context, state State) error
+}
+
 type Step struct {
 	Title  string
 	Help   string
@@ -75,6 +90,7 @@ const (
 	FieldSecret FieldKind = "secret"
 	FieldNumber FieldKind = "number"
 	FieldChoice FieldKind = "choice"
+	FieldBool   FieldKind = "bool" // a checkbox; its value is "true" when ticked and "" when not
 )
 
 type Field struct {
@@ -109,4 +125,7 @@ var (
 	ErrNeedsLogin = errors.New("account needs login")
 	// ErrStorage wraps archive failures a connector cannot fix, such as a full disk.
 	ErrStorage = errors.New("archive storage failed")
+	// ErrCannotSignOut is returned by Runner.SignOut for a connector that
+	// does not implement SignOuter.
+	ErrCannotSignOut = errors.New("this kind of source cannot be signed out")
 )
````

Change `internal/connector/demo/demo.go`:

````diff
--- a/internal/connector/demo/demo.go
+++ b/internal/connector/demo/demo.go
@@ -5,10 +5,12 @@ package demo
 import (
 	"bytes"
 	"context"
+	"errors"
 	"fmt"
 	"image"
 	"image/color"
 	"image/png"
+	"strings"
 	"time"
 
 	"github.com/fenelon/arc5/internal/archive"
@@ -19,19 +21,66 @@ import (
 // Name is the connector name stored on demo accounts.
 const Name = "demo"
 
+// signedOutKey is set in the account's state while the demo is signed out.
+const signedOutKey = "signed_out"
+
+// loginCode is what the demo's login asks for.
+const loginCode = "12345"
+
+// idle captures nothing. It reports itself live, or needing login after it
+// was signed out, so every part of the Sources page can be tried on the demo.
 type idle struct{}
 
-func (idle) Run(ctx context.Context, sink connector.Sink, _ connector.State, _ map[string]string) error {
+func (idle) Run(ctx context.Context, sink connector.Sink, state connector.State, _ map[string]string) error {
+	if v, err := state.Get(signedOutKey); err != nil || len(v) > 0 {
+		return errors.Join(connector.ErrNeedsLogin, err)
+	}
 	sink.ReportProgress(connector.Progress{Phase: connector.PhaseLive})
 	<-ctx.Done()
 	return ctx.Err()
 }
-func (idle) Setup(connector.State) connector.SetupFlow { return nil }
-func (idle) Settings() []connector.Field               { return nil }
 
-// Register adds the demo connector, so demo accounts show as live.
+func (idle) Setup(state connector.State) connector.SetupFlow { return &login{state: state} }
+
+// Settings are examples of each kind of field. The demo ignores them.
+func (idle) Settings() []connector.Field {
+	return []connector.Field{
+		{Name: "speed", Label: "Speed", Help: "How quickly history is fetched. The demo has none to fetch.",
+			Kind: connector.FieldChoice, Choices: []string{"Slow", "Normal", "Fast"}, Default: "Normal"},
+		{Name: "keep_ephemeral", Label: "Keep self-destructing and view-once media",
+			Help: "Off unless you turn it on. The demo has none.", Kind: connector.FieldBool},
+	}
+}
+
+func (idle) SignOut(_ context.Context, state connector.State) error {
+	return state.Set(signedOutKey, []byte("1"))
+}
+
+// login asks for a code, the way a real source asks for one sent by text.
+type login struct{ state connector.State }
+
+func (l *login) Next(_ context.Context, input map[string]string) (connector.Step, error) {
+	ask := connector.Step{
+		Title:  "Sign in to the demo",
+		Help:   "A real source sends a code to your phone. The demo's code is " + loginCode + ".",
+		Fields: []connector.Field{{Name: "code", Label: "Login code", Kind: connector.FieldText}},
+	}
+	if input == nil {
+		return ask, nil
+	}
+	if strings.TrimSpace(input["code"]) != loginCode {
+		return ask, fmt.Errorf("That code is not right. The demo's code is %s.", loginCode)
+	}
+	if err := l.state.Set(signedOutKey, nil); err != nil {
+		return ask, err
+	}
+	return connector.Step{Done: true}, nil
+}
+
+// Register adds the demo connector, so demo accounts show as live. It is
+// hidden: "Add a source" does not offer it.
 func Register(reg *connector.Registry) {
-	reg.Register(connector.Info{Name: Name, Title: "Demo", Display: connector.DisplayChats}, func() connector.Connector { return idle{} })
+	reg.Register(connector.Info{Name: Name, Title: "Demo", Display: connector.DisplayChats, Hidden: true}, func() connector.Connector { return idle{} })
 }
 
 // day is the last day of the sample conversations.
````

Change `internal/connector/registry.go`:

````diff
--- a/internal/connector/registry.go
+++ b/internal/connector/registry.go
@@ -18,6 +18,7 @@ type Info struct {
 	Name    string // stored on each account, such as "telegram"
 	Title   string // shown to the owner, such as "Telegram"
 	Display Display
+	Hidden  bool // not offered when the owner adds a source, such as the demo
 }
 
 // Factory builds a fresh connector for one run.
@@ -85,3 +86,15 @@ func (r *Registry) Names() []string {
 	sort.Strings(names)
 	return names
 }
+
+// Infos describes every registered connector, sorted by title.
+func (r *Registry) Infos() []Info {
+	r.mu.RLock()
+	defer r.mu.RUnlock()
+	out := make([]Info, 0, len(r.entries))
+	for _, e := range r.entries {
+		out = append(out, e.info)
+	}
+	sort.Slice(out, func(i, j int) bool { return out[i].Title < out[j].Title })
+	return out
+}
````

Change `internal/connector/runner.go`:

````diff
--- a/internal/connector/runner.go
+++ b/internal/connector/runner.go
@@ -131,7 +131,40 @@ func (r *Runner) launch(acc archive.Account) {
 func (r *Runner) Restart(accountID int64) error {
 	r.restartMu.Lock()
 	defer r.restartMu.Unlock()
+	return r.restart(accountID, nil)
+}
+
+// SignOut stops an account's connector, has it end its session at the
+// source, and starts it again, when it reports that it needs login. It
+// returns ErrCannotSignOut if the connector has no way to sign out.
+func (r *Runner) SignOut(ctx context.Context, accountID int64) error {
+	acc, err := r.store.Account(accountID)
+	if err != nil {
+		return err
+	}
+	c, ok := r.reg.New(acc.Connector)
+	if !ok {
+		return ErrCannotSignOut
+	}
+	so, ok := c.(SignOuter)
+	if !ok {
+		return ErrCannotSignOut
+	}
+	r.restartMu.Lock()
+	defer r.restartMu.Unlock()
+	var signOutErr error
+	err = r.restart(accountID, func() {
+		signOutErr = so.SignOut(ctx, accountState{store: r.store, accountID: accountID})
+	})
+	if signOutErr != nil {
+		return signOutErr
+	}
+	return err
+}
 
+// restart stops an account's connector, runs between while it is stopped,
+// and starts it again if the account is enabled. restartMu must be held.
+func (r *Runner) restart(accountID int64, between func()) error {
 	r.mu.Lock()
 	s := r.accounts[accountID]
 	delete(r.accounts, accountID)
@@ -141,6 +174,9 @@ func (r *Runner) Restart(accountID int64) error {
 		s.cancel()
 		<-s.done
 	}
+	if between != nil {
+		between()
+	}
 	if stopping {
 		return nil
 	}
````

Change `internal/connector/sink.go`:

````diff
--- a/internal/connector/sink.go
+++ b/internal/connector/sink.go
@@ -76,6 +76,11 @@ func (s *accountSink) RequestedMedia() <-chan model.AttachmentRef { return s.req
 
 func (s *accountSink) ReportProgress(p Progress) { s.progress(p) }
 
+// NewState returns an account's state, for a setup flow run outside the runner.
+func NewState(store *archive.Store, accountID int64) State {
+	return accountState{store: store, accountID: accountID}
+}
+
 // accountState binds connector_state to one account.
 type accountState struct {
 	store     *archive.Store
````

- [ ] **Step 4: Run the tests to verify they pass**

Run: `gofmt -l internal cmd; go vet ./... && go test -race -count=1 -timeout 180s ./...`
Expected: `gofmt -l` prints nothing; every package PASSES.

- [ ] **Step 5: Commit**

```bash
git add internal/connector internal/connector/demo
git commit -m "Hide the demo from new sources, add checkbox settings and sign-out, and let the demo log in"
```

---

### Task 5: The Settings page

Settings has the theme, the search languages, the attachment size limit and the password, as in the dark Settings mockup. The top bar's "Sign out" becomes "Settings" (signing out moves to the bottom of Settings), and the picker remembers the last source opened.

**Files:**
- Create: `internal/web/settings.go`, `internal/web/templates/settings.html`
- Modify: `cmd/arc5/main.go`, `internal/web/chats.go`, `internal/web/server.go`, `internal/web/sources.go`, `internal/web/static/app.css`, `internal/web/static/js/jump_controller.js`, `internal/web/templates/partials.html`
- Test: `internal/web/server_test.go`, `internal/web/settings_test.go` (new)

**Interfaces:**
- Consumes: `indexer.Status`, `(*Indexer).SetLanguages`, `ErrUnknownLanguage` (Task 2); `search.Languages`; `archive.Store` (`MediaCap`, `SetMediaCap`); `auth.login`, `auth.setPassword`, `auth.startSession`.
- Produces:
  - `type web.Index interface { Searcher() *search.Searcher; Status() indexer.Status; SetLanguages(codes []string) error }`
  - `web.Deps.Index Index`; `web.New` returns an error when `Store`, `Runner`, `Registry` or `Index` is nil
  - routes `GET /settings`, `POST /settings/appearance`, `POST /settings/languages`, `POST /settings/attachments`, `POST /settings/password`
  - template `settings`; the head carries `<meta name="csrf">` on signed-in pages
  - cookie `arc5_source` (`remember`, `(*Server).remembered`); `chrome` marks the remembered source when given none; home opens it
  - test helpers: `site.index *indexer.Indexer`, `waitForIndex`; `csrfField` also reads the meta tag

- [ ] **Step 1: Write the failing tests**

Change `internal/web/server_test.go`:

````diff
--- a/internal/web/server_test.go
+++ b/internal/web/server_test.go
@@ -12,6 +12,7 @@ import (
 
 	"github.com/fenelon/arc5/internal/archive"
 	"github.com/fenelon/arc5/internal/connector"
+	"github.com/fenelon/arc5/internal/indexer"
 )
 
 type fakeRunner struct {
@@ -33,6 +34,7 @@ type site struct {
 	store   *archive.Store
 	runner  *fakeRunner
 	reg     *connector.Registry
+	index   *indexer.Indexer
 	srv     *Server
 	cookies map[string]*http.Cookie
 	csrf    string
@@ -45,15 +47,21 @@ func newSite(t *testing.T, envPassword string) *site {
 		t.Fatal(err)
 	}
 	t.Cleanup(func() { store.Close() })
-	s := &site{t: t, store: store, runner: &fakeRunner{}, reg: connector.NewRegistry(), cookies: map[string]*http.Cookie{}}
-	s.srv, err = New(Deps{Store: store, Runner: s.runner, Registry: s.reg, EnvPassword: envPassword, Location: time.UTC})
+	index, err := indexer.Load(store)
+	if err != nil {
+		t.Fatal(err)
+	}
+	index.Logf = func(string, ...any) {}
+	t.Cleanup(index.Wait) // registered after the store's cleanup, so it runs first
+	s := &site{t: t, store: store, runner: &fakeRunner{}, reg: connector.NewRegistry(), index: index, cookies: map[string]*http.Cookie{}}
+	s.srv, err = New(Deps{Store: store, Runner: s.runner, Registry: s.reg, Index: index, EnvPassword: envPassword, Location: time.UTC})
 	if err != nil {
 		t.Fatal(err)
 	}
 	return s
 }
 
-var csrfField = regexp.MustCompile(`name="csrf" value="([^"]+)"`)
+var csrfField = regexp.MustCompile(`name="csrf" (?:value|content)="([^"]+)"`)
 
 func (s *site) do(req *http.Request) *httptest.ResponseRecorder {
 	s.t.Helper()
````

`internal/web/settings_test.go`:

````go
package web

import (
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestTopBarLinksToSettings(t *testing.T) {
	s := newSite(t, "")
	s.signIn()
	acc := s.account("Personal", "telegram")
	s.say(acc, "Dan Whitfield", "1", "hello", noon)
	for _, path := range []string{"/s/chats", "/sources", "/settings"} {
		body := s.page(path)
		has(t, body, `href="/settings"`)
		topbar := body[strings.Index(body, `<header class="topbar"`):strings.Index(body, "</header>")]
		if strings.Contains(topbar, `action="/logout"`) {
			t.Errorf("%s: the top bar still signs out; that moved to Settings", path)
		}
	}
}

func TestSettingsPage(t *testing.T) {
	s := newSite(t, "")
	s.signIn()
	body := s.page("/settings")
	has(t, body, "<h1>Settings</h1>", "Appearance", `name="theme" value="" checked`,
		"Search languages", `name="lang" value="en" checked`, `name="lang" value="ru">`, "Russian", "Turkish",
		"Save and rebuild the index", "The index is up to date for English.",
		"Largest automatic download, in MB", `name="limit_mb" type="number" min="0" max="100000" value="50"`,
		`name="current"`, `name="new"`, "Change password",
		`href="/sources">Manage sources`, `action="/logout"`)
	if n := strings.Count(body, `name="lang"`); n != 18 {
		t.Errorf("%d language checkboxes, want 18", n)
	}
	// Languages are listed by name, as people look for them.
	if strings.Index(body, ">Dutch<") > strings.Index(body, ">English<") || strings.Index(body, ">Spanish<") > strings.Index(body, ">Swedish<") {
		t.Error("languages are not in the order of their names")
	}
	// The theme applies as soon as it is chosen; "Save" is for browsers without scripts.
	has(t, body, `<form method="post" action="/settings/appearance" class="choices" data-controller="jump">`,
		`name="theme" value="dark" data-action="jump#go"`, `data-jump-target="button">Save</button>`)
}

func TestSettingsAppearance(t *testing.T) {
	s := newSite(t, "")
	s.signIn()
	if rec := s.post("/settings/appearance", url.Values{"theme": {"dark"}}); !redirectsTo(rec, "/settings?saved=appearance") {
		t.Fatalf("saving the theme: %d to %q", rec.Code, rec.Header().Get("Location"))
	}
	if c := s.cookies["arc5_theme"]; c == nil || c.Value != "dark" {
		t.Fatalf("theme cookie = %+v", c)
	}
	has(t, s.page("/settings?saved=appearance"), `data-theme="dark"`, `name="theme" value="dark" checked`, "Saved.")
	s.post("/settings/appearance", url.Values{"theme": {""}})
	if _, set := s.cookies["arc5_theme"]; set {
		t.Error("following the device kept the theme cookie")
	}
	s.post("/settings/appearance", url.Values{"theme": {`"><script>`}})
	if _, set := s.cookies["arc5_theme"]; set {
		t.Error("a made-up theme was stored")
	}
}

func TestSettingsLanguages(t *testing.T) {
	s := newSite(t, "")
	s.signIn()
	rec := s.post("/settings/languages", url.Values{"lang": {"ru", "en"}})
	if !redirectsTo(rec, "/settings?saved=languages#languages") {
		t.Fatalf("saving languages: %d to %q", rec.Code, rec.Header().Get("Location"))
	}
	waitForIndex(t, s)
	if got := s.index.Status().Languages; !slices.Equal(got, []string{"en", "ru"}) {
		t.Fatalf("languages = %v", got)
	}
	has(t, s.page("/settings"), `name="lang" value="ru" checked`, "The index is up to date for English and Russian.")

	bad := s.post("/settings/languages", url.Values{"lang": {"en", "klingon"}})
	if bad.Code != http.StatusUnprocessableEntity || !strings.Contains(bad.Body.String(), `role="alert"`) {
		t.Errorf("an unknown language: %d", bad.Code)
	}
	if got := s.index.Status().Languages; !slices.Equal(got, []string{"en", "ru"}) {
		t.Errorf("a refused change altered the languages: %v", got)
	}

	s.post("/settings/languages", nil)
	waitForIndex(t, s)
	has(t, s.page("/settings"), "No languages are ticked, so search finds exact words only.")
}

func TestSettingsAttachmentLimit(t *testing.T) {
	s := newSite(t, "")
	s.signIn()
	if rec := s.post("/settings/attachments", url.Values{"limit_mb": {"10"}}); !redirectsTo(rec, "/settings?saved=attachments#attachments") {
		t.Fatalf("saving the limit: %d to %q", rec.Code, rec.Header().Get("Location"))
	}
	if limit, _ := s.store.MediaCap(); limit != 10<<20 {
		t.Fatalf("media cap = %d", limit)
	}
	has(t, s.page("/settings"), `value="10"`)
	for _, bad := range []string{"", "ten", "-1", "1.5", "100001"} {
		rec := s.post("/settings/attachments", url.Values{"limit_mb": {bad}})
		if rec.Code != http.StatusUnprocessableEntity || !strings.Contains(rec.Body.String(), `role="alert"`) {
			t.Errorf("limit %q: %d", bad, rec.Code)
		}
	}
	if limit, _ := s.store.MediaCap(); limit != 10<<20 {
		t.Errorf("a refused limit changed the cap to %d", limit)
	}
}

func TestSettingsPassword(t *testing.T) {
	s := newSite(t, "")
	s.signIn()
	other := &site{t: t, store: s.store, srv: s.srv, cookies: map[string]*http.Cookie{}}
	if rec := other.post("/login", url.Values{"password": {testPassword}}); !redirectsTo(rec, "/") {
		t.Fatalf("second browser login: %d", rec.Code)
	}

	for name, f := range map[string]url.Values{
		"wrong current": {"current": {"wrong horse"}, "new": {"battery staple"}},
		"short new":     {"current": {testPassword}, "new": {"short"}},
	} {
		rec := s.post("/settings/password", f)
		if rec.Code != http.StatusUnprocessableEntity || !strings.Contains(rec.Body.String(), `role="alert"`) {
			t.Errorf("%s: %d", name, rec.Code)
		}
		if strings.Contains(rec.Body.String(), "battery staple") || strings.Contains(rec.Body.String(), "wrong horse") {
			t.Errorf("%s: the page echoes a password", name)
		}
	}

	rec := s.post("/settings/password", url.Values{"current": {testPassword}, "new": {"battery staple"}})
	if !redirectsTo(rec, "/settings?saved=password#password") {
		t.Fatalf("changing the password: %d to %q", rec.Code, rec.Header().Get("Location"))
	}
	if body := s.page("/settings?saved=password"); !strings.Contains(body, "Every other browser is signed out.") {
		t.Error("the page does not confirm the change")
	}
	if rec := other.get("/"); !redirectsTo(rec, "/login") {
		t.Errorf("the other browser is still signed in: %d", rec.Code)
	}
	if ok, _ := s.srv.auth.check("battery staple"); !ok {
		t.Error("the new password does not work")
	}
}

func TestSettingsPasswordFromTheEnvironment(t *testing.T) {
	s := newSite(t, "from-env")
	if rec := s.post("/login", url.Values{"password": {"from-env"}}); !redirectsTo(rec, "/") {
		t.Fatalf("login: %d", rec.Code)
	}
	body := s.page("/settings")
	has(t, body, "ARC5_PASSWORD")
	lacks(t, body, `name="current"`)
	rec := s.post("/settings/password", url.Values{"current": {"from-env"}, "new": {"battery staple"}})
	if rec.Code != http.StatusUnprocessableEntity {
		t.Errorf("changing an environment password: %d", rec.Code)
	}
}

// The picker remembers the last source opened, for Settings, Sources and home.
func TestPickerRemembersTheSource(t *testing.T) {
	s := newSite(t, "")
	s.signIn()
	personal := s.account("Personal", "telegram")
	work := s.account("Work", "telegram")
	s.say(personal, "Dan", "1", "hello", noon)
	s.say(work, "Ana", "1", "hola", noon)
	if rec := s.get("/"); !redirectsTo(rec, "/s/chats") {
		t.Fatalf("home before any source was opened: %q", rec.Header().Get("Location"))
	}
	s.page(fmt.Sprintf("/s/%d", work))
	if rec := s.get("/"); !redirectsTo(rec, fmt.Sprintf("/s/%d", work)) {
		t.Errorf("home after opening Work: %q", rec.Header().Get("Location"))
	}
	has(t, s.page("/settings"), fmt.Sprintf(`class="tile tile--current" href="/s/%d"`, work))

	s.cookies["arc5_source"] = &http.Cookie{Name: "arc5_source", Value: "9999"}
	if rec := s.get("/"); !redirectsTo(rec, "/s/chats") {
		t.Errorf("home with a source that is gone: %q", rec.Header().Get("Location"))
	}
}

// waitForIndex waits until no rebuild is running.
func waitForIndex(t *testing.T, s *site) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for s.index.Status().Rebuilding {
		if time.Now().After(deadline) {
			t.Fatal("the index rebuild never finished")
		}
		time.Sleep(5 * time.Millisecond)
	}
}
````

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go vet ./internal/web/; go test -count=1 ./internal/web/`
Expected: FAIL to build: `unknown field Index in struct literal of type Deps`.

- [ ] **Step 3: Write the implementation**

Change `cmd/arc5/main.go`:

````diff
--- a/cmd/arc5/main.go
+++ b/cmd/arc5/main.go
@@ -107,7 +107,7 @@ func serve(ctx context.Context, cfg config.Config, ready func(addr string)) erro
 		return err
 	}
 
-	site, err := web.New(web.Deps{Store: store, Runner: runner, Registry: connector.Default, EnvPassword: cfg.Password})
+	site, err := web.New(web.Deps{Store: store, Runner: runner, Registry: connector.Default, Index: index, EnvPassword: cfg.Password})
 	if err != nil {
 		ln.Close()
 		return err
````

Change `internal/web/chats.go`:

````diff
--- a/internal/web/chats.go
+++ b/internal/web/chats.go
@@ -145,6 +145,7 @@ func (s *Server) sourceShell(w http.ResponseWriter, r *http.Request, sess archiv
 		s.fail(w, err)
 		return sourcePage{}, selection{}, false
 	}
+	remember(w, r, sel.Key)
 	data := sourcePage{Page: s.chrome(r, sess, sel.Name, all, sel.Key), Chats: rows}
 	if more {
 		data.MoreChatsHref = fmt.Sprintf("%s?chats=%d", r.URL.Path, count+chatsPerPage)
````

Change `internal/web/server.go`:

````diff
--- a/internal/web/server.go
+++ b/internal/web/server.go
@@ -17,6 +17,8 @@ import (
 
 	"github.com/fenelon/arc5/internal/archive"
 	"github.com/fenelon/arc5/internal/connector"
+	"github.com/fenelon/arc5/internal/indexer"
+	"github.com/fenelon/arc5/internal/search"
 )
 
 //go:embed static templates
@@ -26,6 +28,14 @@ var files embed.FS
 // nothing inline. Message text can never run as code.
 const csp = "default-src 'none'; script-src 'self'; style-src 'self'; img-src 'self'; media-src 'self'; font-src 'self'; connect-src 'self'; form-action 'self'; base-uri 'none'; frame-ancestors 'none'"
 
+// Index is what the interface needs from the search index: a searcher, the
+// state of the index, and a way to change its languages.
+type Index interface {
+	Searcher() *search.Searcher
+	Status() indexer.Status
+	SetLanguages(codes []string) error
+}
+
 // Runner is what the interface needs from the connector runner.
 type Runner interface {
 	Status() []connector.Status
@@ -36,6 +46,7 @@ type Deps struct {
 	Store       *archive.Store
 	Runner      Runner
 	Registry    *connector.Registry
+	Index       Index
 	EnvPassword string           // ARC5_PASSWORD; empty lets the owner choose one
 	Now         func() time.Time // defaults to time.Now
 	Location    *time.Location   // times are shown in this zone; defaults to time.Local
@@ -51,6 +62,9 @@ type Server struct {
 }
 
 func New(deps Deps) (*Server, error) {
+	if deps.Store == nil || deps.Runner == nil || deps.Registry == nil || deps.Index == nil {
+		return nil, errors.New("web: Deps needs a store, a runner, a registry and an index")
+	}
 	if deps.Now == nil {
 		deps.Now = time.Now
 	}
@@ -91,6 +105,11 @@ func (s *Server) routes() {
 	s.mux.Handle("GET /s/{source}", s.signedIn(s.sourceView))
 	s.mux.Handle("GET /s/{source}/c/{chat}", s.signedIn(s.conversationView))
 	s.mux.Handle("GET /sources", s.signedIn(s.sourcesList))
+	s.mux.Handle("GET /settings", s.signedIn(s.settings))
+	s.mux.Handle("POST /settings/appearance", s.signedIn(s.saveAppearance))
+	s.mux.Handle("POST /settings/languages", s.signedIn(s.saveLanguages))
+	s.mux.Handle("POST /settings/attachments", s.signedIn(s.saveAttachments))
+	s.mux.Handle("POST /settings/password", s.signedIn(s.savePassword))
 	s.mux.Handle("GET /media/{id}", s.signedIn(s.serveMedia))
 	s.mux.Handle("POST /media/{id}/fetch", s.signedIn(s.fetchMedia))
 }
@@ -232,5 +251,35 @@ func (s *Server) home(w http.ResponseWriter, r *http.Request, sess archive.Sessi
 		s.render(w, http.StatusOK, "empty", s.page(r, sess, "Welcome"))
 		return
 	}
+	all, err := s.sources()
+	if err != nil {
+		s.fail(w, err)
+		return
+	}
+	if key := s.remembered(r, all); key != "" {
+		redirect(w, r, "/s/"+key)
+		return
+	}
 	redirect(w, r, "/s/chats")
 }
+
+// sourceCookie remembers the last source the owner opened, so home, Sources
+// and Settings come back to it.
+const sourceCookie = "arc5_source"
+
+func remember(w http.ResponseWriter, r *http.Request, key string) {
+	http.SetCookie(w, &http.Cookie{Name: sourceCookie, Value: key, Path: "/", MaxAge: 365 * 24 * 3600,
+		HttpOnly: true, SameSite: http.SameSiteLaxMode, Secure: isSecure(r)})
+}
+
+// remembered is the last source opened, if it still exists.
+func (s *Server) remembered(r *http.Request, all []source) string {
+	c, err := r.Cookie(sourceCookie)
+	if err != nil {
+		return ""
+	}
+	if _, ok := s.selection(c.Value, all); !ok {
+		return ""
+	}
+	return c.Value
+}
````

`internal/web/settings.go`:

````go
package web

import (
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"

	"github.com/fenelon/arc5/internal/archive"
	"github.com/fenelon/arc5/internal/indexer"
	"github.com/fenelon/arc5/internal/search"
)

// maxLimitMB is the largest attachment limit the form accepts, about 100 GB.
const maxLimitMB = 100000

// LanguageBox is one language's checkbox.
type LanguageBox struct {
	Code, Name string
	Checked    bool
}

type settingsPage struct {
	Page
	Saved       string // which card to confirm, from the "saved" parameter
	Problem     string // why the last form was refused
	ProblemCard string // which card the problem belongs to
	Languages   []LanguageBox
	IndexNote   string
	LimitMB     string
	EnvPassword bool
}

// languageList joins language names for a sentence: "English, Russian and Spanish".
func languageList(codes []string) string {
	names := make([]string, 0, len(codes))
	for _, code := range codes {
		for _, l := range search.Languages() {
			if l.Code == code {
				names = append(names, l.Name)
			}
		}
	}
	switch len(names) {
	case 0:
		return ""
	case 1:
		return names[0]
	}
	return strings.Join(names[:len(names)-1], ", ") + " and " + names[len(names)-1]
}

// indexNote says what the search index is doing.
func (s *Server) indexNote() string {
	st := s.deps.Index.Status()
	langs := languageList(st.Languages)
	switch {
	case st.Rebuilding && st.Total > 0:
		return fmt.Sprintf("Rebuilding the index: %d of %d messages. Search keeps working and finds more as it goes.", st.Done, st.Total)
	case st.Rebuilding:
		return "Rebuilding the index. Search keeps working and finds more as it goes."
	case st.Err != "":
		return "The last rebuild failed: " + st.Err + ". Save again to retry."
	case langs == "":
		return "No languages are ticked, so search finds exact words only."
	}
	return "The index is up to date for " + langs + "."
}

func (s *Server) settingsData(r *http.Request, sess archive.Session) (settingsPage, error) {
	all, err := s.sources()
	if err != nil {
		return settingsPage{}, err
	}
	data := settingsPage{Page: s.chrome(r, sess, "Settings", all, ""), Saved: r.URL.Query().Get("saved"), EnvPassword: s.auth.usesEnv()}
	chosen := s.deps.Index.Status().Languages
	for _, l := range search.Languages() {
		data.Languages = append(data.Languages, LanguageBox{Code: l.Code, Name: l.Name, Checked: containsString(chosen, l.Code)})
	}
	sort.Slice(data.Languages, func(i, j int) bool { return data.Languages[i].Name < data.Languages[j].Name })
	data.IndexNote = s.indexNote()
	limit, err := s.deps.Store.MediaCap()
	if err != nil {
		return settingsPage{}, err
	}
	data.LimitMB = strconv.FormatInt((limit+(1<<20)-1)>>20, 10)
	return data, nil
}

func containsString(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

func (s *Server) settings(w http.ResponseWriter, r *http.Request, sess archive.Session) {
	data, err := s.settingsData(r, sess)
	if err != nil {
		s.fail(w, err)
		return
	}
	s.render(w, http.StatusOK, "settings", data)
}

// refuse shows the settings page again with a problem in one card.
func (s *Server) refuse(w http.ResponseWriter, r *http.Request, sess archive.Session, card, problem string) {
	data, err := s.settingsData(r, sess)
	if err != nil {
		s.fail(w, err)
		return
	}
	data.Saved, data.Problem, data.ProblemCard = "", problem, card
	s.render(w, http.StatusUnprocessableEntity, "settings", data)
}

func (s *Server) saveAppearance(w http.ResponseWriter, r *http.Request, _ archive.Session) {
	c := &http.Cookie{Name: "arc5_theme", Path: "/", MaxAge: 365 * 24 * 3600, SameSite: http.SameSiteLaxMode, Secure: isSecure(r)}
	switch choice := r.PostFormValue("theme"); choice {
	case "light", "dark":
		c.Value = choice
	default:
		c.MaxAge = -1 // follow the device
	}
	http.SetCookie(w, c)
	redirect(w, r, "/settings?saved=appearance")
}

func (s *Server) saveLanguages(w http.ResponseWriter, r *http.Request, sess archive.Session) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "The form could not be read.", http.StatusBadRequest)
		return
	}
	err := s.deps.Index.SetLanguages(r.PostForm["lang"])
	switch {
	case errors.Is(err, indexer.ErrUnknownLanguage):
		s.refuse(w, r, sess, "languages", "One of those languages is not available. Tick only languages from the list.")
	case err != nil:
		s.fail(w, err)
	default:
		redirect(w, r, "/settings?saved=languages#languages")
	}
}

func (s *Server) saveAttachments(w http.ResponseWriter, r *http.Request, sess archive.Session) {
	mb, err := strconv.ParseInt(strings.TrimSpace(r.PostFormValue("limit_mb")), 10, 64)
	if err != nil || mb < 0 || mb > maxLimitMB {
		s.refuse(w, r, sess, "attachments", fmt.Sprintf("Type a whole number of megabytes from 0 to %d.", maxLimitMB))
		return
	}
	if err := s.deps.Store.SetMediaCap(mb << 20); err != nil {
		s.fail(w, err)
		return
	}
	redirect(w, r, "/settings?saved=attachments#attachments")
}

func (s *Server) savePassword(w http.ResponseWriter, r *http.Request, sess archive.Session) {
	if s.auth.usesEnv() {
		s.refuse(w, r, sess, "password", "The password comes from ARC5_PASSWORD and can only be changed there.")
		return
	}
	ok, wait, err := s.auth.login(r.Context(), r.PostFormValue("current"))
	switch {
	case errors.Is(err, errLoginBusy) || wait > 0:
		s.refuse(w, r, sess, "password", "Too many wrong passwords. Wait a little and try again.")
		return
	case err != nil:
		s.fail(w, err)
		return
	case !ok:
		s.refuse(w, r, sess, "password", "The current password is not right.")
		return
	}
	switch err := s.auth.setPassword(r.PostFormValue("new")); {
	case errors.Is(err, errShortPassword):
		s.refuse(w, r, sess, "password", fmt.Sprintf("The new password is too short. Use at least %d characters.", minPasswordLen))
		return
	case err != nil:
		s.fail(w, err)
		return
	}
	// Changing the password signed every browser out; sign this one back in.
	if err := s.auth.startSession(w, r); err != nil {
		s.fail(w, err)
		return
	}
	redirect(w, r, "/settings?saved=password#password")
}
````

Change `internal/web/sources.go`:

````diff
--- a/internal/web/sources.go
+++ b/internal/web/sources.go
@@ -153,6 +153,9 @@ func (s *Server) chrome(r *http.Request, sess archive.Session, title string, all
 	if len(all) == 0 {
 		return p
 	}
+	if current == "" {
+		current = s.remembered(r, all)
+	}
 	picker := &Picker{}
 	for _, k := range kinds {
 		var tiles []Tile
````

Change `internal/web/static/app.css`:

````diff
--- a/internal/web/static/app.css
+++ b/internal/web/static/app.css
@@ -206,9 +206,37 @@ svg{display:block}
 .tile--warn{border-color:var(--warn-line)}
 .tile--warn .tile__sub{color:var(--warn-ink)}
 
+/* Settings. */
+.settings-link{display:inline-flex;align-items:center;gap:6px;min-height:44px;padding:0 4px;font-weight:450;color:var(--ink);text-decoration:none}
+.settings-link svg{width:20px;height:20px}
+.plain--settings{max-width:760px;padding-top:28px}
+.card__title{font-weight:650;font-size:17px}
+.card__help{max-width:620px;margin-top:6px;color:var(--muted)}
+.card form{margin-top:14px}
+.card fieldset{margin:0;padding:0;border:0}
+.choices{display:flex;flex-wrap:wrap;align-items:center;gap:10px 20px}
+.choices fieldset{display:flex;flex-wrap:wrap;gap:10px 20px}
+.choice{display:inline-flex;align-items:center;gap:8px;min-height:32px;cursor:pointer}
+.choice input{width:18px;height:18px;margin:0;accent-color:var(--accent)}
+.choice:has(input:checked){font-weight:600}
+.checks{display:grid;grid-template-columns:repeat(4,minmax(0,1fr));gap:6px 16px}
+.card__foot{display:flex;flex-wrap:wrap;align-items:center;gap:10px 16px;margin-top:16px;padding-top:16px;border-top:1px solid var(--line-soft)}
+.card__note{flex:1 1 260px;font-size:13.5px;color:var(--muted)}
+.inline-form{display:flex;flex-wrap:wrap;align-items:flex-end;gap:12px}
+.inline-form .field{flex:1 1 140px;min-width:0;margin-bottom:0}
+.inline-form .field--short{flex:0 1 160px}
+.saved{margin-top:10px;padding:8px 12px;color:var(--ink);background:var(--accent-soft);border-radius:8px}
+.alert{margin-top:10px}
+.page-links{display:flex;flex-wrap:wrap;align-items:center;gap:8px 22px;margin-top:22px}
+.page-links a,.link-button--accent{font-weight:600;color:var(--accent);text-decoration:underline}
+.page-links form{margin:0}
+
 /* Phones: one pane at a time, the rail gives way to a year picker. */
 @media (max-width:760px){
   .only-phone{display:flex}
+  .not-phone{display:none}
+  .settings-link__label{display:none}
+  .checks{grid-template-columns:repeat(2,minmax(0,1fr))}
   .topbar{flex-wrap:nowrap;padding:8px 14px;gap:8px 10px}
   .brand__name{display:none}
   .app--open .topbar{display:none}
````

Change `internal/web/static/js/jump_controller.js`:

````diff
--- a/internal/web/static/js/jump_controller.js
+++ b/internal/web/static/js/jump_controller.js
@@ -1,7 +1,7 @@
 import { Controller } from "./stimulus.js"
 
-// The phone's year picker jumps as soon as a year is chosen. Without this
-// controller the "Go" button does the same.
+// Submits its form as soon as a choice changes: the phone's year picker and
+// the theme in Settings. Without this controller their button does the same.
 export default class extends Controller {
   static targets = ["button"]
 
````

Change `internal/web/templates/partials.html`:

````diff
--- a/internal/web/templates/partials.html
+++ b/internal/web/templates/partials.html
@@ -4,7 +4,8 @@
 <meta charset="utf-8">
 <meta name="viewport" content="width=device-width, initial-scale=1">
 <title>{{.Title}} - arc5</title>
-<link rel="stylesheet" href="{{asset "app.css"}}">
+{{if .CSRF}}<meta name="csrf" content="{{.CSRF}}">
+{{end}}<link rel="stylesheet" href="{{asset "app.css"}}">
 <script type="module" src="{{asset "js/app.js"}}"></script>
 </head>
 {{end}}
@@ -30,8 +31,8 @@
 {{template "brand"}}
 {{if .Picker}}{{template "picker" .Picker}}{{end}}
 <span class="topbar__space"></span>
-<button class="icon-button" type="button" data-controller="theme" data-action="theme#toggle" aria-label="Switch between the light and dark theme"><svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M20 14.5A8 8 0 019.5 4a8 8 0 1010.5 10.5z"/></svg></button>
-<form method="post" action="/logout"><input type="hidden" name="csrf" value="{{.CSRF}}"><button class="link-button" type="submit">Sign out</button></form>
+<button class="icon-button not-phone" type="button" data-controller="theme" data-action="theme#toggle" aria-label="Switch between the light and dark theme"><svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M20 14.5A8 8 0 019.5 4a8 8 0 1010.5 10.5z"/></svg></button>
+<a class="settings-link" href="/settings"><svg class="only-phone" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M4 7h10M18 7h2M4 17h4M12 17h8"/><circle cx="16" cy="7" r="2"/><circle cx="10" cy="17" r="2"/></svg><span class="settings-link__label">Settings</span></a>
 </header>{{end}}
 
 {{define "banner"}}{{if .Alert}}<div class="banner" role="alert"><span><strong>{{.Alert.Name}} is not archiving.</strong> {{.Alert.Detail}}</span><a class="button button--warn" href="/sources">Open sources</a></div>{{end}}{{end}}
````

`internal/web/templates/settings.html`:

````html
{{define "settings"}}{{template "head" .}}
<body class="app">
{{template "topbar" .}}
{{template "banner" .}}
<main class="plain plain--settings">
<h1>Settings</h1>

<section class="card" id="appearance" aria-labelledby="appearance-title">
<h2 class="card__title" id="appearance-title">Appearance</h2>
{{template "settings-note" (dict "Card" "appearance" "Page" .)}}
<form method="post" action="/settings/appearance" class="choices" data-controller="jump">
<input type="hidden" name="csrf" value="{{.CSRF}}">
<fieldset><legend class="visually-hidden">Theme</legend>
<label class="choice"><input type="radio" name="theme" value=""{{if not .Theme}} checked{{end}} data-action="jump#go"> Follow this device</label>
<label class="choice"><input type="radio" name="theme" value="light"{{if eq .Theme "light"}} checked{{end}} data-action="jump#go"> Light</label>
<label class="choice"><input type="radio" name="theme" value="dark"{{if eq .Theme "dark"}} checked{{end}} data-action="jump#go"> Dark</label>
</fieldset>
<button class="button button--quiet" type="submit" data-jump-target="button">Save</button>
</form>
</section>

<section class="card" id="languages" aria-labelledby="languages-title">
<h2 class="card__title" id="languages-title">Search languages</h2>
<p class="card__help">Search finds other forms of a word in the languages ticked here. Tick only the ones you write in: each extra one makes matches looser. Every other language is still found by exact word.</p>
{{template "settings-note" (dict "Card" "languages" "Page" .)}}
<form method="post" action="/settings/languages">
<input type="hidden" name="csrf" value="{{.CSRF}}">
<fieldset class="checks"><legend class="visually-hidden">Languages</legend>
{{range .Languages}}<label class="choice"><input type="checkbox" name="lang" value="{{.Code}}"{{if .Checked}} checked{{end}}> {{.Name}}</label>
{{end}}</fieldset>
<div class="card__foot"><button class="button" type="submit">Save and rebuild the index</button><p class="card__note">{{.IndexNote}}</p></div>
</form>
</section>

<section class="card" id="attachments" aria-labelledby="attachments-title">
<h2 class="card__title" id="attachments-title">Attachments</h2>
<p class="card__help">Files up to this size are downloaded as messages arrive. Larger ones are listed with a Fetch now button.</p>
{{template "settings-note" (dict "Card" "attachments" "Page" .)}}
<form method="post" action="/settings/attachments" class="inline-form">
<input type="hidden" name="csrf" value="{{.CSRF}}">
<div class="field field--short"><label for="limit_mb">Largest automatic download, in MB</label><input id="limit_mb" name="limit_mb" type="number" min="0" max="100000" value="{{.LimitMB}}" required></div>
<button class="button" type="submit">Save</button>
</form>
</section>

<section class="card" id="password" aria-labelledby="password-title">
<h2 class="card__title" id="password-title">Password</h2>
{{if .EnvPassword}}<p class="card__help">The password comes from ARC5_PASSWORD. To change it, change that setting and restart arc5; every browser is then signed out.</p>
{{template "settings-note" (dict "Card" "password" "Page" .)}}
{{else}}<p class="card__help">Changing it signs out every other browser.</p>
{{template "settings-note" (dict "Card" "password" "Page" .)}}
<form method="post" action="/settings/password" class="inline-form">
<input type="hidden" name="csrf" value="{{.CSRF}}">
<div class="field"><label for="current">Current password</label><input id="current" name="current" type="password" autocomplete="current-password" required></div>
<div class="field"><label for="new">New password</label><input id="new" name="new" type="password" autocomplete="new-password" minlength="8" required></div>
<button class="button" type="submit">Change password</button>
</form>{{end}}
</section>

<div class="page-links"><a href="/sources">Manage sources</a><form method="post" action="/logout"><input type="hidden" name="csrf" value="{{.CSRF}}"><button class="link-button link-button--accent" type="submit">Sign out</button></form></div>
</main>
</body>
</html>{{end}}

{{define "settings-note"}}{{if eq .Page.ProblemCard .Card}}<p class="alert" role="alert">{{.Page.Problem}}</p>{{else if eq .Page.Saved .Card}}<p class="saved" role="status">Saved.{{if eq .Card "password"}} Every other browser is signed out.{{end}}{{if eq .Card "languages"}} The index is being rebuilt in the background.{{end}}</p>{{end}}{{end}}
````

- [ ] **Step 4: Run the tests to verify they pass**

Run: `gofmt -l internal cmd; go vet ./... && go test -race -count=1 -timeout 180s ./...`
Expected: `gofmt -l` prints nothing; every package PASSES.

- [ ] **Step 5: Commit**

```bash
git add cmd/arc5 internal/web internal/web/static internal/web/static/js internal/web/templates
git commit -m "Add the Settings page and remember the last source opened"
```

---

### Task 6: Search in the interface

The search box in the top bar replaces the chat list with results, as in the dark search mockup: matching chats and people, then messages with the matched words marked. Opening a result shows the conversation at that message with its words marked and the results kept on the left. Choosing a person lists everything they wrote. Without scripts the box submits as a form; with them, the search controller fetches the column as a fragment while the owner types.

**Files:**
- Create: `internal/web/search.go`, `internal/web/static/js/search_controller.js`
- Modify: `internal/web/chats.go`, `internal/web/conversation.go`, `internal/web/page.go`, `internal/web/server.go`, `internal/web/static/app.css`, `internal/web/static/js/app.js`, `internal/web/templates/conversation.html`, `internal/web/templates/partials.html`, `internal/web/templates/source.html`
- Test: `internal/web/search_test.go` (new), `internal/web/server_test.go`

**Interfaces:**
- Consumes: `Searcher.Names`, `Searcher.Search`, `Searcher.Highlight`, `Hit.ChatKind`, `Hit.Outgoing` (Task 1); `Store.Sender` (Task 3); `Deps.Index` (Task 5).
- Produces:
  - `web.Page.Search *SearchBox`, `type SearchBox struct { Action, Results, Query string }`
  - `type Piece`, `NameRow`, `PersonRow`, `HitRow`, `Results` (fields in the code); `sourcePage.Results *Results`
  - `(*Server).results(ctx, r, sel) (*Results, error)`; `errNoSuchPerson`; helpers `searchQuery`, `keepQuery`, `pageNumber`
  - route `GET /s/{source}/results` (the left column as a fragment); parameters `q`, `person`, `page`, `at`
  - templates `list`, `results`, `pieces`, `search`; `Conversation.Query`, `Message.Marked`; `rail(path, keep, quarters, now)`
  - `static/js/search_controller.js` (registered as `search`)

- [ ] **Step 1: Write the failing tests**

`internal/web/search_test.go`:

````go
package web

import (
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/fenelon/arc5/internal/model"
)

// marinaSite has one source with the conversations the approved mockup
// searches for "marina".
type marinaSite struct {
	*site
	acc                     int64
	flight, deposit, keys   int64 // message IDs
	dan, family, marinaChat int64 // chat IDs
	marina                  int64 // sender ID
}

func newMarinaSite(t *testing.T) *marinaSite {
	t.Helper()
	s := newSite(t, "")
	s.signIn()
	s.srv.deps.Now = func() time.Time { return noon.Add(7 * 24 * time.Hour) }
	m := &marinaSite{site: s, acc: s.account("Personal", "telegram")}
	for _, sn := range []model.Sender{{SourceID: "marina", DisplayName: "Marina Sokolova"}, {SourceID: "pablo", DisplayName: "Pablo Ruiz"}, {SourceID: "dan", DisplayName: "Dan Whitfield"}} {
		if err := s.store.UpsertSender(m.acc, sn); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.store.UpsertChat(m.acc, model.Chat{SourceID: "family", Kind: model.ChatGroup, Title: "Familia: Marina y Pablo"}); err != nil {
		t.Fatal(err)
	}
	from := func(sender string) func(*model.Message) {
		return func(msg *model.Message) { msg.SenderSourceID = sender }
	}
	m.flight = s.say(m.acc, "Dan Whitfield", "1", "Marina's flight lands at 18:40. Can you pick her up?", noon.Add(-48*time.Hour), from("dan"))
	if err := s.store.MarkDeleted(m.acc, "Dan Whitfield", []string{"1"}); err != nil {
		t.Fatal(err)
	}
	m.deposit = s.say(m.acc, "Dan Whitfield", "2", "Sent the deposit to Marina yesterday, receipt attached.", noon.Add(-24*time.Hour), func(msg *model.Message) {
		msg.Outgoing = true
		msg.Attachments = []model.Attachment{{Kind: model.AttachDocument, FileName: "deposit-marina.pdf", MimeType: "application/pdf", Size: 10}}
	})
	m.keys = s.say(m.acc, "family", "1", "Dile a Marina que las llaves están en el buzón.", noon.Add(-time.Hour), from("pablo"))
	s.say(m.acc, "Marina Sokolova", "1", "Спасибо, что встретил!", noon, from("marina"))
	s.say(m.acc, "Saved Messages", "1", "Passport renewal: form 11", noon.Add(-72*time.Hour))
	m.dan, m.family, m.marinaChat = s.chatID(m.acc, "Dan Whitfield"), s.chatID(m.acc, "Familia: Marina y Pablo"), s.chatID(m.acc, "Marina Sokolova")
	if err := s.store.Reader().QueryRow(`SELECT id FROM senders WHERE source_id = 'marina'`).Scan(&m.marina); err != nil {
		t.Fatal(err)
	}
	return m
}

func TestSearchShowsChatsPeopleAndMessages(t *testing.T) {
	m := newMarinaSite(t)
	body := m.page(fmt.Sprintf("/s/%d?q=marina", m.acc))
	has(t, body,
		`<input class="search__input" type="search" name="q" value="marina"`,
		"Chats and people",
		`<mark class="hit">Marina</mark> Sokolova`, "Private chat, last message 28 Sep",
		`Familia: <mark class="hit">Marina</mark> y Pablo`, "Group, last message 28 Sep",
		fmt.Sprintf(`href="/s/%d/c/%d?q=marina"`, m.acc, m.marinaChat),
		"Everything from this person, in any chat", fmt.Sprintf(`href="/s/%d?q=marina&amp;person=%d"`, m.acc, m.marina),
		"<h2 class=\"results__heading\">Messages</h2>",
		`<mark class="hit">Marina</mark>&#39;s flight lands`, "Deleted at source",
		`You: Sent the deposit to <mark class="hit">Marina</mark>`,
		`File: deposit-<mark class="hit">marina</mark>.pdf`,
		`Pablo Ruiz: Dile a <mark class="hit">Marina</mark>`,
		fmt.Sprintf(`href="/s/%d/c/%d?at=%d&amp;q=marina"`, m.acc, m.dan, m.flight))
	lacks(t, body, "Passport renewal", "Show more messages")
}

func TestSearchFragment(t *testing.T) {
	m := newMarinaSite(t)
	rec := m.get(fmt.Sprintf("/s/%d/results?q=marina", m.acc))
	if rec.Code != http.StatusOK || rec.Header().Get("Content-Security-Policy") != csp {
		t.Fatalf("fragment: %d %v", rec.Code, rec.Header())
	}
	body := rec.Body.String()
	has(t, body, "Chats and people", `<mark class="hit">Marina</mark> Sokolova`)
	lacks(t, body, "<html", "<header")
	has(t, m.get(fmt.Sprintf("/s/%d/results?q=", m.acc)).Body.String(), "Passport renewal", `class="row`)
	if rec := m.get("/s/9999/results?q=marina"); rec.Code != http.StatusNotFound {
		t.Errorf("an unknown source: %d", rec.Code)
	}
}

func TestSearchNothingFound(t *testing.T) {
	m := newMarinaSite(t)
	has(t, m.page(fmt.Sprintf("/s/%d?q=zzzz", m.acc)), "Nothing matches “zzzz”.")
}

func TestSearchKeepsToTheChosenSource(t *testing.T) {
	m := newMarinaSite(t)
	work := m.account("Work", "telegram")
	m.say(work, "Ana", "1", "Marina from work", noon)
	has(t, m.page(fmt.Sprintf("/s/%d?q=marina", work)), "</mark> from work")
	lacks(t, m.page(fmt.Sprintf("/s/%d?q=marina", work)), "flight lands")
	lacks(t, m.page(fmt.Sprintf("/s/%d?q=marina", m.acc)), "from work")
	has(t, m.page("/s/chats?q=marina"), "</mark> from work", "flight lands")
}

func TestOpeningAHitKeepsTheResultsAndMarksTheMessage(t *testing.T) {
	m := newMarinaSite(t)
	body := m.page(fmt.Sprintf("/s/%d/c/%d?at=%d&q=marina", m.acc, m.dan, m.flight))
	has(t, body, "Chats and people", "result result--current",
		`<p class="msg__text"><mark class="hit">Marina</mark>&#39;s flight lands`,
		fmt.Sprintf(`class="back only-phone" href="/s/%d?q=marina"`, m.acc),
		`<input class="search__input" type="search" name="q" value="marina"`)
	if strings.Count(body, "result--current") != 1 {
		t.Error("more than one result is marked as open")
	}
	// Other messages on the page are not marked, nor is the target without a search.
	has(t, body, `<p class="msg__text">Sent the deposit to Marina yesterday`)
	lacks(t, m.page(fmt.Sprintf("/s/%d/c/%d?at=%d", m.acc, m.dan, m.flight)), `<mark class="hit">`)
}

func TestPersonShowsTheirMessages(t *testing.T) {
	m := newMarinaSite(t)
	body := m.page(fmt.Sprintf("/s/%d?q=marina&person=%d", m.acc, m.marina))
	has(t, body, "Everything from Marina Sokolova", "Спасибо, что встретил!",
		fmt.Sprintf(`href="/s/%d?q=marina">All results`, m.acc))
	lacks(t, body, "Chats and people", "flight lands", "Dile a")

	other := m.account("Work", "telegram")
	if rec := m.get(fmt.Sprintf("/s/%d?q=marina&person=%d", other, m.marina)); rec.Code != http.StatusNotFound {
		t.Errorf("a person from another source: %d, want 404", rec.Code)
	}
	if rec := m.get(fmt.Sprintf("/s/%d?q=marina&person=banana", m.acc)); rec.Code != http.StatusNotFound {
		t.Errorf("a made-up person: %d, want 404", rec.Code)
	}
}

func TestSearchPaging(t *testing.T) {
	s := newSite(t, "")
	s.signIn()
	acc := s.account("Personal", "telegram")
	for i := range 120 {
		s.say(acc, "Cats", fmt.Sprint(i), fmt.Sprintf("cats number %d", i), noon.Add(time.Duration(i)*time.Minute))
	}
	first := s.page(fmt.Sprintf("/s/%d?q=cats", acc))
	if n := strings.Count(first, `<a class="result`); n != 50 {
		t.Errorf("first page has %d results, want 50", n)
	}
	has(t, first, fmt.Sprintf(`href="/s/%d?q=cats&amp;page=2"`, acc), "Show more messages", "Chats and people")
	third := s.page(fmt.Sprintf("/s/%d?q=cats&page=3", acc))
	if n := strings.Count(third, `<a class="result`); n != 20 {
		t.Errorf("third page has %d results, want 20", n)
	}
	has(t, third, fmt.Sprintf(`href="/s/%d?q=cats">First results`, acc))
	lacks(t, third, "Show more messages", "Chats and people")
	if body := s.page(fmt.Sprintf("/s/%d?q=cats&page=-4", acc)); strings.Count(body, `<a class="result`) != 50 {
		t.Error("a nonsense page number does not give the first page")
	}
}

// Review Focus: hostile text in a query or a result is shown as text.
func TestSearchShowsHostileTextAsText(t *testing.T) {
	s := newSite(t, "")
	s.signIn()
	acc := s.account("Personal", "telegram")
	s.say(acc, `<img src=x onerror=alert(1)>`, "1", `<script>alert("x")</script> script here`, noon)
	q := url.QueryEscape(`"><script>alert(2)</script>`)
	for _, path := range []string{fmt.Sprintf("/s/%d?q=%s", acc, q), fmt.Sprintf("/s/%d?q=script", acc), fmt.Sprintf("/s/%d/results?q=script", acc)} {
		body := s.get(path).Body.String()
		if strings.Contains(body, "<script>alert") || strings.Contains(body, "<img src=x") {
			t.Errorf("%s: hostile text reached the page as markup", path)
		}
	}
}
````

Change `internal/web/server_test.go`:

````diff
--- a/internal/web/server_test.go
+++ b/internal/web/server_test.go
@@ -135,7 +135,7 @@ func TestStaticFiles(t *testing.T) {
 		!strings.Contains(css.Header().Get("Cache-Control"), "immutable") || !strings.Contains(css.Body.String(), "--accent") {
 		t.Errorf("app.css: %d %v", css.Code, css.Header())
 	}
-	for _, name := range []string{"js/app.js", "js/stimulus.js", "js/theme_controller.js", "js/picker_controller.js",
+	for _, name := range []string{"js/app.js", "js/stimulus.js", "js/theme_controller.js", "js/picker_controller.js", "js/search_controller.js",
 		"fonts/geologica-latin-wght-normal.woff2", "fonts/geologica-cyrillic-wght-normal.woff2"} {
 		if rec := s.get(s.srv.asset(name)); rec.Code != http.StatusOK || rec.Body.Len() == 0 {
 			t.Errorf("%s: status %d, %d bytes", name, rec.Code, rec.Body.Len())
````

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go vet ./internal/web/; go test -count=1 ./internal/web/`
Expected: FAIL: `TestSearchShowsChatsPeopleAndMessages` and the other new tests report the page lacks the search box and results.

- [ ] **Step 3: Write the implementation**

Change `internal/web/chats.go`:

````diff
--- a/internal/web/chats.go
+++ b/internal/web/chats.go
@@ -1,6 +1,7 @@
 package web
 
 import (
+	"errors"
 	"fmt"
 	"net/http"
 	"strconv"
@@ -34,6 +35,7 @@ type sourcePage struct {
 	Page
 	Chats         []ChatRow
 	MoreChatsHref string
+	Results       *Results // replaces the chat list while there is a search
 	Conversation  *Conversation
 }
 
@@ -139,17 +141,35 @@ func (s *Server) sourceShell(w http.ResponseWriter, r *http.Request, sess archiv
 		http.Error(w, "This source is shown as mail, which this version of arc5 cannot display yet.", http.StatusNotImplemented)
 		return sourcePage{}, selection{}, false
 	}
-	count := chatCount(r)
-	rows, more, err := s.chatRows(sel, currentChat, count)
-	if err != nil {
+	data := sourcePage{Page: s.chrome(r, sess, sel.Name, all, sel.Key)}
+	data.Search = &SearchBox{Action: "/s/" + sel.Key, Results: "/s/" + sel.Key + "/results", Query: searchQuery(r)}
+	results, err := s.results(r.Context(), r, sel)
+	switch {
+	case errors.Is(err, errNoSuchPerson):
+		http.NotFound(w, r)
+		return sourcePage{}, selection{}, false
+	case err != nil:
 		s.fail(w, err)
 		return sourcePage{}, selection{}, false
+	case results != nil:
+		data.Results = results
+	default:
+		count := chatCount(r)
+		rows, more, err := s.chatRows(sel, currentChat, count)
+		if err != nil {
+			s.fail(w, err)
+			return sourcePage{}, selection{}, false
+		}
+		data.Chats = rows
+		if more {
+			path := r.URL.Path // the chat list grows in place, on a conversation's page too
+			if r.PathValue("chat") == "" {
+				path = "/s/" + sel.Key
+			}
+			data.MoreChatsHref = fmt.Sprintf("%s?chats=%d", path, count+chatsPerPage)
+		}
 	}
 	remember(w, r, sel.Key)
-	data := sourcePage{Page: s.chrome(r, sess, sel.Name, all, sel.Key), Chats: rows}
-	if more {
-		data.MoreChatsHref = fmt.Sprintf("%s?chats=%d", r.URL.Path, count+chatsPerPage)
-	}
 	return data, sel, true
 }
 
````

Change `internal/web/conversation.go`:

````diff
--- a/internal/web/conversation.go
+++ b/internal/web/conversation.go
@@ -22,6 +22,7 @@ type Conversation struct {
 	OlderHref   string
 	NewerHref   string
 	ScrollTo    string // where the page opens: "end", "start" or a message's anchor
+	Query       string // the search the page was opened from, kept in its links
 	Days        []Day
 	Years       []RailYear
 	YearOptions []YearOption
@@ -40,6 +41,7 @@ type Message struct {
 	Sender        string
 	ForwardedFrom string
 	Text          string
+	Marked        []Piece // Text with the search's words marked, for the message opened from a search
 	Time          string
 	Edited        string // when it was last edited, or ""
 	Deleted       string // when the source deleted it, or ""
@@ -144,7 +146,7 @@ func plural(n int, one, many string) string {
 
 // rail builds the year rail from a chat's quarters, counted in the zone of
 // now. now is the moment the page is reading, which marks one band and one year.
-func rail(path string, quarters []archive.Quarter, now time.Time) ([]RailYear, []YearOption) {
+func rail(path, keep string, quarters []archive.Quarter, now time.Time) ([]RailYear, []YearOption) {
 	if len(quarters) == 0 {
 		return nil, nil
 	}
@@ -167,7 +169,7 @@ func rail(path string, quarters []archive.Quarter, now time.Time) ([]RailYear, [
 				level = max(1, (n*4+busiest-1)/busiest)
 			}
 			y.Bands = append(y.Bands, RailBand{
-				Href:  fmt.Sprintf("%s?date=%d-%02d-01", path, year, quarter*3-2),
+				Href:  fmt.Sprintf("%s?date=%d-%02d-01%s", path, year, quarter*3-2, keep),
 				Label: fmt.Sprintf("%s %d, %s", quarterNames[quarter-1], year, plural(n, "message", "messages")),
 				Level: level, Now: year == nowYear && quarter == nowQuarter,
 			})
@@ -248,7 +250,12 @@ func (s *Server) conversationView(w http.ResponseWriter, r *http.Request, sess a
 	}
 
 	path := fmt.Sprintf("/s/%s/c/%d", sel.Key, chatID)
+	q := searchQuery(r)
+	keep := keepQuery(q)
 	conv := &Conversation{Title: chat.Title, BackHref: "/s/" + sel.Key, SelfHref: r.URL.RequestURI()}
+	if keep != "" {
+		conv.Query, conv.BackHref = q, "/s/"+sel.Key+"?"+keep[1:]
+	}
 	conv.Subtitle = kindLabel(chat.Kind)
 	if service, known := s.serviceOf(chat.AccountID); known {
 		conv.Subtitle += " on " + service
@@ -272,13 +279,13 @@ func (s *Server) conversationView(w http.ResponseWriter, r *http.Request, sess a
 			}
 		}
 		if window.HasOlder {
-			conv.OlderHref = fmt.Sprintf("%s?before=%d", path, window.Messages[0].ID)
+			conv.OlderHref = fmt.Sprintf("%s?before=%d%s", path, window.Messages[0].ID, keep)
 		}
 		if window.HasNewer {
-			conv.NewerHref = fmt.Sprintf("%s?after=%d", path, window.Messages[len(window.Messages)-1].ID)
+			conv.NewerHref = fmt.Sprintf("%s?after=%d%s", path, window.Messages[len(window.Messages)-1].ID, keep)
 		}
 	}
-	conv.Years, conv.YearOptions = rail(path, quarters, reading.In(s.deps.Location))
+	conv.Years, conv.YearOptions = rail(path, keep, quarters, reading.In(s.deps.Location))
 
 	for _, m := range window.Messages {
 		sent := m.SentAt.In(s.deps.Location)
@@ -290,6 +297,9 @@ func (s *Server) conversationView(w http.ResponseWriter, r *http.Request, sess a
 			Anchor: fmt.Sprintf("m%d", m.ID), Service: m.Kind == model.KindService, Outgoing: m.Outgoing,
 			Target: m.ID == target, ForwardedFrom: m.ForwardedFrom, Text: m.Text, Time: sent.Format("15:04"),
 		}
+		if v.Target && keep != "" {
+			v.Marked = pieces(s.deps.Index.Searcher().Highlight(q, m.Text))
+		}
 		if !m.Outgoing && chat.Kind != model.ChatPrivate {
 			v.Sender = m.SenderName
 		}
````

Change `internal/web/page.go`:

````diff
--- a/internal/web/page.go
+++ b/internal/web/page.go
@@ -3,10 +3,11 @@ package web
 // Page is what every signed-in page shows around its own content.
 type Page struct {
 	Title  string
-	Theme  string  // "", "light" or "dark"; empty follows the device
-	CSRF   string  // the session's token, for forms
-	Picker *Picker // nil when there are no sources
-	Alert  *Alert  // nil when every source is archiving
+	Theme  string     // "", "light" or "dark"; empty follows the device
+	CSRF   string     // the session's token, for forms
+	Picker *Picker    // nil when there are no sources
+	Search *SearchBox // nil on pages without a search box
+	Alert  *Alert     // nil when every source is archiving
 }
 
 // Tile is one source, or "Everything at once", as the picker and the Sources
````

`internal/web/search.go`:

````go
package web

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/fenelon/arc5/internal/archive"
	"github.com/fenelon/arc5/internal/model"
	"github.com/fenelon/arc5/internal/search"
)

const (
	resultsPerPage = search.DefaultLimit
	maxResultPages = 40  // 2,000 results; narrow the search to see more
	maxQueryRunes  = 500 // longer text is cut before it is searched
)

// errNoSuchPerson is a person parameter that names nobody in the selection.
var errNoSuchPerson = errors.New("no such person here")

// SearchBox is the search field in the top bar.
type SearchBox struct {
	Action  string // where the form goes: the source's page
	Results string // where the search controller fetches results from
	Query   string
}

// Piece is a run of text, marked when the search matched it.
type Piece struct {
	Text string
	Mark bool
}

func pieces(fragments []search.Fragment) []Piece {
	out := make([]Piece, len(fragments))
	for i, f := range fragments {
		out[i] = Piece{Text: f.Text, Mark: f.Match}
	}
	return out
}

// NameRow is a chat whose title matched.
type NameRow struct {
	Href, Initials, Detail string
	Title                  []Piece
}

// PersonRow is a person whose name matched.
type PersonRow struct {
	Href string
	Name []Piece
}

// HitRow is a message that matched.
type HitRow struct {
	Href, Chat, When string
	Prefix           string // "You: " or the sender's name, as in the chat list
	Snippet          []Piece
	Files            [][]Piece
	Deleted, Current bool
}

// Results replace the chat list while there is a search.
type Results struct {
	Query     string
	Chats     []NameRow
	People    []PersonRow
	Person    string // whose messages these are, when one person was chosen
	AllHref   string // back to every result, from one person's messages
	Hits      []HitRow
	MoreHref  string
	FirstHref string
	Empty     bool
	Note      string
}

// searchQuery is the search text of a request, cut to a sane length.
func searchQuery(r *http.Request) string {
	q := r.URL.Query().Get("q")
	if utf8.RuneCountInString(q) > maxQueryRunes {
		q = string([]rune(q)[:maxQueryRunes])
	}
	return q
}

// keepQuery is what links within a conversation add so the results stay.
func keepQuery(q string) string {
	if strings.TrimSpace(q) == "" {
		return ""
	}
	return "&q=" + url.QueryEscape(q)
}

func pageNumber(r *http.Request) int {
	n, err := strconv.Atoi(r.URL.Query().Get("page"))
	if err != nil || n < 1 {
		return 1
	}
	return min(n, maxResultPages)
}

// results runs the request's search within a selection. It returns nil when
// there is no search.
func (s *Server) results(ctx context.Context, r *http.Request, sel selection) (*Results, error) {
	q := searchQuery(r)
	if strings.TrimSpace(q) == "" {
		return nil, nil
	}
	base := "/s/" + sel.Key + "?q=" + url.QueryEscape(q)
	res := &Results{Query: q}
	searcher := s.deps.Index.Searcher()
	page := pageNumber(r)
	query := search.Query{Text: q, AccountIDs: sel.AccountIDs, Limit: resultsPerPage, Offset: (page - 1) * resultsPerPage}
	pageBase := base

	if p := r.URL.Query().Get("person"); p != "" {
		id, err := strconv.ParseInt(p, 10, 64)
		if err != nil {
			return nil, errNoSuchPerson
		}
		person, err := s.deps.Store.Sender(id)
		if errors.Is(err, archive.ErrNotFound) || (err == nil && !containsID(sel.AccountIDs, person.AccountID)) {
			return nil, errNoSuchPerson
		}
		if err != nil {
			return nil, err
		}
		res.Person = person.DisplayName
		if res.Person == "" {
			res.Person = person.Username
		}
		res.AllHref = base
		query.Text, query.SenderID, query.Sort = "", id, search.ByNewest
		pageBase = fmt.Sprintf("%s&person=%d", base, id)
	} else if page == 1 {
		names, err := searcher.Names(ctx, q, sel.AccountIDs, 0)
		if err != nil {
			return nil, err
		}
		for _, c := range names.Chats {
			res.Chats = append(res.Chats, NameRow{
				Href:     fmt.Sprintf("/s/%s/c/%d?q=%s", sel.Key, c.ChatID, url.QueryEscape(q)),
				Initials: initials(fragmentText(c.Title)), Title: pieces(c.Title),
				Detail: kindLabel(c.Kind) + ", last message " + s.when(c.LastAt),
			})
		}
		for _, p := range names.People {
			res.People = append(res.People, PersonRow{Href: fmt.Sprintf("%s&person=%d", base, p.SenderID), Name: pieces(p.Name)})
		}
	}

	found, err := searcher.Search(ctx, query)
	if err != nil {
		return nil, err
	}
	at, _ := strconv.ParseInt(r.URL.Query().Get("at"), 10, 64)
	for _, h := range found.Hits {
		row := HitRow{
			Href: fmt.Sprintf("/s/%s/c/%d?at=%d&q=%s", sel.Key, h.ChatID, h.MessageID, url.QueryEscape(q)),
			Chat: h.ChatTitle, When: s.when(h.SentAt), Snippet: pieces(h.Snippet),
			Deleted: h.Deleted, Current: h.MessageID == at,
		}
		switch {
		case h.Outgoing:
			row.Prefix = "You: "
		case h.ChatKind != model.ChatPrivate && h.SenderName != "":
			row.Prefix = h.SenderName + ": "
		}
		for _, f := range h.Files {
			row.Files = append(row.Files, pieces(f))
		}
		res.Hits = append(res.Hits, row)
	}
	if found.More && page < maxResultPages {
		res.MoreHref = fmt.Sprintf("%s&page=%d", pageBase, page+1)
	}
	if page > 1 {
		res.FirstHref = pageBase
	}
	res.Empty = len(res.Chats) == 0 && len(res.People) == 0 && len(res.Hits) == 0
	if s.deps.Index.Status().Rebuilding {
		res.Note = "The search index is being rebuilt, so some messages are not found yet."
	}
	return res, nil
}

func fragmentText(fragments []search.Fragment) string {
	var b strings.Builder
	for _, f := range fragments {
		b.WriteString(f.Text)
	}
	return b.String()
}

// resultsFragment answers the search controller: the left column's content
// for the typed text, or the chat list when the text is empty.
func (s *Server) resultsFragment(w http.ResponseWriter, r *http.Request, sess archive.Session) {
	data, _, ok := s.sourceShell(w, r, sess, 0)
	if !ok {
		return
	}
	s.render(w, http.StatusOK, "list", data)
}
````

Change `internal/web/server.go`:

````diff
--- a/internal/web/server.go
+++ b/internal/web/server.go
@@ -104,6 +104,7 @@ func (s *Server) routes() {
 	s.mux.Handle("GET /{$}", s.signedIn(s.home))
 	s.mux.Handle("GET /s/{source}", s.signedIn(s.sourceView))
 	s.mux.Handle("GET /s/{source}/c/{chat}", s.signedIn(s.conversationView))
+	s.mux.Handle("GET /s/{source}/results", s.signedIn(s.resultsFragment))
 	s.mux.Handle("GET /sources", s.signedIn(s.sourcesList))
 	s.mux.Handle("GET /settings", s.signedIn(s.settings))
 	s.mux.Handle("POST /settings/appearance", s.signedIn(s.saveAppearance))
````

Change `internal/web/static/app.css`:

````diff
--- a/internal/web/static/app.css
+++ b/internal/web/static/app.css
@@ -206,6 +206,29 @@ svg{display:block}
 .tile--warn{border-color:var(--warn-line)}
 .tile--warn .tile__sub{color:var(--warn-ink)}
 
+/* Search box and results. */
+.search{position:relative;display:flex;flex:1 1 auto;align-items:center;min-width:0;margin:0}
+.search__icon{position:absolute;left:13px;width:17px;height:17px;color:var(--muted);pointer-events:none}
+.search__input{width:100%;height:44px;padding:0 14px 0 40px;font:inherit;color:var(--ink);background:var(--bg);border:1px solid var(--line);border-radius:8px}
+.search__input:focus{outline:2px solid var(--accent);outline-offset:-1px;background:var(--pane)}
+.results__heading{margin:16px 16px 6px;font-size:13.5px;font-weight:600;color:var(--muted)}
+.results__person{display:flex;align-items:baseline;justify-content:space-between;gap:10px;padding-right:16px}
+.results__back{flex-shrink:0;font-size:13.5px;font-weight:600;color:var(--accent)}
+.results__note{padding:10px 16px;font-size:13.5px;color:var(--muted)}
+.name-row{display:flex;gap:12px;align-items:center;padding:9px 16px;color:var(--ink);text-decoration:none}
+.name-row:hover,.result:hover{background:var(--accent-soft)}
+.avatar--person{color:var(--muted);background:transparent;border:1.5px dashed var(--line)}
+.avatar--person svg{width:18px;height:18px}
+.row__preview--accent{color:var(--accent)}
+.result{display:flex;flex-direction:column;gap:3px;padding:11px 16px;color:var(--ink);text-decoration:none;border-bottom:1px solid var(--line-soft)}
+.result--current{background:var(--accent-soft);box-shadow:inset 3px 0 0 var(--accent)}
+.result__snippet{font-size:14px;line-height:1.45;overflow-wrap:anywhere}
+.result__file{font-size:13px;color:var(--muted);overflow-wrap:anywhere}
+.result .tag{align-self:flex-start}
+mark.hit{padding:0 2px;color:var(--hit-ink);background:var(--hit);border-radius:3px}
+.msg--target:has(mark.hit){border-color:var(--hit)}
+.more-link{display:block;padding:14px 16px;font-weight:600;color:var(--accent)}
+
 /* Settings. */
 .settings-link{display:inline-flex;align-items:center;gap:6px;min-height:44px;padding:0 4px;font-weight:450;color:var(--ink);text-decoration:none}
 .settings-link svg{width:20px;height:20px}
@@ -235,9 +258,11 @@ svg{display:block}
 @media (max-width:760px){
   .only-phone{display:flex}
   .not-phone{display:none}
+  .topbar{flex-wrap:wrap}
+  .search{order:10;flex:1 1 100%}
   .settings-link__label{display:none}
   .checks{grid-template-columns:repeat(2,minmax(0,1fr))}
-  .topbar{flex-wrap:nowrap;padding:8px 14px;gap:8px 10px}
+  .topbar{padding:8px 14px;gap:8px 10px}
   .brand__name{display:none}
   .app--open .topbar{display:none}
   .picker{flex:1 1 auto;min-width:0}
````

Change `internal/web/static/js/app.js`:

````diff
--- a/internal/web/static/js/app.js
+++ b/internal/web/static/js/app.js
@@ -3,9 +3,11 @@ import ThemeController from "./theme_controller.js"
 import PickerController from "./picker_controller.js"
 import ScrollController from "./scroll_controller.js"
 import JumpController from "./jump_controller.js"
+import SearchController from "./search_controller.js"
 
 const application = Application.start()
 application.register("theme", ThemeController)
 application.register("picker", PickerController)
 application.register("scroll", ScrollController)
 application.register("jump", JumpController)
+application.register("search", SearchController)
````

`internal/web/static/js/search_controller.js`:

````js
import { Controller } from "./stimulus.js"

// Searches as the owner types. The form works without this controller; with
// it, the left column is replaced by results a moment after typing stops,
// and the address keeps the search so a reload or the back button returns
// to it.
export default class extends Controller {
  static targets = ["input"]
  static values = { results: String }

  disconnect() {
    clearTimeout(this.timer)
    this.pending?.abort()
  }

  type() {
    clearTimeout(this.timer)
    this.timer = setTimeout(() => this.load(), 250)
  }

  submit(event) {
    event.preventDefault()
    clearTimeout(this.timer)
    this.load()
  }

  clear() {
    this.inputTarget.value = ""
    this.load()
  }

  async load() {
    const q = this.inputTarget.value
    const url = new URL(this.resultsValue, window.location.origin)
    url.searchParams.set("q", q)
    this.pending?.abort()
    this.pending = new AbortController()
    let response
    try {
      response = await fetch(url, { signal: this.pending.signal, credentials: "same-origin" })
    } catch {
      return // replaced by a newer search, or offline
    }
    if (response.redirected) {
      window.location.assign(response.url) // signed out meanwhile
      return
    }
    if (!response.ok) return
    const list = document.getElementById("list")
    if (!list) return
    list.innerHTML = await response.text() // escaped by the server's templates
    list.scrollTop = 0
    const here = new URL(window.location.href)
    for (const name of ["q", "page", "person", "chats"]) here.searchParams.delete(name)
    if (q.trim() !== "") here.searchParams.set("q", q)
    window.history.replaceState(null, "", here)
  }
}
````

Change `internal/web/templates/conversation.html`:

````diff
--- a/internal/web/templates/conversation.html
+++ b/internal/web/templates/conversation.html
@@ -2,7 +2,7 @@
 <header class="conv-head">
 <a class="back only-phone" href="{{.BackHref}}" aria-label="Back to chats"><svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M15 5l-7 7 7 7"/></svg></a>
 <div class="conv-head__text"><h1>{{.Title}}</h1><p>{{.Subtitle}}</p></div>
-{{if .YearOptions}}<form class="jump only-phone" method="get" data-controller="jump"><label class="visually-hidden" for="jump-year">Jump to a year</label><select id="jump-year" name="date" data-action="jump#go">{{range .YearOptions}}<option value="{{.Value}}"{{if .Current}} selected{{end}}>{{.Label}}</option>{{end}}</select><button class="button button--quiet" type="submit" data-jump-target="button">Go</button></form>{{end}}
+{{if .YearOptions}}<form class="jump only-phone" method="get" data-controller="jump"><label class="visually-hidden" for="jump-year">Jump to a year</label>{{if .Query}}<input type="hidden" name="q" value="{{.Query}}">{{end}}<select id="jump-year" name="date" data-action="jump#go">{{range .YearOptions}}<option value="{{.Value}}"{{if .Current}} selected{{end}}>{{.Label}}</option>{{end}}</select><button class="button button--quiet" type="submit" data-jump-target="button">Go</button></form>{{end}}
 </header>
 <div class="conv-body">
 <div class="messages" data-controller="scroll" data-scroll-to-value="{{.ScrollTo}}">
@@ -21,7 +21,7 @@
 {{define "message"}}{{$m := .M}}{{if $m.Service}}<p class="service" id="{{$m.Anchor}}">{{$m.Text}}</p>{{else}}<article class="msg{{if $m.Outgoing}} msg--out{{end}}{{if $m.Target}} msg--target{{end}}" id="{{$m.Anchor}}">
 {{if $m.Sender}}<div class="msg__sender">{{$m.Sender}}</div>{{end}}{{if $m.ForwardedFrom}}<div class="msg__fwd">Forwarded from {{$m.ForwardedFrom}}</div>{{end}}
 {{range $m.Attachments}}{{template "attachment" (dict "A" . "CSRF" $.CSRF "Back" $.Back)}}
-{{end}}{{if $m.Text}}<p class="msg__text">{{$m.Text}}</p>{{end}}
+{{end}}{{if $m.Text}}<p class="msg__text">{{if $m.Marked}}{{template "pieces" $m.Marked}}{{else}}{{$m.Text}}{{end}}</p>{{end}}
 <div class="msg__meta"><time>{{$m.Time}}</time>{{if $m.Deleted}}<span class="tag tag--warn">Deleted at source on {{$m.Deleted}}</span>{{end}}</div>
 {{if $m.Versions}}<details class="versions"><summary>Edited at {{$m.Edited}}, show earlier</summary>{{range $m.Versions}}<p class="versions__item">{{.Text}} <span class="versions__when">until {{.Until}}</span></p>{{end}}</details>{{else if $m.Edited}}<div class="versions">Edited at {{$m.Edited}}</div>{{end}}
 </article>{{end}}{{end}}
````

Change `internal/web/templates/partials.html`:

````diff
--- a/internal/web/templates/partials.html
+++ b/internal/web/templates/partials.html
@@ -30,9 +30,11 @@
 {{define "topbar"}}<header class="topbar">
 {{template "brand"}}
 {{if .Picker}}{{template "picker" .Picker}}{{end}}
-<span class="topbar__space"></span>
+{{if .Search}}{{template "search" .Search}}{{else}}<span class="topbar__space"></span>{{end}}
 <button class="icon-button not-phone" type="button" data-controller="theme" data-action="theme#toggle" aria-label="Switch between the light and dark theme"><svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M20 14.5A8 8 0 019.5 4a8 8 0 1010.5 10.5z"/></svg></button>
 <a class="settings-link" href="/settings"><svg class="only-phone" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M4 7h10M18 7h2M4 17h4M12 17h8"/><circle cx="16" cy="7" r="2"/><circle cx="10" cy="17" r="2"/></svg><span class="settings-link__label">Settings</span></a>
 </header>{{end}}
 
 {{define "banner"}}{{if .Alert}}<div class="banner" role="alert"><span><strong>{{.Alert.Name}} is not archiving.</strong> {{.Alert.Detail}}</span><a class="button button--warn" href="/sources">Open sources</a></div>{{end}}{{end}}
+
+{{define "search"}}<form class="search" role="search" method="get" action="{{.Action}}" data-controller="search" data-search-results-value="{{.Results}}" data-action="submit->search#submit"><svg class="search__icon" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><circle cx="11" cy="11" r="7"/><path d="M20 20l-3.5-3.5"/></svg><input class="search__input" type="search" name="q" value="{{.Query}}" placeholder="Search messages, chats and people" aria-label="Search messages, chats and people" autocomplete="off" data-search-target="input" data-action="input->search#type keydown.esc->search#clear"></form>{{end}}
````

Change `internal/web/templates/source.html`:

````diff
--- a/internal/web/templates/source.html
+++ b/internal/web/templates/source.html
@@ -3,10 +3,8 @@
 {{template "topbar" .}}
 {{template "banner" .}}
 <div class="shell{{if .Conversation}} shell--open{{end}}">
-<aside class="list" aria-label="Chats">
-{{range .Chats}}<a class="row{{if .Current}} row--current{{end}}" href="{{.Href}}"{{if .Current}} aria-current="true"{{end}}><span class="avatar" aria-hidden="true">{{.Initials}}</span><span class="row__body"><span class="row__top"><span class="row__name">{{.Title}}</span><span class="row__when">{{.When}}</span></span><span class="row__preview">{{.Preview}}</span></span></a>
-{{else}}<p class="empty">Nothing has been archived here yet.</p>
-{{end}}{{if .MoreChatsHref}}<a class="more" href="{{.MoreChatsHref}}">Show more chats</a>{{end}}
+<aside class="list" id="list" aria-label="{{if .Results}}Search results{{else}}Chats{{end}}">
+{{template "list" .}}
 </aside>
 <section class="conversation" aria-label="Conversation">
 {{if .Conversation}}{{template "conversation" .}}{{else}}<p class="empty empty--center">Choose a chat on the left to read it.</p>{{end}}
@@ -14,3 +12,21 @@
 </div>
 </body>
 </html>{{end}}
+
+{{define "list"}}{{if .Results}}{{template "results" .Results}}{{else}}{{range .Chats}}<a class="row{{if .Current}} row--current{{end}}" href="{{.Href}}"{{if .Current}} aria-current="true"{{end}}><span class="avatar" aria-hidden="true">{{.Initials}}</span><span class="row__body"><span class="row__top"><span class="row__name">{{.Title}}</span><span class="row__when">{{.When}}</span></span><span class="row__preview">{{.Preview}}</span></span></a>
+{{else}}<p class="empty">Nothing has been archived here yet.</p>
+{{end}}{{if .MoreChatsHref}}<a class="more" href="{{.MoreChatsHref}}">Show more chats</a>{{end}}{{end}}{{end}}
+
+{{define "results"}}{{if .Person}}<div class="results__person"><h2 class="results__heading">Everything from {{.Person}}</h2><a class="results__back" href="{{.AllHref}}">All results</a></div>
+{{end}}{{if or .Chats .People}}<h2 class="results__heading">Chats and people</h2>
+{{range .Chats}}<a class="name-row" href="{{.Href}}"><span class="avatar" aria-hidden="true">{{.Initials}}</span><span class="row__body"><span class="row__name">{{template "pieces" .Title}}</span><span class="row__preview">{{.Detail}}</span></span></a>
+{{end}}{{range .People}}<a class="name-row" href="{{.Href}}"><span class="avatar avatar--person" aria-hidden="true"><svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><circle cx="12" cy="8" r="4"/><path d="M4 21a8 8 0 0116 0"/></svg></span><span class="row__body"><span class="row__name">{{template "pieces" .Name}}</span><span class="row__preview row__preview--accent">Everything from this person, in any chat</span></span></a>
+{{end}}{{end}}{{if .Hits}}{{if not .Person}}<h2 class="results__heading">Messages</h2>
+{{end}}{{range .Hits}}<a class="result{{if .Current}} result--current{{end}}" href="{{.Href}}"{{if .Current}} aria-current="true"{{end}}><span class="row__top"><span class="row__name">{{.Chat}}</span><span class="row__when">{{.When}}</span></span><span class="result__snippet">{{.Prefix}}{{template "pieces" .Snippet}}</span>{{range .Files}}<span class="result__file">File: {{template "pieces" .}}</span>{{end}}{{if .Deleted}}<span class="tag tag--warn">Deleted at source</span>{{end}}</a>
+{{end}}{{end}}{{if .Empty}}<p class="empty">Nothing matches “{{.Query}}”.</p>
+{{end}}{{if .Note}}<p class="results__note">{{.Note}}</p>
+{{end}}{{if .MoreHref}}<a class="more-link" href="{{.MoreHref}}">Show more messages</a>
+{{end}}{{if .FirstHref}}<a class="more-link" href="{{.FirstHref}}">First results</a>
+{{end}}{{end}}
+
+{{define "pieces"}}{{range .}}{{if .Mark}}<mark class="hit">{{.Text}}</mark>{{else}}{{.Text}}{{end}}{{end}}{{end}}
````

- [ ] **Step 4: Run the tests to verify they pass**

Run: `gofmt -l internal cmd; go vet ./... && go test -race -count=1 -timeout 180s ./...`
Expected: `gofmt -l` prints nothing; every package PASSES.

- [ ] **Step 5: Commit**

```bash
git add internal/web internal/web/static internal/web/static/js internal/web/templates
git commit -m "Search from the top bar: chats, people and messages, kept beside the conversation"
```

---

### Task 7: Sort and filters

The spec lets the owner switch to newest first and filter by attachments, deletions at source, service messages and dates. One line above the results holds "Best match / Newest first" and a Filters disclosure; every link from the results keeps them. This task can be dropped without touching the others (decision 2).

**Files:**
- Create: `internal/web/filters.go`
- Modify: `internal/web/search.go`, `internal/web/static/app.css`, `internal/web/static/js/search_controller.js`, `internal/web/templates/source.html`
- Test: `internal/web/filters_test.go` (new)

**Interfaces:**
- Consumes: `Results`, `results` (Task 6); `search.Query` filter fields.
- Produces: `type Filters`, `readFilters`, `(Filters).suffix/narrowing/active/apply/box`, `type FilterBox`, `Results.Filters *FilterBox`; template `filters`; parameters `sort=newest`, `files=1`, `deleted=1`, `service=1`, `from`, `to` (YYYY-MM-DD, the last day included).

- [ ] **Step 1: Write the failing tests**

`internal/web/filters_test.go`:

````go
package web

import (
	"fmt"
	"strings"
	"testing"

	"github.com/fenelon/arc5/internal/model"
)

// order returns the positions of each text in body, so tests can check
// which result comes first.
func order(t *testing.T, body string, texts ...string) []int {
	t.Helper()
	var at []int
	for _, text := range texts {
		i := strings.Index(body, text)
		if i < 0 {
			t.Fatalf("the page lacks %q", text)
		}
		at = append(at, i)
	}
	return at
}

func TestSearchFilters(t *testing.T) {
	m := newMarinaSite(t)
	m.say(m.acc, "family", "2", "Marina joined the group", noon, func(msg *model.Message) { msg.Kind = model.KindService })
	path := func(query string) string { return fmt.Sprintf("/s/%d?q=marina%s", m.acc, query) }
	const flight, deposit, keys, joined = "s flight lands", "Sent the deposit", "Dile a", "joined the group"

	body := m.page(path(""))
	has(t, body, `<summary class="filters__summary">Filters</summary>`, `class="filters__sort filters__sort--on">Best match`,
		fmt.Sprintf(`href="/s/%d?q=marina&amp;sort=newest"`, m.acc))
	lacks(t, body, joined)

	newest := m.page(path("&sort=newest"))
	if at := order(t, newest, keys, deposit, flight); !(at[0] < at[1] && at[1] < at[2]) {
		t.Errorf("newest first gave the order %v", at)
	}
	has(t, newest, `class="filters__sort filters__sort--on">Newest first`)

	for name, c := range map[string]struct {
		query      string
		want, lack []string
	}{
		"with attachments":  {"&files=1", []string{deposit}, []string{flight, keys}},
		"deleted at source": {"&deleted=1", []string{flight}, []string{deposit, keys}},
		"service messages":  {"&service=1", []string{joined, flight}, nil},
		"one day":           {"&from=2026-09-27&to=2026-09-27", []string{deposit}, []string{flight, keys}},
		"from a day on":     {"&from=2026-09-27", []string{deposit, keys}, []string{flight}},
		"a bad date":        {"&from=yesterday", []string{deposit, keys, flight}, nil},
	} {
		body := m.page(path(c.query))
		for _, w := range c.want {
			if !strings.Contains(body, w) {
				t.Errorf("%s: lacks %q", name, w)
			}
		}
		for _, l := range c.lack {
			if strings.Contains(body, l) {
				t.Errorf("%s: has %q", name, l)
			}
		}
	}

	filtered := m.page(path("&files=1&sort=newest"))
	has(t, filtered, `<summary class="filters__summary">Filters (1)</summary>`, `<details class="filters__more" open>`,
		`name="files" value="1" checked`, `<input type="hidden" name="sort" value="newest">`,
		fmt.Sprintf(`href="/s/%d/c/%d?at=%d&amp;q=marina&amp;sort=newest&amp;files=1"`, m.acc, m.dan, m.deposit),
		fmt.Sprintf(`href="/s/%d?q=marina&amp;sort=newest">Clear filters`, m.acc))
}
````

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go vet ./internal/web/; go test -count=1 ./internal/web/`
Expected: FAIL: `TestSearchFilters` reports the page lacks the filters.

- [ ] **Step 3: Write the implementation**

`internal/web/filters.go`:

````go
package web

import (
	"net/http"
	"net/url"
	"time"

	"github.com/fenelon/arc5/internal/search"
)

// Filters narrow a message search. They travel in the address, so every
// link from the results keeps them.
type Filters struct {
	Newest  bool   // newest first instead of best match
	Files   bool   // only messages with attachments
	Deleted bool   // only messages deleted at the source
	Service bool   // service messages too: joins, calls, pins
	From    string // first day, YYYY-MM-DD, or ""
	To      string // last day, YYYY-MM-DD, or ""
}

// FilterBox is the sort switch and the filter form above the results.
type FilterBox struct {
	Filters
	Action, Query                   string
	Active                          int // filters set, not counting the sort
	BestHref, NewestHref, ClearHref string
}

func readFilters(r *http.Request, loc *time.Location) Filters {
	q := r.URL.Query()
	f := Filters{Newest: q.Get("sort") == "newest", Files: q.Get("files") == "1", Deleted: q.Get("deleted") == "1", Service: q.Get("service") == "1"}
	if _, err := time.ParseInLocation(time.DateOnly, q.Get("from"), loc); err == nil {
		f.From = q.Get("from")
	}
	if _, err := time.ParseInLocation(time.DateOnly, q.Get("to"), loc); err == nil {
		f.To = q.Get("to")
	}
	return f
}

// suffix is the filters as address parameters, each starting with "&".
func (f Filters) suffix() string {
	s := ""
	if f.Newest {
		s += "&sort=newest"
	}
	return s + f.narrowing()
}

// narrowing is the filters other than the sort, as address parameters.
func (f Filters) narrowing() string {
	s := ""
	if f.Files {
		s += "&files=1"
	}
	if f.Deleted {
		s += "&deleted=1"
	}
	if f.Service {
		s += "&service=1"
	}
	if f.From != "" {
		s += "&from=" + url.QueryEscape(f.From)
	}
	if f.To != "" {
		s += "&to=" + url.QueryEscape(f.To)
	}
	return s
}

func (f Filters) active() int {
	n := 0
	for _, on := range []bool{f.Files, f.Deleted, f.Service, f.From != "", f.To != ""} {
		if on {
			n++
		}
	}
	return n
}

// apply sets the filters on a search. The last day is included whole.
func (f Filters) apply(q *search.Query, loc *time.Location) {
	if f.Newest {
		q.Sort = search.ByNewest
	}
	q.HasAttachment, q.DeletedOnly, q.IncludeService = f.Files, f.Deleted, f.Service
	if t, err := time.ParseInLocation(time.DateOnly, f.From, loc); err == nil {
		q.From = t
	}
	if t, err := time.ParseInLocation(time.DateOnly, f.To, loc); err == nil {
		q.To = t.AddDate(0, 0, 1)
	}
}

// box is the filter form for a search whose plain address is base ("?q=…").
func (f Filters) box(action, query, base string) *FilterBox {
	b := &FilterBox{Filters: f, Action: action, Query: query, Active: f.active()}
	b.BestHref = base + f.narrowing()
	b.NewestHref = base + "&sort=newest" + f.narrowing()
	b.ClearHref = base
	if f.Newest {
		b.ClearHref += "&sort=newest"
	}
	return b
}
````

Change `internal/web/search.go`:

````diff
--- a/internal/web/search.go
+++ b/internal/web/search.go
@@ -76,6 +76,7 @@ type Results struct {
 	Hits      []HitRow
 	MoreHref  string
 	FirstHref string
+	Filters   *FilterBox // nil for one person's messages
 	Empty     bool
 	Note      string
 }
@@ -112,11 +113,15 @@ func (s *Server) results(ctx context.Context, r *http.Request, sel selection) (*
 	if strings.TrimSpace(q) == "" {
 		return nil, nil
 	}
-	base := "/s/" + sel.Key + "?q=" + url.QueryEscape(q)
+	plain := "/s/" + sel.Key + "?q=" + url.QueryEscape(q)
+	filters := readFilters(r, s.deps.Location)
+	keep := "&q=" + url.QueryEscape(q) + filters.suffix() // what links from the results carry
+	base := plain + filters.suffix()
 	res := &Results{Query: q}
 	searcher := s.deps.Index.Searcher()
 	page := pageNumber(r)
 	query := search.Query{Text: q, AccountIDs: sel.AccountIDs, Limit: resultsPerPage, Offset: (page - 1) * resultsPerPage}
+	filters.apply(&query, s.deps.Location)
 	pageBase := base
 
 	if p := r.URL.Query().Get("person"); p != "" {
@@ -138,14 +143,17 @@ func (s *Server) results(ctx context.Context, r *http.Request, sel selection) (*
 		res.AllHref = base
 		query.Text, query.SenderID, query.Sort = "", id, search.ByNewest
 		pageBase = fmt.Sprintf("%s&person=%d", base, id)
-	} else if page == 1 {
+	} else {
+		res.Filters = filters.box("/s/"+sel.Key, q, plain)
+	}
+	if res.Person == "" && page == 1 {
 		names, err := searcher.Names(ctx, q, sel.AccountIDs, 0)
 		if err != nil {
 			return nil, err
 		}
 		for _, c := range names.Chats {
 			res.Chats = append(res.Chats, NameRow{
-				Href:     fmt.Sprintf("/s/%s/c/%d?q=%s", sel.Key, c.ChatID, url.QueryEscape(q)),
+				Href:     fmt.Sprintf("/s/%s/c/%d?%s", sel.Key, c.ChatID, keep[1:]),
 				Initials: initials(fragmentText(c.Title)), Title: pieces(c.Title),
 				Detail: kindLabel(c.Kind) + ", last message " + s.when(c.LastAt),
 			})
@@ -162,7 +170,7 @@ func (s *Server) results(ctx context.Context, r *http.Request, sel selection) (*
 	at, _ := strconv.ParseInt(r.URL.Query().Get("at"), 10, 64)
 	for _, h := range found.Hits {
 		row := HitRow{
-			Href: fmt.Sprintf("/s/%s/c/%d?at=%d&q=%s", sel.Key, h.ChatID, h.MessageID, url.QueryEscape(q)),
+			Href: fmt.Sprintf("/s/%s/c/%d?at=%d%s", sel.Key, h.ChatID, h.MessageID, keep),
 			Chat: h.ChatTitle, When: s.when(h.SentAt), Snippet: pieces(h.Snippet),
 			Deleted: h.Deleted, Current: h.MessageID == at,
 		}
````

Change `internal/web/static/app.css`:

````diff
--- a/internal/web/static/app.css
+++ b/internal/web/static/app.css
@@ -225,6 +225,17 @@ svg{display:block}
 .result__snippet{font-size:14px;line-height:1.45;overflow-wrap:anywhere}
 .result__file{font-size:13px;color:var(--muted);overflow-wrap:anywhere}
 .result .tag{align-self:flex-start}
+.filters{display:flex;flex-wrap:wrap;align-items:center;justify-content:space-between;gap:6px 12px;padding:10px 16px;border-bottom:1px solid var(--line-soft);font-size:13.5px}
+.filters__sorts{display:flex;gap:4px}
+.filters__sort{padding:4px 8px;color:var(--muted);text-decoration:none;border-radius:6px}
+.filters__sort--on{font-weight:600;color:var(--ink);background:var(--line-soft)}
+.filters__summary{cursor:pointer;font-weight:600;color:var(--accent)}
+.filters__more[open]{flex:1 1 100%}
+.filters__form{display:flex;flex-direction:column;gap:6px;margin-top:8px}
+.filters__dates{display:flex;flex-wrap:wrap;gap:8px 14px}
+.filters__dates input{height:36px;padding:0 8px;font:inherit;color:var(--ink);background:var(--pane);border:1px solid var(--line);border-radius:6px}
+.filters__actions{display:flex;align-items:center;gap:14px;margin-top:4px}
+.filters__actions a{font-weight:600;color:var(--accent)}
 mark.hit{padding:0 2px;color:var(--hit-ink);background:var(--hit);border-radius:3px}
 .msg--target:has(mark.hit){border-color:var(--hit)}
 .more-link{display:block;padding:14px 16px;font-weight:600;color:var(--accent)}
````

Change `internal/web/static/js/search_controller.js`:

````diff
--- a/internal/web/static/js/search_controller.js
+++ b/internal/web/static/js/search_controller.js
@@ -1,5 +1,8 @@
 import { Controller } from "./stimulus.js"
 
+// The filters a search keeps while the text changes.
+const filters = ["sort", "files", "deleted", "service", "from", "to"]
+
 // Searches as the owner types. The form works without this controller; with
 // it, the left column is replaced by results a moment after typing stops,
 // and the address keeps the search so a reload or the back button returns
@@ -33,6 +36,10 @@ export default class extends Controller {
     const q = this.inputTarget.value
     const url = new URL(this.resultsValue, window.location.origin)
     url.searchParams.set("q", q)
+    const current = new URLSearchParams(window.location.search)
+    for (const name of filters) {
+      if (current.has(name)) url.searchParams.set(name, current.get(name))
+    }
     this.pending?.abort()
     this.pending = new AbortController()
     let response
````

Change `internal/web/templates/source.html`:

````diff
--- a/internal/web/templates/source.html
+++ b/internal/web/templates/source.html
@@ -18,6 +18,7 @@
 {{end}}{{if .MoreChatsHref}}<a class="more" href="{{.MoreChatsHref}}">Show more chats</a>{{end}}{{end}}{{end}}
 
 {{define "results"}}{{if .Person}}<div class="results__person"><h2 class="results__heading">Everything from {{.Person}}</h2><a class="results__back" href="{{.AllHref}}">All results</a></div>
+{{end}}{{with .Filters}}{{template "filters" .}}
 {{end}}{{if or .Chats .People}}<h2 class="results__heading">Chats and people</h2>
 {{range .Chats}}<a class="name-row" href="{{.Href}}"><span class="avatar" aria-hidden="true">{{.Initials}}</span><span class="row__body"><span class="row__name">{{template "pieces" .Title}}</span><span class="row__preview">{{.Detail}}</span></span></a>
 {{end}}{{range .People}}<a class="name-row" href="{{.Href}}"><span class="avatar avatar--person" aria-hidden="true"><svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><circle cx="12" cy="8" r="4"/><path d="M4 21a8 8 0 0116 0"/></svg></span><span class="row__body"><span class="row__name">{{template "pieces" .Name}}</span><span class="row__preview row__preview--accent">Everything from this person, in any chat</span></span></a>
@@ -30,3 +31,14 @@
 {{end}}{{end}}
 
 {{define "pieces"}}{{range .}}{{if .Mark}}<mark class="hit">{{.Text}}</mark>{{else}}{{.Text}}{{end}}{{end}}{{end}}
+
+{{define "filters"}}<div class="filters"><span class="filters__sorts"><a href="{{.BestHref}}" class="filters__sort{{if not .Newest}} filters__sort--on{{end}}">Best match</a><a href="{{.NewestHref}}" class="filters__sort{{if .Newest}} filters__sort--on{{end}}">Newest first</a></span>
+<details class="filters__more"{{if .Active}} open{{end}}><summary class="filters__summary">Filters{{if .Active}} ({{.Active}}){{end}}</summary>
+<form class="filters__form" method="get" action="{{.Action}}"><input type="hidden" name="q" value="{{.Query}}">{{if .Newest}}<input type="hidden" name="sort" value="newest">{{end}}
+<label class="choice"><input type="checkbox" name="files" value="1"{{if .Files}} checked{{end}}> With attachments</label>
+<label class="choice"><input type="checkbox" name="deleted" value="1"{{if .Deleted}} checked{{end}}> Deleted at source</label>
+<label class="choice"><input type="checkbox" name="service" value="1"{{if .Service}} checked{{end}}> Joins, calls and pins too</label>
+<div class="filters__dates"><label>From <input type="date" name="from" value="{{.From}}"></label><label>To <input type="date" name="to" value="{{.To}}"></label></div>
+<div class="filters__actions"><button class="button button--quiet" type="submit">Apply</button>{{if .Active}}<a href="{{.ClearHref}}">Clear filters</a>{{end}}</div>
+</form>
+</details></div>{{end}}
````

- [ ] **Step 4: Run the tests to verify they pass**

Run: `gofmt -l internal cmd; go vet ./... && go test -race -count=1 -timeout 180s ./...`
Expected: `gofmt -l` prints nothing; every package PASSES.

- [ ] **Step 5: Commit**

```bash
git add internal/web internal/web/static internal/web/static/js internal/web/templates
git commit -m "Sort search results newest first and filter them"
```

---

### Task 8: The Sources page and its settings panel

Following the Sources mockup: tiles grouped by kind with a line saying what each source is doing, then the chosen source's panel with its name, a link to choose archived chats, its state and catch-up progress, the connector's own settings rendered from its fields, "Sign this source out" and "Remove this source and keep its archive".

**Files:**
- Create: `internal/web/sources_page.go`
- Modify: `internal/web/server.go`, `internal/web/sources.go`, `internal/web/static/app.css`, `internal/web/templates/sources.html`
- Test: `internal/web/chats_test.go`, `internal/web/fake_test.go` (new), `internal/web/server_test.go`, `internal/web/sources_test.go` (new)

**Interfaces:**
- Consumes: Tasks 3 and 4 (`RenameAccount`, `RemoveAccount`, `AccountChats`, `LastMessageTimes`, `SignOuter`, `FieldBool`).
- Produces:
  - `web.Runner` gains `Restart(accountID int64) error` and `SignOut(ctx, accountID int64) error` (`*connector.Runner` has both)
  - `type FieldView`, `Choice`, `ProgressView`, `SourcePanel`; `sourcesPage{Page; Groups; Panel; Saved; Problem}`
  - `(*Server).sourcesData`, `panel`, `account`, `live`, `newConnector` (recovers a panicking factory), `ago`, `lowerFirst`, `readSettings`
  - routes `GET /sources/{id}`, `POST /sources/{id}/name`, `POST /sources/{id}/settings`, `POST /sources/{id}/signout`, `POST /sources/{id}/remove`
  - templates `source-panel`, `setting-field`; tile lines read "Service, what it is doing"
  - test helpers: `fakeRunner.restarted`, `signedOut`, `signOutErr`; `fakeConnector`, `fakeFlow`, `(*site).fakeAccount` in `fake_test.go`

- [ ] **Step 1: Write the failing tests**

Change `internal/web/chats_test.go`:

````diff
--- a/internal/web/chats_test.go
+++ b/internal/web/chats_test.go
@@ -187,8 +187,8 @@ func TestSourceStates(t *testing.T) {
 		{AccountID: ids["Paused"], State: connector.StatePaused, LastError: "disk full"},
 	}
 	body := s.page("/sources")
-	has(t, body, "Catching up on history, 3 of 9 chats", "Connecting", "Not connected: connection reset",
-		"Paused, the archive could not be written: disk full", "Switched off", "Not running",
+	has(t, body, "Telegram, catching up on history, 3 of 9 chats", "Telegram, connecting", "Telegram, not connected: connection reset",
+		"Telegram, paused, the archive could not be written: disk full", "Telegram, switched off", "Telegram, not running", "Telegram, live",
 		`aria-label="Live"`, `aria-label="Catching up on history"`)
 	if strings.Count(body, "tile--warn") != 2 {
 		t.Errorf("want the two failing sources drawn with the attention border, got %d", strings.Count(body, "tile--warn"))
````

`internal/web/fake_test.go`:

````go
package web

import (
	"context"
	"errors"

	"github.com/fenelon/arc5/internal/connector"
)

// fakeConnector stands for a real connector on the Sources page: it has a
// setting of every kind, can be signed out, and logs in with two steps.
type fakeConnector struct{}

func (fakeConnector) Run(ctx context.Context, _ connector.Sink, _ connector.State, _ map[string]string) error {
	<-ctx.Done()
	return ctx.Err()
}

func (fakeConnector) Setup(state connector.State) connector.SetupFlow { return &fakeFlow{state: state} }

func (fakeConnector) Settings() []connector.Field {
	return []connector.Field{
		{Name: "speed", Label: "Speed", Help: "How fast history is fetched.", Kind: connector.FieldChoice, Choices: []string{"Slow", "Normal", "Fast"}, Default: "Normal"},
		{Name: "keep", Label: "Keep self-destructing media", Kind: connector.FieldBool},
		{Name: "months", Label: "Months of history", Kind: connector.FieldNumber, Default: "12"},
		{Name: "note", Label: "Note", Kind: connector.FieldText},
		{Name: "token", Label: "API key", Kind: connector.FieldSecret},
	}
}

func (fakeConnector) SignOut(_ context.Context, state connector.State) error {
	return state.Set("session", nil)
}

// fakeFlow asks for a phone number, then a code. The code is 12345.
type fakeFlow struct {
	state connector.State
	phone string
}

func (f *fakeFlow) Next(_ context.Context, input map[string]string) (connector.Step, error) {
	phone := connector.Step{Title: "Your phone number", Help: "With the country code.",
		Fields: []connector.Field{{Name: "phone", Label: "Phone number", Kind: connector.FieldText}}}
	code := connector.Step{Title: "The code", Help: "Sent to " + f.phone + ".",
		Fields: []connector.Field{{Name: "code", Label: "Code", Kind: connector.FieldText}, {Name: "password", Label: "Two-step password", Kind: connector.FieldSecret}}}
	switch {
	case input == nil:
		return phone, nil
	case f.phone == "":
		if input["phone"] == "" {
			return phone, errors.New("Type your phone number.")
		}
		f.phone = input["phone"]
		code.Help = "Sent to " + f.phone + "."
		return code, nil
	case input["code"] != "12345":
		return code, errors.New("That code is not right.")
	}
	if err := f.state.Set("session", []byte("signed in as "+f.phone)); err != nil {
		return code, err
	}
	return connector.Step{Done: true}, nil
}

// fakeAccount adds a source run by fakeConnector, registered as "fake".
func (s *site) fakeAccount(name string) int64 {
	s.t.Helper()
	if _, known := s.reg.Info("fake"); !known {
		s.reg.Register(connector.Info{Name: "fake", Title: "Fake", Display: connector.DisplayChats}, func() connector.Connector { return fakeConnector{} })
	}
	id, err := s.store.CreateAccount("fake", name, nil)
	if err != nil {
		s.t.Fatal(err)
	}
	return id
}
````

Change `internal/web/server_test.go`:

````diff
--- a/internal/web/server_test.go
+++ b/internal/web/server_test.go
@@ -1,6 +1,7 @@
 package web
 
 import (
+	"context"
 	"net/http"
 	"net/http/httptest"
 	"net/url"
@@ -16,17 +17,42 @@ import (
 )
 
 type fakeRunner struct {
-	status    []connector.Status
-	requested []int64
-	err       error
+	mu         sync.Mutex
+	status     []connector.Status
+	requested  []int64
+	restarted  []int64
+	signedOut  []int64
+	err        error
+	signOutErr error
+}
+
+func (f *fakeRunner) Status() []connector.Status {
+	f.mu.Lock()
+	defer f.mu.Unlock()
+	return append([]connector.Status(nil), f.status...)
 }
 
-func (f *fakeRunner) Status() []connector.Status { return f.status }
 func (f *fakeRunner) RequestMedia(id int64) error {
+	f.mu.Lock()
+	defer f.mu.Unlock()
 	f.requested = append(f.requested, id)
 	return f.err
 }
 
+func (f *fakeRunner) Restart(id int64) error {
+	f.mu.Lock()
+	defer f.mu.Unlock()
+	f.restarted = append(f.restarted, id)
+	return nil
+}
+
+func (f *fakeRunner) SignOut(_ context.Context, id int64) error {
+	f.mu.Lock()
+	defer f.mu.Unlock()
+	f.signedOut = append(f.signedOut, id)
+	return f.signOutErr
+}
+
 // site is a server with a browser's memory: it keeps cookies and the CSRF
 // token from the last page it saw.
 type site struct {
````

`internal/web/sources_test.go`:

````go
package web

import (
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/fenelon/arc5/internal/connector"
)

func TestSourcesPageShowsTheChosenSource(t *testing.T) {
	s := newSite(t, "")
	s.signIn()
	personal, work := s.fakeAccount("Personal"), s.fakeAccount("Work")
	s.say(personal, "Dan", "1", "hello", noon)
	s.say(personal, "Ana", "1", "hola", noon)

	body := s.page("/sources")
	has(t, body, "Settings for Personal", fmt.Sprintf(`href="/sources/%d"`, work),
		fmt.Sprintf(`href="/s/%d">Open its chats`, personal),
		`name="name" value="Personal"`,
		fmt.Sprintf(`href="/sources/%d/chats">Choose which of the 2 chats are archived`, personal),
		`<select id="field-speed" name="speed">`, `<option value="Normal" selected>Normal</option>`,
		`<input id="field-keep" name="keep" type="checkbox" value="true">`,
		`<input id="field-months" name="months" type="number" value="12">`,
		`<input id="field-token" name="token" type="password" value="" autocomplete="off"`,
		"Sign this source out", "Remove this source and keep its archive")
	if c := strings.Count(body, "tile--current"); c != 1 {
		t.Errorf("%d tiles marked current", c)
	}
	has(t, s.page(fmt.Sprintf("/sources/%d", work)), "Settings for Work", `name="name" value="Work"`)

	s.page(fmt.Sprintf("/s/%d", work))
	has(t, s.page("/sources"), "Settings for Work")

	for _, path := range []string{"/sources/999", "/sources/banana"} {
		if rec := s.get(path); rec.Code != http.StatusNotFound {
			t.Errorf("GET %s: %d, want 404", path, rec.Code)
		}
	}
}

func TestSourceTilesSayWhatTheyAreDoing(t *testing.T) {
	s := newSite(t, "")
	s.signIn()
	live, catching := s.fakeAccount("Personal"), s.fakeAccount("Work")
	s.say(live, "Dan", "1", "hello", noon)
	s.srv.deps.Now = func() time.Time { return noon.Add(2 * time.Minute) }
	s.runner.status = []connector.Status{
		{AccountID: live, State: connector.StateRunning, Progress: connector.Progress{Phase: connector.PhaseLive}},
		{AccountID: catching, State: connector.StateRunning, Progress: connector.Progress{Phase: connector.PhaseBackfill, ChatsDone: 37, ChatsTotal: 112}},
	}
	has(t, s.page("/sources"), "Fake, last message 2 minutes ago", "Fake, catching up on history, 37 of 112 chats")
	has(t, s.page(fmt.Sprintf("/sources/%d", catching)), `<progress class="progress" max="112" value="37">`, "37 of 112 chats")
}

func TestRenameSource(t *testing.T) {
	s := newSite(t, "")
	s.signIn()
	acc := s.fakeAccount("Personal")
	rec := s.post(fmt.Sprintf("/sources/%d/name", acc), url.Values{"name": {"  Home phone "}})
	if !redirectsTo(rec, fmt.Sprintf("/sources/%d?saved=name", acc)) {
		t.Fatalf("rename: %d to %q", rec.Code, rec.Header().Get("Location"))
	}
	if got, _ := s.store.Account(acc); got.Label != "Home phone" {
		t.Errorf("label = %q", got.Label)
	}
	bad := s.post(fmt.Sprintf("/sources/%d/name", acc), url.Values{"name": {"   "}})
	if bad.Code != http.StatusUnprocessableEntity || !strings.Contains(bad.Body.String(), `role="alert"`) {
		t.Errorf("an empty name: %d", bad.Code)
	}
}

func TestSaveSourceSettings(t *testing.T) {
	s := newSite(t, "")
	s.signIn()
	acc := s.fakeAccount("Personal")
	if err := s.store.UpdateAccountSettings(acc, map[string]string{"token": "old-secret", "internal": "kept"}); err != nil {
		t.Fatal(err)
	}
	form := url.Values{"speed": {"Fast"}, "keep": {"true"}, "months": {"6"}, "note": {"hi"}, "token": {""}}
	rec := s.post(fmt.Sprintf("/sources/%d/settings", acc), form)
	if !redirectsTo(rec, fmt.Sprintf("/sources/%d?saved=settings", acc)) {
		t.Fatalf("save: %d to %q", rec.Code, rec.Header().Get("Location"))
	}
	got, _ := s.store.Account(acc)
	want := map[string]string{"speed": "Fast", "keep": "true", "months": "6", "note": "hi", "token": "old-secret", "internal": "kept"}
	for k, v := range want {
		if got.Settings[k] != v {
			t.Errorf("setting %s = %q, want %q", k, got.Settings[k], v)
		}
	}
	if !slices.Contains(s.runner.restarted, acc) {
		t.Error("the connector was not restarted with its new settings")
	}
	has(t, s.page(fmt.Sprintf("/sources/%d?saved=settings", acc)), `<option value="Fast" selected>`, `name="keep" type="checkbox" value="true" checked`, "Saved.")
	lacks(t, s.page(fmt.Sprintf("/sources/%d", acc)), "old-secret")

	s.post(fmt.Sprintf("/sources/%d/settings", acc), url.Values{"speed": {"Fast"}, "months": {"6"}, "token": {"new-secret"}})
	got, _ = s.store.Account(acc)
	if got.Settings["keep"] != "" || got.Settings["token"] != "new-secret" {
		t.Errorf("unticking and a new secret: %v", got.Settings)
	}

	for name, f := range map[string]url.Values{
		"a choice not offered": {"speed": {"Ludicrous"}, "months": {"6"}},
		"not a number":         {"speed": {"Fast"}, "months": {"six"}},
	} {
		rec := s.post(fmt.Sprintf("/sources/%d/settings", acc), f)
		if rec.Code != http.StatusUnprocessableEntity || !strings.Contains(rec.Body.String(), `role="alert"`) {
			t.Errorf("%s: %d", name, rec.Code)
		}
	}
}

func TestSignOutSource(t *testing.T) {
	s := newSite(t, "")
	s.signIn()
	acc := s.fakeAccount("Personal")
	rec := s.post(fmt.Sprintf("/sources/%d/signout", acc), nil)
	if !redirectsTo(rec, fmt.Sprintf("/sources/%d?saved=signout", acc)) || !slices.Contains(s.runner.signedOut, acc) {
		t.Fatalf("sign out: %d to %q, signed out %v", rec.Code, rec.Header().Get("Location"), s.runner.signedOut)
	}
	plain := s.account("Old", "telegram") // no sign-out
	lacks(t, s.page(fmt.Sprintf("/sources/%d", plain)), "Sign this source out")
	if rec := s.post(fmt.Sprintf("/sources/%d/signout", plain), nil); rec.Code != http.StatusNotFound {
		t.Errorf("signing out a source that cannot: %d", rec.Code)
	}
}

func TestRemoveSourceKeepsItsArchive(t *testing.T) {
	s := newSite(t, "")
	s.signIn()
	acc := s.fakeAccount("Personal")
	s.say(acc, "Dan", "1", "still here", noon)
	rec := s.post(fmt.Sprintf("/sources/%d/remove", acc), nil)
	if !redirectsTo(rec, "/sources?saved=removed") || !slices.Contains(s.runner.restarted, acc) {
		t.Fatalf("remove: %d to %q, restarted %v", rec.Code, rec.Header().Get("Location"), s.runner.restarted)
	}
	if got, _ := s.store.Account(acc); got.RemovedAt.IsZero() || got.Enabled {
		t.Fatalf("account after removal = %+v", got)
	}
	has(t, s.page("/sources?saved=removed"), "Removed, archive kept", "Fake, removed")
	panel := s.page(fmt.Sprintf("/sources/%d", acc))
	has(t, panel, "This source was removed on", `name="name" value="Personal"`)
	lacks(t, panel, "Sign this source out", "Remove this source", `name="speed"`)
	has(t, s.page(fmt.Sprintf("/s/%d", acc)), "still here")
	for _, action := range []string{"settings", "signout", "remove"} {
		if rec := s.post(fmt.Sprintf("/sources/%d/%s", acc, action), nil); rec.Code != http.StatusConflict {
			t.Errorf("%s on a removed source: %d, want 409", action, rec.Code)
		}
	}
}
````

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go vet ./internal/web/; go test -count=1 ./internal/web/`
Expected: FAIL: `TestSourceStates` lacks the new tile lines ("Telegram, catching up on history, 3 of 9 chats"); `TestSourcesPageShowsTheChosenSource` lacks "Settings for Personal"; the other new tests fail likewise.

- [ ] **Step 3: Write the implementation**

Change `internal/web/server.go`:

````diff
--- a/internal/web/server.go
+++ b/internal/web/server.go
@@ -3,6 +3,7 @@ package web
 
 import (
 	"bytes"
+	"context"
 	"crypto/sha256"
 	"crypto/subtle"
 	"embed"
@@ -40,6 +41,11 @@ type Index interface {
 type Runner interface {
 	Status() []connector.Status
 	RequestMedia(attachmentID int64) error
+	// Restart stops an account's connector and starts it again if the
+	// account is enabled, after its settings change.
+	Restart(accountID int64) error
+	// SignOut has an account's connector end its session at the source.
+	SignOut(ctx context.Context, accountID int64) error
 }
 
 type Deps struct {
@@ -106,6 +112,11 @@ func (s *Server) routes() {
 	s.mux.Handle("GET /s/{source}/c/{chat}", s.signedIn(s.conversationView))
 	s.mux.Handle("GET /s/{source}/results", s.signedIn(s.resultsFragment))
 	s.mux.Handle("GET /sources", s.signedIn(s.sourcesList))
+	s.mux.Handle("GET /sources/{id}", s.signedIn(s.sourceSettings))
+	s.mux.Handle("POST /sources/{id}/name", s.signedIn(s.renameSource))
+	s.mux.Handle("POST /sources/{id}/settings", s.signedIn(s.saveSourceSettings))
+	s.mux.Handle("POST /sources/{id}/signout", s.signedIn(s.signOutSource))
+	s.mux.Handle("POST /sources/{id}/remove", s.signedIn(s.removeSource))
 	s.mux.Handle("GET /settings", s.signedIn(s.settings))
 	s.mux.Handle("POST /settings/appearance", s.signedIn(s.saveAppearance))
 	s.mux.Handle("POST /settings/languages", s.signedIn(s.saveLanguages))
````

Change `internal/web/sources.go`:

````diff
--- a/internal/web/sources.go
+++ b/internal/web/sources.go
@@ -55,6 +55,9 @@ func (s *Server) sources() ([]source, error) {
 func describe(src *source, st connector.Status, running bool) {
 	set := func(state, label string) { src.State, src.StateLabel, src.Detail = state, label, label }
 	switch {
+	case !src.Account.RemovedAt.IsZero():
+		set("", "Removed")
+		return
 	case !src.Account.Enabled:
 		set(stateOffline, "Switched off")
 	case !running, st.State == connector.StateStopped:
@@ -115,14 +118,15 @@ type selection struct {
 }
 
 var kinds = []struct {
-	display connector.Display
-	key     string
-	title   string
-	one     string
-	many    string
+	display      connector.Display
+	key          string
+	title        string
+	sourcesTitle string // the group's heading on the Sources page
+	one          string
+	many         string
 }{
-	{connector.DisplayChats, "chats", "Messengers", "messenger", "messengers"},
-	{connector.DisplayMail, "mail", "Mail", "mailbox", "mailboxes"},
+	{connector.DisplayChats, "chats", "Messengers", "Messengers, shown as chats", "messenger", "messengers"},
+	{connector.DisplayMail, "mail", "Mail", "Mailboxes, shown as mail", "mailbox", "mailboxes"},
 }
 
 func (s *Server) selection(key string, all []source) (selection, bool) {
@@ -191,30 +195,3 @@ func (s *Server) chrome(r *http.Request, sess archive.Session, title string, all
 	}
 	return p
 }
-
-// sourcesPage lists every source with its state.
-type sourcesPage struct {
-	Page
-	Groups []PickerGroup
-}
-
-func (s *Server) sourcesList(w http.ResponseWriter, r *http.Request, sess archive.Session) {
-	all, err := s.sources()
-	if err != nil {
-		s.fail(w, err)
-		return
-	}
-	data := sourcesPage{Page: s.chrome(r, sess, "Sources", all, "")}
-	for _, k := range kinds {
-		group := PickerGroup{Title: k.title}
-		for _, src := range all {
-			if src.Info.Display == k.display {
-				group.Tiles = append(group.Tiles, src.tile(""))
-			}
-		}
-		if len(group.Tiles) > 0 {
-			data.Groups = append(data.Groups, group)
-		}
-	}
-	s.render(w, http.StatusOK, "sources", data)
-}
````

`internal/web/sources_page.go`:

````go
package web

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/fenelon/arc5/internal/archive"
	"github.com/fenelon/arc5/internal/connector"
)

// maxFieldRunes caps a text setting.
const maxFieldRunes = 1000

// FieldView is one of a connector's settings, as a form shows it.
type FieldView struct {
	ID, Name, Label, Help, Kind, Value string
	Checked                            bool
	Choices                            []Choice
}

type Choice struct {
	Value    string
	Selected bool
}

// ProgressView is how far a source has caught up on its history.
type ProgressView struct {
	Done, Total int
}

// SourcePanel is the chosen source's settings on the Sources page.
type SourcePanel struct {
	ID                  int64
	Name, Service, Logo string
	OpenHref, ChatsHref string
	Chats               int
	Removed             string // the day it was removed, or ""
	Status              string
	Progress            *ProgressView
	Fields              []FieldView
	CanSignOut          bool
}

type sourcesPage struct {
	Page
	Groups  []PickerGroup
	Panel   *SourcePanel
	Saved   string
	Problem string
}

// lowerFirst makes a label fit after a comma: "Needs login" → "needs login".
func lowerFirst(s string) string {
	r, size := utf8.DecodeRuneInString(s)
	return string(unicode.ToLower(r)) + s[size:]
}

// ago says how long ago t was, for "last message 2 minutes ago".
func (s *Server) ago(t time.Time) string {
	d := s.deps.Now().Sub(t)
	switch {
	case d < time.Minute:
		return "less than a minute ago"
	case d < time.Hour:
		return plural(int(d/time.Minute), "minute", "minutes") + " ago"
	case d < 24*time.Hour:
		return plural(int(d/time.Hour), "hour", "hours") + " ago"
	}
	return "on " + s.when(t)
}

// newConnector builds a connector to read its settings or sign it out. A
// connector's factory is not this package's code; a panic in it must not
// break the page.
func (s *Server) newConnector(name string) (c connector.Connector, ok bool) {
	defer func() {
		if recover() != nil {
			c, ok = nil, false
		}
	}()
	c, ok = s.deps.Registry.New(name)
	return c, ok && c != nil
}

// sourceLine is a source's line on its tile: service and what it is doing.
func (s *Server) sourceLine(src source, last map[int64]time.Time) string {
	detail := src.Detail
	if src.State == stateLive {
		if t, ok := last[src.Account.ID]; ok {
			detail = "last message " + s.ago(t)
		}
	}
	return src.Info.Title + ", " + lowerFirst(detail)
}

func (s *Server) sourcesData(r *http.Request, sess archive.Session, chosen string) (sourcesPage, error) {
	all, err := s.sources()
	if err != nil {
		return sourcesPage{}, err
	}
	data := sourcesPage{Page: s.chrome(r, sess, "Sources", all, ""), Saved: r.URL.Query().Get("saved")}
	if len(all) == 0 {
		return data, nil
	}
	last, err := s.deps.Store.LastMessageTimes()
	if err != nil {
		return sourcesPage{}, err
	}
	var picked *source
	if chosen == "" {
		chosen = s.remembered(r, all)
	}
	for i := range all {
		if all[i].key() == chosen {
			picked = &all[i]
		}
	}
	if picked == nil {
		picked = &all[0]
		for i := range all {
			if all[i].Account.RemovedAt.IsZero() {
				picked = &all[i]
				break
			}
		}
	}
	tile := func(src source) Tile {
		t := src.tile("")
		t.Href, t.Current, t.Detail = "/sources/"+src.key(), src.key() == picked.key(), s.sourceLine(src, last)
		return t
	}
	for _, k := range kinds {
		group := PickerGroup{Title: k.sourcesTitle}
		for _, src := range all {
			if src.Info.Display == k.display && src.Account.RemovedAt.IsZero() {
				group.Tiles = append(group.Tiles, tile(src))
			}
		}
		if len(group.Tiles) > 0 {
			data.Groups = append(data.Groups, group)
		}
	}
	removed := PickerGroup{Title: "Removed, archive kept"}
	for _, src := range all {
		if !src.Account.RemovedAt.IsZero() {
			removed.Tiles = append(removed.Tiles, tile(src))
		}
	}
	if len(removed.Tiles) > 0 {
		data.Groups = append(data.Groups, removed)
	}
	data.Panel, err = s.panel(*picked)
	return data, err
}

func (s *Server) panel(src source) (*SourcePanel, error) {
	acc := src.Account
	p := &SourcePanel{
		ID: acc.ID, Name: acc.Label, Service: src.Info.Title, Logo: logoFor(src.Info.Name),
		OpenHref: "/s/" + src.key(), ChatsHref: fmt.Sprintf("/sources/%d/chats", acc.ID), Status: src.Detail,
	}
	chats, err := s.deps.Store.AccountChats(acc.ID)
	if err != nil {
		return nil, err
	}
	p.Chats = len(chats)
	if !acc.RemovedAt.IsZero() {
		p.Removed = acc.RemovedAt.In(s.deps.Location).Format("2 January 2006")
		return p, nil
	}
	for _, st := range s.deps.Runner.Status() {
		if st.AccountID == acc.ID && st.Progress.ChatsTotal > 0 && st.Progress.Phase == connector.PhaseBackfill {
			p.Progress = &ProgressView{Done: st.Progress.ChatsDone, Total: st.Progress.ChatsTotal}
		}
	}
	c, ok := s.newConnector(acc.Connector)
	if !ok {
		return p, nil
	}
	_, p.CanSignOut = c.(connector.SignOuter)
	for _, f := range c.Settings() {
		value, set := acc.Settings[f.Name]
		if !set {
			value = f.Default
		}
		v := FieldView{ID: "field-" + f.Name, Name: f.Name, Label: f.Label, Help: f.Help, Kind: string(f.Kind), Value: value}
		switch f.Kind {
		case connector.FieldSecret:
			v.Value = "" // never sent back to the browser
		case connector.FieldBool:
			v.Checked = value == "true"
		case connector.FieldChoice:
			for _, choice := range f.Choices {
				v.Choices = append(v.Choices, Choice{Value: choice, Selected: choice == value})
			}
		}
		p.Fields = append(p.Fields, v)
	}
	return p, nil
}

func (s *Server) sourcesList(w http.ResponseWriter, r *http.Request, sess archive.Session) {
	data, err := s.sourcesData(r, sess, "")
	if err != nil {
		s.fail(w, err)
		return
	}
	s.render(w, http.StatusOK, "sources", data)
}

// account reads the {id} of a /sources/{id} address. It answers the request
// itself when there is no such account.
func (s *Server) account(w http.ResponseWriter, r *http.Request) (archive.Account, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return archive.Account{}, false
	}
	acc, err := s.deps.Store.Account(id)
	if errors.Is(err, archive.ErrNotFound) {
		http.NotFound(w, r)
		return archive.Account{}, false
	}
	if err != nil {
		s.fail(w, err)
		return archive.Account{}, false
	}
	return acc, true
}

// live is account for actions that make no sense on a removed source.
func (s *Server) live(w http.ResponseWriter, r *http.Request) (archive.Account, bool) {
	acc, ok := s.account(w, r)
	if ok && !acc.RemovedAt.IsZero() {
		http.Error(w, "This source was removed. Its archive is kept, but it has no settings any more.", http.StatusConflict)
		return archive.Account{}, false
	}
	return acc, ok
}

func (s *Server) sourceSettings(w http.ResponseWriter, r *http.Request, sess archive.Session) {
	acc, ok := s.account(w, r)
	if !ok {
		return
	}
	data, err := s.sourcesData(r, sess, strconv.FormatInt(acc.ID, 10))
	if err != nil {
		s.fail(w, err)
		return
	}
	s.render(w, http.StatusOK, "sources", data)
}

// refuseSource shows a source's panel again with a problem.
func (s *Server) refuseSource(w http.ResponseWriter, r *http.Request, sess archive.Session, acc archive.Account, problem string) {
	data, err := s.sourcesData(r, sess, strconv.FormatInt(acc.ID, 10))
	if err != nil {
		s.fail(w, err)
		return
	}
	data.Saved, data.Problem = "", problem
	s.render(w, http.StatusUnprocessableEntity, "sources", data)
}

func (s *Server) renameSource(w http.ResponseWriter, r *http.Request, sess archive.Session) {
	acc, ok := s.account(w, r)
	if !ok {
		return
	}
	switch err := s.deps.Store.RenameAccount(acc.ID, r.PostFormValue("name")); {
	case errors.Is(err, archive.ErrInvalid):
		s.refuseSource(w, r, sess, acc, "Give the source a name.")
	case err != nil:
		s.fail(w, err)
	default:
		redirect(w, r, fmt.Sprintf("/sources/%d?saved=name", acc.ID))
	}
}

// readSettings checks a settings form against the connector's fields and
// returns the account's settings with the form's values in place.
func readSettings(fields []connector.Field, current map[string]string, r *http.Request) (map[string]string, error) {
	next := make(map[string]string, len(current)+len(fields))
	for k, v := range current {
		next[k] = v
	}
	for _, f := range fields {
		value := strings.TrimSpace(r.PostFormValue(f.Name))
		switch f.Kind {
		case connector.FieldBool:
			value = map[bool]string{true: "true", false: ""}[value == "true"]
		case connector.FieldSecret:
			if value == "" {
				continue // left empty: keep the one stored
			}
		case connector.FieldChoice:
			if !containsString(f.Choices, value) {
				return nil, fmt.Errorf("Choose one of the offered values for %s.", f.Label)
			}
		case connector.FieldNumber:
			if _, err := strconv.ParseInt(value, 10, 64); err != nil && value != "" {
				return nil, fmt.Errorf("%s needs a whole number.", f.Label)
			}
		}
		if utf8.RuneCountInString(value) > maxFieldRunes {
			return nil, fmt.Errorf("%s is too long.", f.Label)
		}
		if value == "" {
			delete(next, f.Name)
		} else {
			next[f.Name] = value
		}
	}
	return next, nil
}

func (s *Server) saveSourceSettings(w http.ResponseWriter, r *http.Request, sess archive.Session) {
	acc, ok := s.live(w, r)
	if !ok {
		return
	}
	c, known := s.newConnector(acc.Connector)
	if !known {
		http.NotFound(w, r)
		return
	}
	next, err := readSettings(c.Settings(), acc.Settings, r)
	if err != nil {
		s.refuseSource(w, r, sess, acc, err.Error())
		return
	}
	if err := s.deps.Store.UpdateAccountSettings(acc.ID, next); err != nil {
		s.fail(w, err)
		return
	}
	if err := s.deps.Runner.Restart(acc.ID); err != nil {
		s.fail(w, err)
		return
	}
	redirect(w, r, fmt.Sprintf("/sources/%d?saved=settings", acc.ID))
}

func (s *Server) signOutSource(w http.ResponseWriter, r *http.Request, _ archive.Session) {
	acc, ok := s.live(w, r)
	if !ok {
		return
	}
	c, known := s.newConnector(acc.Connector)
	if _, can := c.(connector.SignOuter); !known || !can {
		http.NotFound(w, r)
		return
	}
	switch err := s.deps.Runner.SignOut(r.Context(), acc.ID); {
	case errors.Is(err, connector.ErrCannotSignOut):
		http.NotFound(w, r)
	case err != nil:
		s.fail(w, err)
	default:
		redirect(w, r, fmt.Sprintf("/sources/%d?saved=signout", acc.ID))
	}
}

// removeSource stops a source for good and forgets its login. Its messages
// stay in the archive.
func (s *Server) removeSource(w http.ResponseWriter, r *http.Request, _ archive.Session) {
	acc, ok := s.live(w, r)
	if !ok {
		return
	}
	if err := s.deps.Store.SetAccountEnabled(acc.ID, false); err != nil {
		s.fail(w, err)
		return
	}
	// Stop the connector before its state is deleted, so it cannot write it back.
	if err := s.deps.Runner.Restart(acc.ID); err != nil {
		s.fail(w, err)
		return
	}
	if err := s.deps.Store.RemoveAccount(acc.ID); err != nil {
		s.fail(w, err)
		return
	}
	redirect(w, r, "/sources?saved=removed")
}
````

Change `internal/web/static/app.css`:

````diff
--- a/internal/web/static/app.css
+++ b/internal/web/static/app.css
@@ -240,6 +240,36 @@ mark.hit{padding:0 2px;color:var(--hit-ink);background:var(--hit);border-radius:
 .msg--target:has(mark.hit){border-color:var(--hit)}
 .more-link{display:block;padding:14px 16px;font-weight:600;color:var(--accent)}
 
+/* Sources page: tiles, then the chosen source's panel. */
+.page-head{display:flex;flex-wrap:wrap;align-items:center;justify-content:space-between;gap:12px}
+.panel{margin-top:26px;background:var(--pane);border:2px solid var(--accent);border-radius:12px;overflow:hidden}
+.panel__head{display:flex;align-items:center;gap:12px;padding:14px 20px;background:var(--accent-soft);border-bottom:1px solid var(--line)}
+.panel__title{flex:1 1 auto;min-width:0;font-weight:650;font-size:19px}
+.panel__open,.panel__link{font-weight:600;color:var(--accent)}
+.panel__note{margin:14px 20px 0}
+.panel__grid{display:grid;grid-template-columns:repeat(3,minmax(0,1fr))}
+.panel__cell{display:flex;flex-direction:column;gap:6px;padding:18px 20px}
+.panel__cell + .panel__cell{border-left:1px solid var(--line-soft)}
+.panel__label{font-weight:650;font-size:15px}
+.panel__help{font-size:13.5px;color:var(--muted)}
+.progress-line{display:flex;justify-content:space-between;gap:10px;font-size:14px}
+.progress{width:100%;height:8px;appearance:none;border:0;border-radius:4px;background:var(--line-soft);overflow:hidden}
+.progress::-webkit-progress-bar{background:var(--line-soft)}
+.progress::-webkit-progress-value{background:var(--accent)}
+.progress::-moz-progress-bar{background:var(--accent)}
+.panel__removed{padding:16px 20px;color:var(--muted);border-top:1px solid var(--line-soft)}
+.panel__settings{padding:18px 20px;border-top:1px solid var(--line-soft)}
+.panel__fields{display:grid;grid-template-columns:repeat(auto-fill,minmax(240px,1fr));gap:4px 24px;margin-bottom:14px}
+.setting select,.setting input[type=text],.setting input[type=number],.setting input[type=password]{height:44px;padding:0 12px;font:inherit;color:var(--ink);background:var(--pane);border:1px solid var(--line);border-radius:8px}
+.setting--bool{margin-bottom:14px}
+.panel__foot{display:flex;flex-wrap:wrap;align-items:flex-start;gap:10px 26px;padding:14px 20px;background:var(--bg);border-top:1px solid var(--line-soft)}
+.panel__foot form{margin:0}
+.link-button--warn{color:var(--warn-ink);font-weight:600;text-decoration:underline;list-style:none}
+.confirm summary{display:flex;align-items:center;min-height:44px;cursor:pointer}
+.confirm summary::-webkit-details-marker{display:none}
+.confirm__body{display:flex;flex-direction:column;gap:10px;margin-top:10px;max-width:520px}
+.button--warn{color:var(--warn-bg);background:var(--warn-ink)}
+
 /* Settings. */
 .settings-link{display:inline-flex;align-items:center;gap:6px;min-height:44px;padding:0 4px;font-weight:450;color:var(--ink);text-decoration:none}
 .settings-link svg{width:20px;height:20px}
@@ -273,6 +303,8 @@ mark.hit{padding:0 2px;color:var(--hit-ink);background:var(--hit);border-radius:
   .search{order:10;flex:1 1 100%}
   .settings-link__label{display:none}
   .checks{grid-template-columns:repeat(2,minmax(0,1fr))}
+  .panel__grid{grid-template-columns:1fr}
+  .panel__cell + .panel__cell{border-left:0;border-top:1px solid var(--line-soft)}
   .topbar{padding:8px 14px;gap:8px 10px}
   .brand__name{display:none}
   .app--open .topbar{display:none}
````

Change `internal/web/templates/sources.html`:

````diff
--- a/internal/web/templates/sources.html
+++ b/internal/web/templates/sources.html
@@ -3,14 +3,55 @@
 {{template "topbar" .}}
 {{template "banner" .}}
 <main class="plain plain--wide">
-<h1>Sources</h1>
-<p>Each source is one account arc5 keeps a copy of. Its type decides how it is shown: messengers as chats, mailboxes as mail.</p>
+<div class="page-head"><h1>Sources</h1></div>
+{{if eq .Saved "removed"}}<p class="saved" role="status">Removed. Its messages stay in the archive.</p>{{end}}
 {{range .Groups}}<h2 class="group-heading">{{.Title}}</h2>
 <div class="tiles">
-{{range .Tiles}}<a class="tile tile--wide{{if .Warn}} tile--warn{{end}}" href="{{.Href}}">{{template "logo" .Logo}}<span class="tile__text"><span class="tile__name">{{.Name}}</span><span class="tile__sub">{{.Service}}. {{.Detail}}</span></span>{{template "state" .}}</a>
+{{range .Tiles}}<a class="tile tile--wide{{if .Current}} tile--current{{end}}{{if .Warn}} tile--warn{{end}}" href="{{.Href}}"{{if .Current}} aria-current="true"{{end}}>{{template "logo" .Logo}}<span class="tile__text"><span class="tile__name">{{.Name}}</span><span class="tile__sub">{{.Detail}}</span></span>{{template "state" .}}</a>
 {{end}}</div>
 {{else}}<p class="empty">No sources yet.</p>
 {{end}}
+{{with .Panel}}{{template "source-panel" (dict "P" . "Page" $)}}{{end}}
 </main>
 </body>
 </html>{{end}}
+
+{{define "source-panel"}}{{$p := .P}}{{$page := .Page}}<section class="panel" aria-labelledby="panel-title">
+<header class="panel__head">{{template "logo" $p.Logo}}<h2 class="panel__title" id="panel-title">Settings for {{$p.Name}}</h2><a class="panel__open" href="{{$p.OpenHref}}">Open its chats</a></header>
+{{if $page.Problem}}<p class="alert panel__note" role="alert">{{$page.Problem}}</p>
+{{else if eq $page.Saved "name" "settings"}}<p class="saved panel__note" role="status">Saved.{{if eq $page.Saved "settings"}} The source starts again with them.{{end}}</p>
+{{else if eq $page.Saved "signout"}}<p class="saved panel__note" role="status">Signed out. It stops archiving until you log in again.</p>
+{{end}}<div class="panel__grid">
+<div class="panel__cell">
+<h3 class="panel__label">Name</h3>
+<p class="panel__help">Shown in the source picker</p>
+<form class="inline-form" method="post" action="/sources/{{$p.ID}}/name"><input type="hidden" name="csrf" value="{{$page.CSRF}}"><div class="field"><label class="visually-hidden" for="source-name">Name</label><input id="source-name" name="name" value="{{$p.Name}}" maxlength="100" required></div><button class="button" type="submit">Rename</button></form>
+</div>
+<div class="panel__cell">
+<h3 class="panel__label">Chats</h3>
+<p class="panel__help">Private chats and groups are archived as they appear. Channels and bots only when you switch them on.</p>
+<p><a class="panel__link" href="{{$p.ChatsHref}}">Choose which of the {{$p.Chats}} chats are archived</a></p>
+</div>
+<div class="panel__cell">
+<h3 class="panel__label">State</h3>
+{{if $p.Progress}}<div class="progress-line"><span>Catching up on history</span><span>{{$p.Progress.Done}} of {{$p.Progress.Total}} chats</span></div>
+<progress class="progress" max="{{$p.Progress.Total}}" value="{{$p.Progress.Done}}">{{$p.Progress.Done}} of {{$p.Progress.Total}} chats</progress>
+{{else}}<p>{{$p.Status}}</p>
+{{end}}<p class="panel__help">New messages are saved the moment they arrive, whatever else it is doing.</p>
+</div>
+</div>
+{{if $p.Removed}}<p class="panel__removed">This source was removed on {{$p.Removed}}. Its messages stay in the archive and in search; nothing is fetched for it any more.</p>
+{{else}}{{if $p.Fields}}<form class="panel__settings" method="post" action="/sources/{{$p.ID}}/settings"><input type="hidden" name="csrf" value="{{$page.CSRF}}">
+<div class="panel__fields">{{range $p.Fields}}{{template "setting-field" .}}{{end}}</div>
+<button class="button" type="submit">Save</button>
+</form>
+{{end}}<footer class="panel__foot">{{if $p.CanSignOut}}<form method="post" action="/sources/{{$p.ID}}/signout"><input type="hidden" name="csrf" value="{{$page.CSRF}}"><button class="link-button link-button--accent" type="submit">Sign this source out</button></form>{{end}}
+<details class="confirm"><summary class="link-button link-button--warn">Remove this source and keep its archive</summary>
+<div class="confirm__body"><p>{{$p.Name}} stops archiving and its login is forgotten. Its messages stay in the archive and in search.</p>
+<form method="post" action="/sources/{{$p.ID}}/remove"><input type="hidden" name="csrf" value="{{$page.CSRF}}"><button class="button button--warn" type="submit">Remove {{$p.Name}}</button></form></div></details>
+</footer>{{end}}
+</section>{{end}}
+
+{{define "setting-field"}}{{if eq .Kind "bool"}}<div class="setting setting--bool"><label class="choice"><input id="{{.ID}}" name="{{.Name}}" type="checkbox" value="true"{{if .Checked}} checked{{end}}> {{.Label}}</label>{{if .Help}}<p class="panel__help">{{.Help}}</p>{{end}}</div>
+{{else}}<div class="field setting"><label for="{{.ID}}">{{.Label}}</label>{{if eq .Kind "choice"}}<select id="{{.ID}}" name="{{.Name}}">{{range .Choices}}<option value="{{.Value}}"{{if .Selected}} selected{{end}}>{{.Value}}</option>{{end}}</select>{{else if eq .Kind "secret"}}<input id="{{.ID}}" name="{{.Name}}" type="password" value="" autocomplete="off" placeholder="Unchanged">{{else if eq .Kind "number"}}<input id="{{.ID}}" name="{{.Name}}" type="number" value="{{.Value}}">{{else}}<input id="{{.ID}}" name="{{.Name}}" type="text" value="{{.Value}}">{{end}}{{if .Help}}<p class="panel__help">{{.Help}}</p>{{end}}</div>
+{{end}}{{end}}
````

- [ ] **Step 4: Run the tests to verify they pass**

Run: `gofmt -l internal cmd; go vet ./... && go test -race -count=1 -timeout 180s ./...`
Expected: `gofmt -l` prints nothing; every package PASSES.

- [ ] **Step 5: Commit**

```bash
git add internal/web internal/web/static internal/web/templates
git commit -m "Show the chosen source's settings on the Sources page"
```

---

### Task 9: Choosing which chats are archived

"Choose which of the 112 chats are archived" opens a page listing every chat of the source by kind, each with a checkbox. Saving switches the shown chats and restarts the connector, as spec section 3 asks.

**Files:**
- Create: `internal/web/archived.go`, `internal/web/templates/archived.html`
- Modify: `internal/web/server.go`, `internal/web/static/app.css`
- Test: `internal/web/archived_test.go` (new)

**Interfaces:**
- Consumes: `AccountChats`, `SetChatsArchived` (Task 3); `account`, `live` (Task 8); `Runner.Restart`.
- Produces: `type ChatToggle`, `ChatGroup`, `archivedPage`; routes `GET /sources/{id}/chats`, `POST /sources/{id}/chats` (fields `shown` and `archived`, chat IDs); template `archived`.

- [ ] **Step 1: Write the failing tests**

`internal/web/archived_test.go`:

````go
package web

import (
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"testing"

	"github.com/fenelon/arc5/internal/model"
)

func TestChooseArchivedChats(t *testing.T) {
	s := newSite(t, "")
	s.signIn()
	acc := s.fakeAccount("Personal")
	other := s.fakeAccount("Work")
	s.say(acc, "Dan", "1", "hello", noon)
	for _, c := range []model.Chat{{SourceID: "news", Kind: model.ChatChannel, Title: "Rail news"}, {SourceID: "g1", Kind: model.ChatGroup, Title: "Hikers"}} {
		if err := s.store.UpsertChat(acc, c); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.store.UpsertChat(other, model.Chat{SourceID: "x", Kind: model.ChatPrivate, Title: "Elsewhere"}); err != nil {
		t.Fatal(err)
	}
	chats, _ := s.store.AccountChats(acc)
	id := map[string]int64{}
	for _, c := range chats {
		id[c.SourceID] = c.ID
	}
	elsewhere, _ := s.store.AccountChats(other)
	path := fmt.Sprintf("/sources/%d/chats", acc)

	body := s.page(path)
	has(t, body, "<h1>Chats of Personal</h1>", fmt.Sprintf(`href="/sources/%d">Back to Personal`, acc),
		"Private chats", "Groups", "Channels",
		fmt.Sprintf(`name="archived" value="%d" checked`, id["Dan"]),
		fmt.Sprintf(`name="archived" value="%d" checked`, id["g1"]),
		fmt.Sprintf(`name="archived" value="%d">`, id["news"]),
		"1 message, the last on 28 Sep", "Nothing archived yet")

	form := url.Values{"archived": {strconv.FormatInt(id["news"], 10)}}
	for _, c := range append(chats, elsewhere...) {
		form.Add("shown", strconv.FormatInt(c.ID, 10))
	}
	rec := s.post(path, form)
	if !redirectsTo(rec, path+"?saved=1") {
		t.Fatalf("save: %d to %q", rec.Code, rec.Header().Get("Location"))
	}
	for source, want := range map[string]bool{"Dan": false, "g1": false, "news": true} {
		if got, _ := s.store.ChatArchived(acc, source); got != want {
			t.Errorf("chat %s archived = %v, want %v", source, got, want)
		}
	}
	if got, _ := s.store.ChatArchived(other, "x"); !got {
		t.Error("a chat of another source was switched off")
	}
	if !slices.Contains(s.runner.restarted, acc) {
		t.Error("the connector was not restarted")
	}
	has(t, s.page(path+"?saved=1"), "Saved.")

	if rec := s.post(path, url.Values{"shown": {"banana"}}); rec.Code != http.StatusBadRequest {
		t.Errorf("a made-up chat ID: %d", rec.Code)
	}
	if rec := s.get("/sources/999/chats"); rec.Code != http.StatusNotFound {
		t.Errorf("unknown source: %d", rec.Code)
	}
	s.post(fmt.Sprintf("/sources/%d/remove", acc), nil)
	if rec := s.post(path, form); rec.Code != http.StatusConflict {
		t.Errorf("changing a removed source's chats: %d", rec.Code)
	}
}
````

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go vet ./internal/web/; go test -count=1 ./internal/web/`
Expected: FAIL: `TestChooseArchivedChats`: `GET /sources/1/chats: status 404`.

- [ ] **Step 3: Write the implementation**

`internal/web/archived.go`:

````go
package web

import (
	"fmt"
	"net/http"
	"strconv"

	"github.com/fenelon/arc5/internal/archive"
	"github.com/fenelon/arc5/internal/model"
)

// ChatToggle is one chat with its archive switch.
type ChatToggle struct {
	ID       int64
	Title    string
	Detail   string
	Archived bool
}

// ChatGroup is the chats of one kind.
type ChatGroup struct {
	Title string
	Chats []ChatToggle
}

type archivedPage struct {
	Page
	AccountID int64
	Name      string
	Removed   bool
	Saved     bool
	Groups    []ChatGroup
}

var chatKindTitles = []struct {
	kind  model.ChatKind
	title string
}{
	{model.ChatPrivate, "Private chats"}, {model.ChatGroup, "Groups"}, {model.ChatChannel, "Channels"}, {model.ChatBot, "Bots"},
}

func (s *Server) archivedChats(w http.ResponseWriter, r *http.Request, sess archive.Session) {
	acc, ok := s.account(w, r)
	if !ok {
		return
	}
	all, err := s.sources()
	if err != nil {
		s.fail(w, err)
		return
	}
	chats, err := s.deps.Store.AccountChats(acc.ID)
	if err != nil {
		s.fail(w, err)
		return
	}
	data := archivedPage{
		Page: s.chrome(r, sess, "Chats of "+acc.Label, all, ""), AccountID: acc.ID, Name: acc.Label,
		Removed: !acc.RemovedAt.IsZero(), Saved: r.URL.Query().Get("saved") == "1",
	}
	for _, k := range chatKindTitles {
		group := ChatGroup{Title: k.title}
		for _, c := range chats {
			if c.Kind != k.kind {
				continue
			}
			detail := "Nothing archived yet"
			if c.Messages > 0 {
				detail = fmt.Sprintf("%s, the last on %s", plural(c.Messages, "message", "messages"), s.when(c.LastAt))
			}
			group.Chats = append(group.Chats, ChatToggle{ID: c.ID, Title: c.Title, Detail: detail, Archived: c.Archived})
		}
		if len(group.Chats) > 0 {
			data.Groups = append(data.Groups, group)
		}
	}
	s.render(w, http.StatusOK, "archived", data)
}

// saveArchivedChats switches archiving for the chats the form showed: ticked
// ones on, the others off. The connector restarts to pick up the change.
func (s *Server) saveArchivedChats(w http.ResponseWriter, r *http.Request, _ archive.Session) {
	acc, ok := s.live(w, r)
	if !ok {
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "The form could not be read.", http.StatusBadRequest)
		return
	}
	changes := map[int64]bool{}
	for _, name := range []string{"shown", "archived"} {
		for _, v := range r.PostForm[name] {
			id, err := strconv.ParseInt(v, 10, 64)
			if err != nil {
				http.Error(w, "The form named a chat that does not exist.", http.StatusBadRequest)
				return
			}
			changes[id] = name == "archived"
		}
	}
	if err := s.deps.Store.SetChatsArchived(acc.ID, changes); err != nil {
		s.fail(w, err)
		return
	}
	if err := s.deps.Runner.Restart(acc.ID); err != nil {
		s.fail(w, err)
		return
	}
	redirect(w, r, fmt.Sprintf("/sources/%d/chats?saved=1", acc.ID))
}
````

Change `internal/web/server.go`:

````diff
--- a/internal/web/server.go
+++ b/internal/web/server.go
@@ -117,6 +117,8 @@ func (s *Server) routes() {
 	s.mux.Handle("POST /sources/{id}/settings", s.signedIn(s.saveSourceSettings))
 	s.mux.Handle("POST /sources/{id}/signout", s.signedIn(s.signOutSource))
 	s.mux.Handle("POST /sources/{id}/remove", s.signedIn(s.removeSource))
+	s.mux.Handle("GET /sources/{id}/chats", s.signedIn(s.archivedChats))
+	s.mux.Handle("POST /sources/{id}/chats", s.signedIn(s.saveArchivedChats))
 	s.mux.Handle("GET /settings", s.signedIn(s.settings))
 	s.mux.Handle("POST /settings/appearance", s.signedIn(s.saveAppearance))
 	s.mux.Handle("POST /settings/languages", s.signedIn(s.saveLanguages))
````

Change `internal/web/static/app.css`:

````diff
--- a/internal/web/static/app.css
+++ b/internal/web/static/app.css
@@ -270,6 +270,13 @@ mark.hit{padding:0 2px;color:var(--hit-ink);background:var(--hit);border-radius:
 .confirm__body{display:flex;flex-direction:column;gap:10px;margin-top:10px;max-width:520px}
 .button--warn{color:var(--warn-bg);background:var(--warn-ink)}
 
+.toggles{margin:10px 0 0;padding:0;list-style:none}
+.toggles li{border-top:1px solid var(--line-soft)}
+.toggle{display:flex;width:100%;padding:8px 0}
+.toggle__text{display:flex;flex-direction:column;min-width:0}
+.toggle__title{overflow-wrap:anywhere}
+.toggle__detail{font-size:13px;font-weight:350;color:var(--muted)}
+
 /* Settings. */
 .settings-link{display:inline-flex;align-items:center;gap:6px;min-height:44px;padding:0 4px;font-weight:450;color:var(--ink);text-decoration:none}
 .settings-link svg{width:20px;height:20px}
````

`internal/web/templates/archived.html`:

````html
{{define "archived"}}{{template "head" .}}
<body class="app">
{{template "topbar" .}}
{{template "banner" .}}
<main class="plain plain--settings">
<p><a class="panel__link" href="/sources/{{.AccountID}}">Back to {{.Name}}</a></p>
<h1>Chats of {{.Name}}</h1>
<p>Ticked chats are archived. A chat switched off keeps what was archived before; nothing new is saved from it.</p>
{{if .Saved}}<p class="saved" role="status">Saved. The source starts again with this choice.</p>{{end}}
{{if .Removed}}<p class="panel__help">This source was removed, so nothing is archived from it any more.</p>{{end}}
<form method="post" action="/sources/{{.AccountID}}/chats">
<input type="hidden" name="csrf" value="{{.CSRF}}">
{{range .Groups}}<section class="card"><h2 class="card__title">{{.Title}}</h2>
<ul class="toggles">{{range .Chats}}<li><input type="hidden" name="shown" value="{{.ID}}"><label class="choice toggle"><input type="checkbox" name="archived" value="{{.ID}}"{{if .Archived}} checked{{end}}{{if $.Removed}} disabled{{end}}> <span class="toggle__text"><span class="toggle__title">{{.Title}}</span><span class="toggle__detail">{{.Detail}}</span></span></label></li>
{{end}}</ul></section>
{{else}}<p class="empty">This source has no chats yet.</p>
{{end}}{{if and .Groups (not .Removed)}}<div class="page-links"><button class="button" type="submit">Save</button></div>{{end}}
</form>
</main>
</body>
</html>{{end}}
````

- [ ] **Step 4: Run the tests to verify they pass**

Run: `gofmt -l internal cmd; go vet ./... && go test -race -count=1 -timeout 180s ./...`
Expected: `gofmt -l` prints nothing; every package PASSES.

- [ ] **Step 5: Commit**

```bash
git add internal/web internal/web/static internal/web/templates
git commit -m "Choose which chats of a source are archived"
```

---

### Task 10: Adding a source and logging in

"Add a source" offers every connector that is not hidden, creates the account switched off, and runs the connector's setup flow one step at a time; the account is switched on and started when the flow finishes. "Log in again" runs the same flow for a source that needs it, from the panel and from the banner. Flows live in memory, one per account, and are closed when finished, cancelled or left for 30 minutes.

**Files:**
- Create: `internal/web/setup.go`, `internal/web/templates/sources_setup.html`
- Modify: `internal/web/page.go`, `internal/web/server.go`, `internal/web/sources.go`, `internal/web/sources_page.go`, `internal/web/static/app.css`, `internal/web/templates/partials.html`, `internal/web/templates/sources.html`
- Test: `internal/web/fake_test.go`, `internal/web/setup_test.go` (new)

**Interfaces:**
- Consumes: `Registry.Infos`, `Info.Hidden`, `NewState`, `SetupFlow`, `io.Closer` (Task 4); `RenameAccount` (Task 3); `FieldView`, `setting-field`, `live`, `account`, `newConnector` (Task 8).
- Produces:
  - `type flow`, `flows` (`get`, `put`, `drop`), `Server.flows`; `flowLifetime = 30 * time.Minute`
  - routes `GET /sources/new`, `POST /sources/new`, `GET /sources/{id}/login`, `POST /sources/{id}/login`, `POST /sources/{id}/login/cancel`
  - templates `add`, `login-step` (in `sources_setup.html`); `SourcePanel.LoginHref`, `LoginLabel`
  - `Alert.ActionHref`, `Alert.ActionLabel`: the banner's button is "Log in again" for a source that needs login
  - a source that needs login says since when, and offers no sign-out

- [ ] **Step 1: Write the failing tests**

Change `internal/web/fake_test.go`:

````diff
--- a/internal/web/fake_test.go
+++ b/internal/web/fake_test.go
@@ -3,6 +3,7 @@ package web
 import (
 	"context"
 	"errors"
+	"sync/atomic"
 
 	"github.com/fenelon/arc5/internal/connector"
 )
@@ -38,6 +39,14 @@ type fakeFlow struct {
 	phone string
 }
 
+// fakeFlowsClosed counts fakeFlows closed, as a flow holding a connection would be.
+var fakeFlowsClosed atomic.Int64
+
+func (f *fakeFlow) Close() error {
+	fakeFlowsClosed.Add(1)
+	return nil
+}
+
 func (f *fakeFlow) Next(_ context.Context, input map[string]string) (connector.Step, error) {
 	phone := connector.Step{Title: "Your phone number", Help: "With the country code.",
 		Fields: []connector.Field{{Name: "phone", Label: "Phone number", Kind: connector.FieldText}}}
````

`internal/web/setup_test.go`:

````go
package web

import (
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/fenelon/arc5/internal/connector"
)

func TestAddASource(t *testing.T) {
	s := newSite(t, "")
	s.signIn()
	s.reg.Register(connector.Info{Name: "fake", Title: "Fake", Display: connector.DisplayChats}, func() connector.Connector { return fakeConnector{} })
	s.reg.Register(connector.Info{Name: "hidden", Title: "Hidden", Display: connector.DisplayChats, Hidden: true}, func() connector.Connector { return fakeConnector{} })

	has(t, s.page("/sources"), `href="/sources/new"`, "Add a source")
	body := s.page("/sources/new")
	has(t, body, "<h1>Add a source</h1>", `name="connector" value="fake"`, "Fake")
	lacks(t, body, "Hidden")

	for name, f := range map[string]url.Values{
		"a hidden kind":  {"connector": {"hidden"}, "name": {"Demo"}},
		"an unknown one": {"connector": {"nope"}, "name": {"X"}},
		"no name":        {"connector": {"fake"}, "name": {"  "}},
	} {
		rec := s.post("/sources/new", f)
		if rec.Code != http.StatusUnprocessableEntity || !strings.Contains(rec.Body.String(), `role="alert"`) {
			t.Errorf("%s: %d", name, rec.Code)
		}
	}
	if accounts, _ := s.store.Accounts(); len(accounts) != 0 {
		t.Fatalf("refused forms created %d accounts", len(accounts))
	}

	rec := s.post("/sources/new", url.Values{"connector": {"fake"}, "name": {"Phone"}})
	accounts, _ := s.store.Accounts()
	if len(accounts) != 1 {
		t.Fatalf("%d accounts after adding one", len(accounts))
	}
	acc := accounts[0]
	login := fmt.Sprintf("/sources/%d/login", acc.ID)
	if !redirectsTo(rec, login) || acc.Enabled || acc.Label != "Phone" || acc.Connector != "fake" {
		t.Fatalf("add: %d to %q, account %+v", rec.Code, rec.Header().Get("Location"), acc)
	}

	has(t, s.page(login), "<title>Log in to Phone - arc5</title>", "Your phone number", "With the country code.", `name="phone" type="text"`)
	if rec := s.post(login, url.Values{"phone": {""}}); rec.Code != http.StatusUnprocessableEntity ||
		!strings.Contains(rec.Body.String(), "Type your phone number.") || !strings.Contains(rec.Body.String(), `name="phone"`) {
		t.Fatalf("an empty phone: %d", rec.Code)
	}
	if rec := s.post(login, url.Values{"phone": {"020 7946 0000"}}); !redirectsTo(rec, login) {
		t.Fatalf("phone: %d to %q", rec.Code, rec.Header().Get("Location"))
	}
	has(t, s.page(login), "The code", "Sent to 020 7946 0000.", `name="password" type="password"`)
	wrong := s.post(login, url.Values{"code": {"000"}, "password": {"hunter22"}})
	if wrong.Code != http.StatusUnprocessableEntity || !strings.Contains(wrong.Body.String(), "That code is not right.") {
		t.Fatalf("a wrong code: %d", wrong.Code)
	}
	if strings.Contains(wrong.Body.String(), "hunter22") {
		t.Error("the page echoes a secret field")
	}
	closed := fakeFlowsClosed.Load()
	if rec := s.post(login, url.Values{"code": {"12345"}}); !redirectsTo(rec, fmt.Sprintf("/sources/%d?saved=login", acc.ID)) {
		t.Fatalf("right code: %d to %q", rec.Code, rec.Header().Get("Location"))
	}
	if got, _ := s.store.Account(acc.ID); !got.Enabled {
		t.Error("the source is still switched off after logging in")
	}
	if v, _ := s.store.StateGet(acc.ID, "session"); string(v) != "signed in as 020 7946 0000" {
		t.Errorf("session = %q", v)
	}
	if !slices.Contains(s.runner.restarted, acc.ID) {
		t.Error("the connector was not started")
	}
	if fakeFlowsClosed.Load() != closed+1 {
		t.Error("the finished flow was not closed")
	}
	has(t, s.page(fmt.Sprintf("/sources/%d?saved=login", acc.ID)), "Logged in.")
}

func TestLogInAgain(t *testing.T) {
	s := newSite(t, "")
	s.signIn()
	acc := s.fakeAccount("Personal")
	s.say(acc, "Dan", "1", "hello", noon)
	s.srv.deps.Now = func() time.Time { return noon.Add(time.Hour) }
	s.runner.status = []connector.Status{{AccountID: acc, State: connector.StateNeedsLogin, Since: noon, LastError: "account needs login"}}
	login := fmt.Sprintf("/sources/%d/login", acc)
	for _, path := range []string{fmt.Sprintf("/s/%d", acc), fmt.Sprintf("/sources/%d", acc)} {
		has(t, s.page(path), fmt.Sprintf(`href="%s">Log in again`, login), "Needs login since 12:00")
	}
	lacks(t, s.page(fmt.Sprintf("/sources/%d", acc)), "account needs login", "Sign this source out")
	s.post(login, nil) // the first step
	s.post(login, url.Values{"phone": {"+1 555"}})
	if rec := s.post(login, url.Values{"code": {"12345"}}); !redirectsTo(rec, fmt.Sprintf("/sources/%d?saved=login", acc)) {
		t.Fatalf("log in again: %d", rec.Code)
	}
}

func TestSetupCanBeCancelledAndExpires(t *testing.T) {
	s := newSite(t, "")
	s.signIn()
	acc := s.fakeAccount("Personal")
	login := fmt.Sprintf("/sources/%d/login", acc)
	s.page(login)
	s.post(login, url.Values{"phone": {"+1 555"}})
	has(t, s.page(login), "The code")

	closed := fakeFlowsClosed.Load()
	if rec := s.post(login+"/cancel", nil); !redirectsTo(rec, fmt.Sprintf("/sources/%d", acc)) {
		t.Fatalf("cancel: %d", rec.Code)
	}
	if fakeFlowsClosed.Load() != closed+1 {
		t.Error("the cancelled flow was not closed")
	}
	has(t, s.page(login), "Your phone number")

	s.post(login, url.Values{"phone": {"+1 555"}})
	start := time.Now()
	s.srv.deps.Now = func() time.Time { return start.Add(31 * time.Minute) }
	has(t, s.page(login), "Your phone number")

	if rec := s.get("/sources/999/login"); rec.Code != http.StatusNotFound {
		t.Errorf("unknown source: %d", rec.Code)
	}
	plain := s.account("Old", "telegram") // a connector with no way to log in
	if rec := s.get(fmt.Sprintf("/sources/%d/login", plain)); rec.Code != http.StatusNotFound {
		t.Errorf("a source that cannot log in: %d", rec.Code)
	}
}
````

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go vet ./internal/web/; go test -count=1 ./internal/web/`
Expected: FAIL: `TestAddASource` lacks "Add a source"; `TestLogInAgain` lacks "Log in again" and "Needs login since 12:00"; `TestSetupCanBeCancelledAndExpires` fails.

- [ ] **Step 3: Write the implementation**

Change `internal/web/page.go`:

````diff
--- a/internal/web/page.go
+++ b/internal/web/page.go
@@ -38,6 +38,8 @@ type Picker struct {
 
 // Alert names a source that is not archiving.
 type Alert struct {
-	Name   string
-	Detail string
+	Name        string
+	Detail      string
+	ActionHref  string // "Log in again" for a source that needs it, else the Sources page
+	ActionLabel string
 }
````

Change `internal/web/server.go`:

````diff
--- a/internal/web/server.go
+++ b/internal/web/server.go
@@ -65,6 +65,7 @@ type Server struct {
 	static  fs.FS
 	version string
 	mux     *http.ServeMux
+	flows   flows // logins in progress on the Sources page
 }
 
 func New(deps Deps) (*Server, error) {
@@ -112,7 +113,12 @@ func (s *Server) routes() {
 	s.mux.Handle("GET /s/{source}/c/{chat}", s.signedIn(s.conversationView))
 	s.mux.Handle("GET /s/{source}/results", s.signedIn(s.resultsFragment))
 	s.mux.Handle("GET /sources", s.signedIn(s.sourcesList))
+	s.mux.Handle("GET /sources/new", s.signedIn(s.newSourceForm))
+	s.mux.Handle("POST /sources/new", s.signedIn(s.addSource))
 	s.mux.Handle("GET /sources/{id}", s.signedIn(s.sourceSettings))
+	s.mux.Handle("GET /sources/{id}/login", s.signedIn(s.loginStep))
+	s.mux.Handle("POST /sources/{id}/login", s.signedIn(s.submitStep))
+	s.mux.Handle("POST /sources/{id}/login/cancel", s.signedIn(s.cancelLogin))
 	s.mux.Handle("POST /sources/{id}/name", s.signedIn(s.renameSource))
 	s.mux.Handle("POST /sources/{id}/settings", s.signedIn(s.saveSourceSettings))
 	s.mux.Handle("POST /sources/{id}/signout", s.signedIn(s.signOutSource))
````

`internal/web/setup.go`:

````go
package web

import (
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/fenelon/arc5/internal/archive"
	"github.com/fenelon/arc5/internal/connector"
)

// flowLifetime is how long an unfinished login waits for its next step.
const flowLifetime = 30 * time.Minute

// flow is a connector's setup flow in progress for one account. Its mutex
// keeps two submissions of the same step from running at once.
type flow struct {
	mu      sync.Mutex
	setup   connector.SetupFlow
	step    connector.Step
	expires time.Time
}

// flows holds the setup flows in progress, one per account.
type flows struct {
	mu     sync.Mutex
	byAcct map[int64]*flow
}

// end closes a flow if it holds anything open.
func end(f *flow) {
	if c, ok := f.setup.(io.Closer); ok {
		c.Close()
	}
}

// get returns the account's flow, unless it has expired. Expired flows of
// any account are closed on the way.
func (fs *flows) get(accountID int64, now time.Time) *flow {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	for id, f := range fs.byAcct {
		if now.After(f.expires) {
			delete(fs.byAcct, id)
			end(f)
		}
	}
	return fs.byAcct[accountID]
}

func (fs *flows) put(accountID int64, f *flow) {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	if fs.byAcct == nil {
		fs.byAcct = map[int64]*flow{}
	}
	if old := fs.byAcct[accountID]; old != nil && old != f {
		end(old)
	}
	fs.byAcct[accountID] = f
}

// drop ends the account's flow, if any.
func (fs *flows) drop(accountID int64) {
	fs.mu.Lock()
	f := fs.byAcct[accountID]
	delete(fs.byAcct, accountID)
	fs.mu.Unlock()
	if f != nil {
		end(f)
	}
}

// ConnectorChoice is a kind of source the owner can add.
type ConnectorChoice struct {
	Name, Title, Logo string
	Checked           bool
}

type addPage struct {
	Page
	Choices []ConnectorChoice
	Name    string
	Problem string
}

func (s *Server) addable() []ConnectorChoice {
	var out []ConnectorChoice
	for _, info := range s.deps.Registry.Infos() {
		if !info.Hidden {
			out = append(out, ConnectorChoice{Name: info.Name, Title: info.Title, Logo: logoFor(info.Name)})
		}
	}
	return out
}

func (s *Server) addForm(w http.ResponseWriter, r *http.Request, sess archive.Session, status int, chosen, name, problem string) {
	all, err := s.sources()
	if err != nil {
		s.fail(w, err)
		return
	}
	data := addPage{Page: s.chrome(r, sess, "Add a source", all, ""), Choices: s.addable(), Name: name, Problem: problem}
	for i := range data.Choices {
		data.Choices[i].Checked = data.Choices[i].Name == chosen || (chosen == "" && i == 0)
	}
	s.render(w, status, "add", data)
}

func (s *Server) newSourceForm(w http.ResponseWriter, r *http.Request, sess archive.Session) {
	s.addForm(w, r, sess, http.StatusOK, "", "", "")
}

// addSource creates the account switched off, then runs its connector's
// login. The account is switched on when the login finishes.
func (s *Server) addSource(w http.ResponseWriter, r *http.Request, sess archive.Session) {
	name, chosen := strings.TrimSpace(r.PostFormValue("name")), r.PostFormValue("connector")
	info, known := s.deps.Registry.Info(chosen)
	switch {
	case !known || info.Hidden:
		s.addForm(w, r, sess, http.StatusUnprocessableEntity, "", name, "Choose one of the kinds of source listed.")
		return
	case name == "":
		s.addForm(w, r, sess, http.StatusUnprocessableEntity, chosen, name, "Give the source a name, such as Personal or Work.")
		return
	}
	id, err := s.deps.Store.CreateAccount(chosen, name, nil)
	if err == nil {
		err = s.deps.Store.SetAccountEnabled(id, false)
	}
	if err == nil {
		err = s.deps.Store.RenameAccount(id, name) // tidies the name as a rename would
	}
	if err != nil {
		s.fail(w, err)
		return
	}
	redirect(w, r, fmt.Sprintf("/sources/%d/login", id))
}

type loginPage struct {
	Page
	AccountID int64
	Name      string
	StepTitle string // the step's own title; Page.Title names the page
	Help      string
	Fields    []FieldView
	Problem   string
}

// startFlow begins an account's login, or returns false and answers the
// request itself when its connector has none.
func (s *Server) startFlow(w http.ResponseWriter, r *http.Request, acc archive.Account) (*flow, bool) {
	c, ok := s.newConnector(acc.Connector)
	if !ok {
		http.NotFound(w, r)
		return nil, false
	}
	setup := c.Setup(connector.NewState(s.deps.Store, acc.ID))
	if setup == nil {
		http.NotFound(w, r)
		return nil, false
	}
	f := &flow{setup: setup, expires: s.deps.Now().Add(flowLifetime)}
	step, err := setup.Next(r.Context(), nil)
	if err != nil {
		if c, ok := setup.(io.Closer); ok {
			c.Close()
		}
		s.fail(w, fmt.Errorf("starting the login of account %d: %w", acc.ID, err))
		return nil, false
	}
	f.step = step
	s.flows.put(acc.ID, f)
	return f, true
}

func (s *Server) renderLogin(w http.ResponseWriter, r *http.Request, sess archive.Session, acc archive.Account, step connector.Step, status int, problem string) {
	all, err := s.sources()
	if err != nil {
		s.fail(w, err)
		return
	}
	data := loginPage{Page: s.chrome(r, sess, "Log in to "+acc.Label, all, ""), AccountID: acc.ID, Name: acc.Label,
		StepTitle: step.Title, Help: step.Help, Problem: problem}
	for _, f := range step.Fields {
		v := FieldView{ID: "step-" + f.Name, Name: f.Name, Label: f.Label, Help: f.Help, Kind: string(f.Kind), Value: f.Default}
		if f.Kind == connector.FieldSecret {
			v.Value = ""
		}
		for _, choice := range f.Choices {
			v.Choices = append(v.Choices, Choice{Value: choice, Selected: choice == f.Default})
		}
		data.Fields = append(data.Fields, v)
	}
	s.render(w, status, "login-step", data)
}

func (s *Server) loginStep(w http.ResponseWriter, r *http.Request, sess archive.Session) {
	acc, ok := s.live(w, r)
	if !ok {
		return
	}
	f := s.flows.get(acc.ID, s.deps.Now())
	if f == nil {
		if f, ok = s.startFlow(w, r, acc); !ok {
			return
		}
	}
	f.mu.Lock()
	step := f.step
	f.mu.Unlock()
	s.renderLogin(w, r, sess, acc, step, http.StatusOK, "")
}

// submitStep sends a step's answers to the connector. A refused answer shows
// the same step again with the connector's explanation.
func (s *Server) submitStep(w http.ResponseWriter, r *http.Request, sess archive.Session) {
	acc, ok := s.live(w, r)
	if !ok {
		return
	}
	login := fmt.Sprintf("/sources/%d/login", acc.ID)
	f := s.flows.get(acc.ID, s.deps.Now())
	if f == nil {
		if _, ok := s.startFlow(w, r, acc); ok {
			redirect(w, r, login) // nothing was asked yet; show the first step
		}
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	input := map[string]string{}
	for _, field := range f.step.Fields {
		input[field.Name] = r.PostFormValue(field.Name)
	}
	next, err := f.setup.Next(r.Context(), input)
	if err != nil {
		s.renderLogin(w, r, sess, acc, f.step, http.StatusUnprocessableEntity, err.Error())
		return
	}
	if !next.Done {
		f.step, f.expires = next, s.deps.Now().Add(flowLifetime)
		redirect(w, r, login)
		return
	}
	s.flows.drop(acc.ID)
	if err := s.deps.Store.SetAccountEnabled(acc.ID, true); err != nil {
		s.fail(w, err)
		return
	}
	if err := s.deps.Runner.Restart(acc.ID); err != nil {
		s.fail(w, err)
		return
	}
	redirect(w, r, fmt.Sprintf("/sources/%d?saved=login", acc.ID))
}

func (s *Server) cancelLogin(w http.ResponseWriter, r *http.Request, _ archive.Session) {
	acc, ok := s.account(w, r)
	if !ok {
		return
	}
	s.flows.drop(acc.ID)
	redirect(w, r, fmt.Sprintf("/sources/%d", acc.ID))
}
````

Change `internal/web/sources.go`:

````diff
--- a/internal/web/sources.go
+++ b/internal/web/sources.go
@@ -4,6 +4,7 @@ import (
 	"fmt"
 	"net/http"
 	"strconv"
+	"time"
 
 	"github.com/fenelon/arc5/internal/archive"
 	"github.com/fenelon/arc5/internal/connector"
@@ -45,14 +46,15 @@ func (s *Server) sources() ([]source, error) {
 			src.Info = connector.Info{Name: acc.Connector, Title: acc.Connector, Display: connector.DisplayChats}
 		}
 		st, running := status[acc.ID]
-		describe(&src, st, running)
+		describe(&src, st, running, s.when)
 		out = append(out, src)
 	}
 	return out, nil
 }
 
 // describe turns the runner's status into a state mark and a sentence.
-func describe(src *source, st connector.Status, running bool) {
+// when formats the moment a state began, for "Needs login since 12:00".
+func describe(src *source, st connector.Status, running bool, when func(time.Time) string) {
 	set := func(state, label string) { src.State, src.StateLabel, src.Detail = state, label, label }
 	switch {
 	case !src.Account.RemovedAt.IsZero():
@@ -64,7 +66,11 @@ func describe(src *source, st connector.Status, running bool) {
 		set(stateOffline, "Not running")
 	case st.State == connector.StateNeedsLogin:
 		set(stateNeedsLogin, "Needs login")
+		if !st.Since.IsZero() {
+			src.Detail = "Needs login since " + when(st.Since)
+		}
 		src.Failing = true
+		return // the error only repeats that it needs login
 	case st.State == connector.StatePaused:
 		set(stateOffline, "Paused, the archive could not be written")
 		src.Failing = true
@@ -189,7 +195,11 @@ func (s *Server) chrome(r *http.Request, sess archive.Session, title string, all
 	p.Picker = picker
 	for _, src := range all {
 		if src.Failing {
-			p.Alert = &Alert{Name: src.Account.Label, Detail: src.Detail + ". New messages are not being saved, and ones that auto-delete cannot be recovered."}
+			p.Alert = &Alert{Name: src.Account.Label, Detail: src.Detail + ". New messages are not being saved, and ones that auto-delete cannot be recovered.",
+				ActionHref: "/sources/" + src.key(), ActionLabel: "Open sources"}
+			if src.State == stateNeedsLogin && src.Known {
+				p.Alert.ActionHref, p.Alert.ActionLabel = "/sources/"+src.key()+"/login", "Log in again"
+			}
 			break
 		}
 	}
````

Change `internal/web/sources_page.go`:

````diff
--- a/internal/web/sources_page.go
+++ b/internal/web/sources_page.go
@@ -45,6 +45,8 @@ type SourcePanel struct {
 	Progress            *ProgressView
 	Fields              []FieldView
 	CanSignOut          bool
+	LoginHref           string // "Log in again", when the source needs it
+	LoginLabel          string
 }
 
 type sourcesPage struct {
@@ -183,7 +185,14 @@ func (s *Server) panel(src source) (*SourcePanel, error) {
 	if !ok {
 		return p, nil
 	}
+	switch {
+	case src.State == stateNeedsLogin:
+		p.LoginHref, p.LoginLabel = fmt.Sprintf("/sources/%d/login", acc.ID), "Log in again"
+	case !acc.Enabled:
+		p.LoginHref, p.LoginLabel = fmt.Sprintf("/sources/%d/login", acc.ID), "Log in"
+	}
 	_, p.CanSignOut = c.(connector.SignOuter)
+	p.CanSignOut = p.CanSignOut && acc.Enabled && src.State != stateNeedsLogin
 	for _, f := range c.Settings() {
 		value, set := acc.Settings[f.Name]
 		if !set {
````

Change `internal/web/static/app.css`:

````diff
--- a/internal/web/static/app.css
+++ b/internal/web/static/app.css
@@ -268,7 +268,6 @@ mark.hit{padding:0 2px;color:var(--hit-ink);background:var(--hit);border-radius:
 .confirm summary{display:flex;align-items:center;min-height:44px;cursor:pointer}
 .confirm summary::-webkit-details-marker{display:none}
 .confirm__body{display:flex;flex-direction:column;gap:10px;margin-top:10px;max-width:520px}
-.button--warn{color:var(--warn-bg);background:var(--warn-ink)}
 
 .toggles{margin:10px 0 0;padding:0;list-style:none}
 .toggles li{border-top:1px solid var(--line-soft)}
@@ -277,6 +276,14 @@ mark.hit{padding:0 2px;color:var(--hit-ink);background:var(--hit);border-radius:
 .toggle__title{overflow-wrap:anywhere}
 .toggle__detail{font-size:13px;font-weight:350;color:var(--muted)}
 
+.button__icon{width:16px;height:16px}
+.kinds{display:grid;grid-template-columns:repeat(auto-fill,minmax(200px,1fr));gap:8px;margin:0 0 16px;padding:0;border:0}
+.kinds legend{margin-bottom:8px}
+.kind{display:flex;align-items:center;gap:10px;padding:10px 12px;background:var(--raised);border:1px solid var(--line);border-radius:10px;cursor:pointer}
+.kind:has(input:checked){border:2px solid var(--accent);padding:9px 11px}
+.kind input{accent-color:var(--accent)}
+.step-fields{margin:14px 0}
+
 /* Settings. */
 .settings-link{display:inline-flex;align-items:center;gap:6px;min-height:44px;padding:0 4px;font-weight:450;color:var(--ink);text-decoration:none}
 .settings-link svg{width:20px;height:20px}
````

Change `internal/web/templates/partials.html`:

````diff
--- a/internal/web/templates/partials.html
+++ b/internal/web/templates/partials.html
@@ -35,6 +35,6 @@
 <a class="settings-link" href="/settings"><svg class="only-phone" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M4 7h10M18 7h2M4 17h4M12 17h8"/><circle cx="16" cy="7" r="2"/><circle cx="10" cy="17" r="2"/></svg><span class="settings-link__label">Settings</span></a>
 </header>{{end}}
 
-{{define "banner"}}{{if .Alert}}<div class="banner" role="alert"><span><strong>{{.Alert.Name}} is not archiving.</strong> {{.Alert.Detail}}</span><a class="button button--warn" href="/sources">Open sources</a></div>{{end}}{{end}}
+{{define "banner"}}{{if .Alert}}<div class="banner" role="alert"><span><strong>{{.Alert.Name}} is not archiving.</strong> {{.Alert.Detail}}</span><a class="button button--warn" href="{{.Alert.ActionHref}}">{{.Alert.ActionLabel}}</a></div>{{end}}{{end}}
 
 {{define "search"}}<form class="search" role="search" method="get" action="{{.Action}}" data-controller="search" data-search-results-value="{{.Results}}" data-action="submit->search#submit"><svg class="search__icon" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><circle cx="11" cy="11" r="7"/><path d="M20 20l-3.5-3.5"/></svg><input class="search__input" type="search" name="q" value="{{.Query}}" placeholder="Search messages, chats and people" aria-label="Search messages, chats and people" autocomplete="off" data-search-target="input" data-action="input->search#type keydown.esc->search#clear"></form>{{end}}
````

Change `internal/web/templates/sources.html`:

````diff
--- a/internal/web/templates/sources.html
+++ b/internal/web/templates/sources.html
@@ -3,7 +3,7 @@
 {{template "topbar" .}}
 {{template "banner" .}}
 <main class="plain plain--wide">
-<div class="page-head"><h1>Sources</h1></div>
+<div class="page-head"><h1>Sources</h1><a class="button" href="/sources/new"><svg class="button__icon" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.4" stroke-linecap="round" aria-hidden="true"><path d="M12 5v14M5 12h14"/></svg>Add a source</a></div>
 {{if eq .Saved "removed"}}<p class="saved" role="status">Removed. Its messages stay in the archive.</p>{{end}}
 {{range .Groups}}<h2 class="group-heading">{{.Title}}</h2>
 <div class="tiles">
@@ -21,6 +21,7 @@
 {{if $page.Problem}}<p class="alert panel__note" role="alert">{{$page.Problem}}</p>
 {{else if eq $page.Saved "name" "settings"}}<p class="saved panel__note" role="status">Saved.{{if eq $page.Saved "settings"}} The source starts again with them.{{end}}</p>
 {{else if eq $page.Saved "signout"}}<p class="saved panel__note" role="status">Signed out. It stops archiving until you log in again.</p>
+{{else if eq $page.Saved "login"}}<p class="saved panel__note" role="status">Logged in. It starts archiving now.</p>
 {{end}}<div class="panel__grid">
 <div class="panel__cell">
 <h3 class="panel__label">Name</h3>
@@ -37,6 +38,7 @@
 {{if $p.Progress}}<div class="progress-line"><span>Catching up on history</span><span>{{$p.Progress.Done}} of {{$p.Progress.Total}} chats</span></div>
 <progress class="progress" max="{{$p.Progress.Total}}" value="{{$p.Progress.Done}}">{{$p.Progress.Done}} of {{$p.Progress.Total}} chats</progress>
 {{else}}<p>{{$p.Status}}</p>
+{{end}}{{if $p.LoginHref}}<p><a class="button" href="{{$p.LoginHref}}">{{$p.LoginLabel}}</a></p>
 {{end}}<p class="panel__help">New messages are saved the moment they arrive, whatever else it is doing.</p>
 </div>
 </div>
````

`internal/web/templates/sources_setup.html`:

````html
{{define "add"}}{{template "head" .}}
<body class="app">
{{template "topbar" .}}
{{template "banner" .}}
<main class="plain plain--settings">
<p><a class="panel__link" href="/sources">Back to sources</a></p>
<h1>Add a source</h1>
{{if .Choices}}<form class="card" method="post" action="/sources/new">
<input type="hidden" name="csrf" value="{{.CSRF}}">
{{if .Problem}}<p class="alert" role="alert">{{.Problem}}</p>{{end}}
<fieldset class="kinds"><legend class="panel__label">What kind of account?</legend>
{{range .Choices}}<label class="kind">{{template "logo" .Logo}}<input type="radio" name="connector" value="{{.Name}}"{{if .Checked}} checked{{end}}> <span>{{.Title}}</span></label>
{{end}}</fieldset>
<div class="field"><label for="source-name">Name, as the source picker shows it</label><input id="source-name" name="name" value="{{.Name}}" maxlength="100" placeholder="Personal" required></div>
<button class="button" type="submit">Continue</button>
</form>
{{else}}<p>No kind of source is built into this copy of arc5 yet. Telegram arrives with the next release.</p>
{{end}}</main>
</body>
</html>{{end}}

{{define "login-step"}}{{template "head" .}}
<body class="app">
{{template "topbar" .}}
<main class="plain plain--settings">
<p><a class="panel__link" href="/sources/{{.AccountID}}">Back to {{.Name}}</a></p>
<h1>Log in to {{.Name}}</h1>
<form class="card" method="post" action="/sources/{{.AccountID}}/login">
<input type="hidden" name="csrf" value="{{.CSRF}}">
{{if .StepTitle}}<h2 class="card__title">{{.StepTitle}}</h2>{{end}}
{{if .Help}}<p class="card__help">{{.Help}}</p>{{end}}
{{if .Problem}}<p class="alert" role="alert">{{.Problem}}</p>{{end}}
<div class="step-fields">{{range .Fields}}{{template "setting-field" .}}{{end}}</div>
<button class="button" type="submit">Continue</button>
</form>
<form class="page-links" method="post" action="/sources/{{.AccountID}}/login/cancel"><input type="hidden" name="csrf" value="{{.CSRF}}"><button class="link-button link-button--accent" type="submit">Cancel the login</button></form>
</main>
</body>
</html>{{end}}
````

- [ ] **Step 4: Run the tests to verify they pass**

Run: `gofmt -l internal cmd; go vet ./... && go test -race -count=1 -timeout 180s ./...`
Expected: `gofmt -l` prints nothing; every package PASSES.

- [ ] **Step 5: Commit**

```bash
git add internal/web internal/web/static internal/web/templates
git commit -m "Add a source through its login, and log in again when a source needs it"
```

---

### Task 11: Keeping the Sources page current

The spec's progress controller: while the Sources page is open and in view, its tiles and the chosen source's state refresh every five seconds by fetching the page again and swapping in the same elements.

**Files:**
- Create: `internal/web/static/js/progress_controller.js`
- Modify: `internal/web/static/js/app.js`, `internal/web/templates/sources.html`
- Test: `internal/web/sources_test.go`

**Interfaces:**
- Produces: `static/js/progress_controller.js` (registered as `progress`, value `every`); elements `#source-tiles` and `#source-state`.

- [ ] **Step 1: Write the failing tests**

Change `internal/web/sources_test.go`:

````diff
--- a/internal/web/sources_test.go
+++ b/internal/web/sources_test.go
@@ -155,3 +155,17 @@ func TestRemoveSourceKeepsItsArchive(t *testing.T) {
 		}
 	}
 }
+
+// The tiles and the chosen source's state refresh themselves while the
+// Sources page is open.
+func TestSourcesPageKeepsItsStatesCurrent(t *testing.T) {
+	s := newSite(t, "")
+	s.signIn()
+	s.fakeAccount("Personal")
+	has(t, s.page("/sources"),
+		`<div id="source-tiles" data-controller="progress" data-progress-every-value="5000">`,
+		`<div class="panel__cell" id="source-state" data-controller="progress" data-progress-every-value="5000">`)
+	if rec := s.get(s.srv.asset("js/progress_controller.js")); rec.Code != http.StatusOK {
+		t.Errorf("progress_controller.js: %d", rec.Code)
+	}
+}
````

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go vet ./internal/web/; go test -count=1 ./internal/web/`
Expected: FAIL: `TestSourcesPageKeepsItsStatesCurrent` lacks the two refreshing elements.

- [ ] **Step 3: Write the implementation**

Change `internal/web/static/js/app.js`:

````diff
--- a/internal/web/static/js/app.js
+++ b/internal/web/static/js/app.js
@@ -4,6 +4,7 @@ import PickerController from "./picker_controller.js"
 import ScrollController from "./scroll_controller.js"
 import JumpController from "./jump_controller.js"
 import SearchController from "./search_controller.js"
+import ProgressController from "./progress_controller.js"
 
 const application = Application.start()
 application.register("theme", ThemeController)
@@ -11,3 +12,4 @@ application.register("picker", PickerController)
 application.register("scroll", ScrollController)
 application.register("jump", JumpController)
 application.register("search", SearchController)
+application.register("progress", ProgressController)
````

`internal/web/static/js/progress_controller.js`:

````js
import { Controller } from "./stimulus.js"

// Keeps a part of the page current: every few seconds, while the page is in
// view, it fetches the page again and swaps in the element with the same id.
// The server renders and escapes it, as for a normal page load.
export default class extends Controller {
  static values = { every: Number }

  connect() {
    this.timer = setInterval(() => this.refresh(), this.everyValue || 5000)
  }

  disconnect() {
    clearInterval(this.timer)
  }

  async refresh() {
    if (document.hidden || this.busy) return
    this.busy = true
    try {
      const response = await fetch(window.location.href, { credentials: "same-origin" })
      if (!response.ok || response.redirected) return
      const page = new DOMParser().parseFromString(await response.text(), "text/html")
      const fresh = page.getElementById(this.element.id)
      if (fresh) this.element.innerHTML = fresh.innerHTML
    } catch {
      // offline for a moment; try again next time
    } finally {
      this.busy = false
    }
  }
}
````

Change `internal/web/templates/sources.html`:

````diff
--- a/internal/web/templates/sources.html
+++ b/internal/web/templates/sources.html
@@ -5,12 +5,13 @@
 <main class="plain plain--wide">
 <div class="page-head"><h1>Sources</h1><a class="button" href="/sources/new"><svg class="button__icon" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2.4" stroke-linecap="round" aria-hidden="true"><path d="M12 5v14M5 12h14"/></svg>Add a source</a></div>
 {{if eq .Saved "removed"}}<p class="saved" role="status">Removed. Its messages stay in the archive.</p>{{end}}
+<div id="source-tiles" data-controller="progress" data-progress-every-value="5000">
 {{range .Groups}}<h2 class="group-heading">{{.Title}}</h2>
 <div class="tiles">
 {{range .Tiles}}<a class="tile tile--wide{{if .Current}} tile--current{{end}}{{if .Warn}} tile--warn{{end}}" href="{{.Href}}"{{if .Current}} aria-current="true"{{end}}>{{template "logo" .Logo}}<span class="tile__text"><span class="tile__name">{{.Name}}</span><span class="tile__sub">{{.Detail}}</span></span>{{template "state" .}}</a>
 {{end}}</div>
 {{else}}<p class="empty">No sources yet.</p>
-{{end}}
+{{end}}</div>
 {{with .Panel}}{{template "source-panel" (dict "P" . "Page" $)}}{{end}}
 </main>
 </body>
@@ -33,7 +34,7 @@
 <p class="panel__help">Private chats and groups are archived as they appear. Channels and bots only when you switch them on.</p>
 <p><a class="panel__link" href="{{$p.ChatsHref}}">Choose which of the {{$p.Chats}} chats are archived</a></p>
 </div>
-<div class="panel__cell">
+<div class="panel__cell" id="source-state" data-controller="progress" data-progress-every-value="5000">
 <h3 class="panel__label">State</h3>
 {{if $p.Progress}}<div class="progress-line"><span>Catching up on history</span><span>{{$p.Progress.Done}} of {{$p.Progress.Total}} chats</span></div>
 <progress class="progress" max="{{$p.Progress.Total}}" value="{{$p.Progress.Done}}">{{$p.Progress.Done}} of {{$p.Progress.Total}} chats</progress>
````

- [ ] **Step 4: Run the tests to verify they pass**

Run: `gofmt -l internal cmd; go vet ./... && go test -race -count=1 -timeout 180s ./...`
Expected: `gofmt -l` prints nothing; every package PASSES.

- [ ] **Step 5: Commit**

```bash
git add internal/web internal/web/static/js internal/web/templates
git commit -m "Keep the Sources page's states current while it is open"
```

---

### Task 12: Attachments: withheld view-once media, missing files, and the demo's Fetch now

Three findings around media. View-once media a source chose not to keep is shown without "Fetch now" and the server refuses to fetch it (decision 7). A file missing from the data folder answers 404 rather than 500. The demo connector answers "Fetch now" by marking the file unavailable, so the button does not wait forever.

**Files:**
- Modify: `internal/connector/demo/demo.go`, `internal/web/conversation.go`, `internal/web/media.go`
- Test: `internal/connector/demo/demo_test.go`, `internal/web/conversation_test.go`, `internal/web/media_test.go`

**Interfaces:**
- Produces: attachment mode `withheld`; `POST /media/{id}/fetch` answers 409 for skipped ephemeral media; `GET /media/{id}` answers 404 when the file is gone; the demo's `Run` reads `RequestedMedia`.

- [ ] **Step 1: Write the failing tests**

Change `internal/connector/demo/demo_test.go`:

````diff
--- a/internal/connector/demo/demo_test.go
+++ b/internal/connector/demo/demo_test.go
@@ -124,6 +124,9 @@ type progressSink struct {
 	reported chan connector.Progress
 }
 
+// RequestedMedia never delivers: nobody asks this sink for files.
+func (p *progressSink) RequestedMedia() <-chan model.AttachmentRef { return nil }
+
 func (s *progressSink) ReportProgress(p connector.Progress) { s.reported <- p }
 
 // memState is a connector.State in memory.
@@ -184,3 +187,33 @@ func TestDemoIsHiddenButHasSettingsAndALogin(t *testing.T) {
 	stop()
 	<-done
 }
+
+// fetchSink records what the demo does with a request to fetch a file.
+type fetchSink struct {
+	connector.Sink
+	requests chan model.AttachmentRef
+	statuses chan model.AttachmentStatus
+}
+
+func (f *fetchSink) RequestedMedia() <-chan model.AttachmentRef { return f.requests }
+func (f *fetchSink) ReportProgress(connector.Progress)          {}
+func (f *fetchSink) SetAttachmentStatus(_ model.AttachmentRef, s model.AttachmentStatus, _ string) error {
+	f.statuses <- s
+	return nil
+}
+
+func TestDemoAnswersFetchRequests(t *testing.T) {
+	sink := &fetchSink{requests: make(chan model.AttachmentRef, 1), statuses: make(chan model.AttachmentStatus, 1)}
+	sink.requests <- model.AttachmentRef{ChatSourceID: "dan", MessageSourceID: "1"}
+	ctx, cancel := context.WithCancel(context.Background())
+	defer cancel()
+	go idle{}.Run(ctx, sink, memState{}, nil)
+	select {
+	case s := <-sink.statuses:
+		if s != model.StatusUnavailable {
+			t.Errorf("status = %q, want unavailable", s)
+		}
+	case <-time.After(5 * time.Second):
+		t.Fatal("the demo never answered the request")
+	}
+}
````

Change `internal/web/conversation_test.go`:

````diff
--- a/internal/web/conversation_test.go
+++ b/internal/web/conversation_test.go
@@ -201,8 +201,13 @@ func TestAttachmentModes(t *testing.T) {
 	if strings.Count(body, "<img ") != 1 {
 		t.Error("something other than the one photo is drawn as an image")
 	}
-	if got := strings.Count(body, `name="csrf"`); got != 4 {
-		t.Errorf("%d CSRF fields; want the sign-out form plus three fetch forms", got)
+	if got := strings.Count(body, `name="csrf"`); got != 3 {
+		t.Errorf("%d CSRF fields; want the page's token plus two fetch forms", got)
+	}
+	// View-once media the source chose not to keep is not offered for fetching.
+	once := body[strings.Index(body, "once.jpg"):strings.Index(body, "Sent as view-once")]
+	if strings.Contains(once, "Fetch now") {
+		t.Error("withheld view-once media offers Fetch now")
 	}
 }
 
````

Change `internal/web/media_test.go`:

````diff
--- a/internal/web/media_test.go
+++ b/internal/web/media_test.go
@@ -6,6 +6,7 @@ import (
 	"net/http"
 	"net/http/httptest"
 	"net/url"
+	"os"
 	"strings"
 	"testing"
 
@@ -156,3 +157,30 @@ func TestFetchNow(t *testing.T) {
 		t.Errorf("fetch of a bad id: %d", rec.Code)
 	}
 }
+
+// View-once media is fetched only by a source set to keep it; the owner
+// cannot ask for it afterwards, so nothing reports it as opened.
+func TestFetchRefusesWithheldViewOnceMedia(t *testing.T) {
+	s := newSite(t, "")
+	s.signIn()
+	ids := storeFiles(t, s, 0, model.Attachment{Kind: model.AttachPhoto, FileName: "once.jpg", Size: 9, Ephemeral: true, NoFetch: true, NoFetchReason: "self-destructing media is not archived"})
+	if rec := s.post(fmt.Sprintf("/media/%d/fetch", ids[0]), nil); rec.Code != http.StatusConflict || len(s.runner.requested) != 0 {
+		t.Fatalf("fetching withheld view-once media: %d, requested %v", rec.Code, s.runner.requested)
+	}
+}
+
+func TestMissingFileIs404(t *testing.T) {
+	s := newSite(t, "")
+	s.signIn()
+	ids := storeFiles(t, s, 1, model.Attachment{Kind: model.AttachPhoto, FileName: "plan.jpg", MimeType: "image/jpeg", Size: 9})
+	file, err := s.store.AttachmentFile(ids[0])
+	if err != nil {
+		t.Fatal(err)
+	}
+	if err := os.Remove(file.Path); err != nil {
+		t.Fatal(err)
+	}
+	if rec := s.get(fmt.Sprintf("/media/%d", ids[0])); rec.Code != http.StatusNotFound {
+		t.Errorf("a file missing from disk: %d, want 404", rec.Code)
+	}
+}
````

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go vet ./internal/web/ ./internal/connector/demo/; go test -count=1 ./internal/web/ ./internal/connector/demo/`
Expected: FAIL: `TestAttachmentModes` counts 4 CSRF fields, want 3; `TestFetchRefusesWithheldViewOnceMedia` and `TestMissingFileIs404` (500, want 404) fail; `TestDemoAnswersFetchRequests` times out.

- [ ] **Step 3: Write the implementation**

Change `internal/connector/demo/demo.go`:

````diff
--- a/internal/connector/demo/demo.go
+++ b/internal/connector/demo/demo.go
@@ -36,8 +36,17 @@ func (idle) Run(ctx context.Context, sink connector.Sink, state connector.State,
 		return errors.Join(connector.ErrNeedsLogin, err)
 	}
 	sink.ReportProgress(connector.Progress{Phase: connector.PhaseLive})
-	<-ctx.Done()
-	return ctx.Err()
+	for {
+		select {
+		case <-ctx.Done():
+			return ctx.Err()
+		case ref := <-sink.RequestedMedia():
+			// "Fetch now" on a sample file: there is nothing to fetch.
+			if err := sink.SetAttachmentStatus(ref, model.StatusUnavailable, "the demo has no files to fetch"); err != nil {
+				return err
+			}
+		}
+	}
 }
 
 func (idle) Setup(state connector.State) connector.SetupFlow { return &login{state: state} }
````

Change `internal/web/conversation.go`:

````diff
--- a/internal/web/conversation.go
+++ b/internal/web/conversation.go
@@ -57,7 +57,7 @@ type Version struct {
 
 type Attachment struct {
 	ID        int64
-	Mode      string // image, audio, video, file, skipped, pending, failed or unavailable
+	Mode      string // image, audio, video, file, skipped, withheld, pending, failed or unavailable
 	Name      string
 	Detail    string
 	URL       string
@@ -125,6 +125,9 @@ func attachmentView(a archive.StoredAttachment) Attachment {
 		}
 	case model.StatusSkipped:
 		v.Mode, v.Detail = "skipped", size+", not downloaded: "+a.Reason
+		if a.Ephemeral {
+			v.Mode = "withheld" // kept out by the source's setting; never fetched on request
+		}
 	case model.StatusFailed:
 		v.Mode, v.Detail = "failed", "The download failed: "+a.Reason
 	case model.StatusUnavailable:
````

Change `internal/web/media.go`:

````diff
--- a/internal/web/media.go
+++ b/internal/web/media.go
@@ -2,6 +2,7 @@ package web
 
 import (
 	"errors"
+	"io/fs"
 	"mime"
 	"net/http"
 	"os"
@@ -10,6 +11,7 @@ import (
 	"time"
 
 	"github.com/fenelon/arc5/internal/archive"
+	"github.com/fenelon/arc5/internal/model"
 )
 
 // mediaPolicy stops a file from running script or loading anything, even if a
@@ -35,6 +37,10 @@ func (s *Server) serveMedia(w http.ResponseWriter, r *http.Request, _ archive.Se
 		return
 	}
 	f, err := os.Open(file.Path)
+	if errors.Is(err, fs.ErrNotExist) {
+		http.Error(w, "This file is missing from the data folder.", http.StatusNotFound)
+		return
+	}
 	if err != nil {
 		s.fail(w, err)
 		return
@@ -84,6 +90,11 @@ func (s *Server) fetchMedia(w http.ResponseWriter, r *http.Request, _ archive.Se
 		http.NotFound(w, r)
 		return
 	}
+	file, err := s.deps.Store.AttachmentFile(id)
+	if err == nil && file.Ephemeral && file.Status == model.StatusSkipped {
+		http.Error(w, "Self-destructing media is fetched only when its source is set to keep it.", http.StatusConflict)
+		return
+	}
 	switch err := s.deps.Runner.RequestMedia(id); {
 	case errors.Is(err, archive.ErrNotFound):
 		http.NotFound(w, r)
````

- [ ] **Step 4: Run the tests to verify they pass**

Run: `gofmt -l internal cmd; go vet ./... && go test -race -count=1 -timeout 180s ./...`
Expected: `gofmt -l` prints nothing; every package PASSES.

- [ ] **Step 5: Commit**

```bash
git add internal/connector/demo internal/web
git commit -m "Withhold view-once media from Fetch now, 404 for missing files, and answer the demo's fetches"
```

---

### Task 13: Hardening sign-in and redirects

The remaining security findings of the 3a review: a tab or other control character can no longer turn a "back" address into another site's; a damaged password hash cannot make the check panic or allocate without bound; two first-run forms sent at once cannot both set the password. A table test checks every route for a session and every change for the token.

**Files:**
- Modify: `internal/archive/settings.go`, `internal/web/auth.go`, `internal/web/auth_handlers.go`, `internal/web/media.go`
- Test: `internal/archive/settings_test.go`, `internal/web/auth_test.go`, `internal/web/media_test.go`, `internal/web/server_test.go`

**Interfaces:**
- Produces: `(*archive.Store).SetSettingIfAbsent(key, value string) (bool, error)`; `(*auth).setFirstPassword(password string) error`, `errPasswordExists`; `POST /setup` uses it and sends a late second form to the login page.

- [ ] **Step 1: Write the failing tests**

Change `internal/archive/settings_test.go`:

````diff
--- a/internal/archive/settings_test.go
+++ b/internal/archive/settings_test.go
@@ -53,3 +53,16 @@ func TestConnectorStateIsPerAccount(t *testing.T) {
 		t.Fatalf("other account sees state %v", v)
 	}
 }
+
+func TestSetSettingIfAbsent(t *testing.T) {
+	s := openTemp(t)
+	if set, err := s.SetSettingIfAbsent("k", "first"); err != nil || !set {
+		t.Fatalf("first = %v, %v", set, err)
+	}
+	if set, err := s.SetSettingIfAbsent("k", "second"); err != nil || set {
+		t.Fatalf("second = %v, %v; want it refused", set, err)
+	}
+	if v, _, _ := s.Setting("k"); v != "first" {
+		t.Errorf("value = %q", v)
+	}
+}
````

Change `internal/web/auth_test.go`:

````diff
--- a/internal/web/auth_test.go
+++ b/internal/web/auth_test.go
@@ -41,7 +41,9 @@ func TestPasswordHashing(t *testing.T) {
 			t.Errorf("wrong password %q was accepted", wrong)
 		}
 	}
-	for _, broken := range []string{"", "argon2id$", "plain$1$2$3$4$5", "argon2id$x$2$1$AAAA$AAAA", "argon2id$19456$2$1$***$AAAA"} {
+	for _, broken := range []string{"", "argon2id$", "plain$1$2$3$4$5", "argon2id$x$2$1$AAAA$AAAA", "argon2id$19456$2$1$***$AAAA",
+		"argon2id$19456$0$1$AAAAAAAAAAAAAAAAAAAAAA$AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA", "argon2id$4294967295$2$1$AAAAAAAAAAAAAAAAAAAAAA$AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA",
+		"argon2id$19456$2$255$AAAAAAAAAAAAAAAAAAAAAA$AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"} {
 		if verifyPassword(broken, "correct horse") {
 			t.Errorf("broken hash %q verified", broken)
 		}
@@ -218,3 +220,20 @@ func TestSessionCookie(t *testing.T) {
 		t.Error("the cookie is Secure on a plain HTTP request, so it would never be sent back")
 	}
 }
+
+// Two first-run forms sent at once cannot both choose the password.
+func TestFirstPasswordIsSetOnce(t *testing.T) {
+	a, _ := newAuth(t, "")
+	if err := a.setFirstPassword("first horse"); err != nil {
+		t.Fatal(err)
+	}
+	if err := a.setFirstPassword("second horse"); !errors.Is(err, errPasswordExists) {
+		t.Fatalf("a second first password = %v, want errPasswordExists", err)
+	}
+	if ok, _ := a.check("first horse"); !ok {
+		t.Error("the first password was replaced")
+	}
+	if err := a.setFirstPassword("short"); !errors.Is(err, errShortPassword) {
+		t.Errorf("a short first password = %v", err)
+	}
+}
````

Change `internal/web/media_test.go`:

````diff
--- a/internal/web/media_test.go
+++ b/internal/web/media_test.go
@@ -126,7 +126,7 @@ func TestFetchNow(t *testing.T) {
 		t.Fatalf("fetch: %d to %q, requested %v", rec.Code, rec.Header().Get("Location"), s.runner.requested)
 	}
 	// Only an address on this site is followed back.
-	for _, back := range []string{"https://evil.example/", "//evil.example/", `/\evil.example`, "", "javascript:alert(1)"} {
+	for _, back := range []string{"https://evil.example/", "//evil.example/", `/\evil.example`, "", "javascript:alert(1)", "/\t/evil.example/", "/\x01/x", "/\x7f"} {
 		if rec := s.post(path, url.Values{"back": {back}}); !redirectsTo(rec, "/") {
 			t.Errorf("back %q: redirected to %q, want /", back, rec.Header().Get("Location"))
 		}
````

Change `internal/web/server_test.go`:

````diff
--- a/internal/web/server_test.go
+++ b/internal/web/server_test.go
@@ -2,6 +2,7 @@ package web
 
 import (
 	"context"
+	"fmt"
 	"net/http"
 	"net/http/httptest"
 	"net/url"
@@ -359,3 +360,38 @@ func TestParallelLoginsAreThrottled(t *testing.T) {
 		t.Fatalf("%d wrong passwords were checked and %d refused; want at most 5 checked", checked, refused)
 	}
 }
+
+// Review Focus 2, for every route: no page without a session, and no change
+// without the session's token.
+func TestEveryRouteNeedsASessionAndEveryChangeTheToken(t *testing.T) {
+	s := newSite(t, "")
+	s.signIn()
+	acc := s.fakeAccount("Personal")
+	s.say(acc, "Dan", "1", "hello", noon)
+	chat := s.chatID(acc, "Dan")
+	gets := []string{"/", "/s/chats", fmt.Sprintf("/s/%d", acc), fmt.Sprintf("/s/%d/c/%d", acc, chat), fmt.Sprintf("/s/%d/results?q=x", acc),
+		"/media/1", "/sources", "/sources/new", fmt.Sprintf("/sources/%d", acc), fmt.Sprintf("/sources/%d/chats", acc),
+		fmt.Sprintf("/sources/%d/login", acc), "/settings"}
+	posts := []string{"/logout", "/media/1/fetch", "/sources/new", fmt.Sprintf("/sources/%d/name", acc), fmt.Sprintf("/sources/%d/settings", acc),
+		fmt.Sprintf("/sources/%d/signout", acc), fmt.Sprintf("/sources/%d/remove", acc), fmt.Sprintf("/sources/%d/chats", acc),
+		fmt.Sprintf("/sources/%d/login", acc), fmt.Sprintf("/sources/%d/login/cancel", acc),
+		"/settings/appearance", "/settings/languages", "/settings/attachments", "/settings/password"}
+
+	stranger := &site{t: t, store: s.store, srv: s.srv, cookies: map[string]*http.Cookie{}}
+	for _, path := range gets {
+		if rec := stranger.get(path); !redirectsTo(rec, "/login") {
+			t.Errorf("GET %s without a session: %d to %q", path, rec.Code, rec.Header().Get("Location"))
+		}
+	}
+	for _, path := range posts {
+		if rec := stranger.post(path, url.Values{"csrf": {s.csrf}}); !redirectsTo(rec, "/login") {
+			t.Errorf("POST %s without a session: %d", path, rec.Code)
+		}
+		if rec := s.post(path, url.Values{"csrf": {"made-up"}}); rec.Code != http.StatusForbidden {
+			t.Errorf("POST %s with a wrong token: %d, want 403", path, rec.Code)
+		}
+	}
+	if got, _ := s.store.Account(acc); !got.RemovedAt.IsZero() || got.Label != "Personal" {
+		t.Error("a refused request changed the source")
+	}
+}
````

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go vet ./internal/archive/ ./internal/web/; go test -count=1 ./internal/archive/ ./internal/web/`
Expected: FAIL to build: `s.SetSettingIfAbsent undefined`, `a.setFirstPassword undefined`. Once they exist, `TestFetchNow` fails on the tab address and `TestPasswordHashing` panics on the zero-pass hash until the fixes are in. `TestEveryRouteNeedsASessionAndEveryChangeTheToken` passes as soon as it compiles: it pins behaviour that already holds.

- [ ] **Step 3: Write the implementation**

Change `internal/archive/settings.go`:

````diff
--- a/internal/archive/settings.go
+++ b/internal/archive/settings.go
@@ -26,6 +26,17 @@ func (s *Store) SetSetting(key, value string) error {
 	return err
 }
 
+// SetSettingIfAbsent stores a setting only if it has no value yet, and
+// reports whether it did.
+func (s *Store) SetSettingIfAbsent(key, value string) (bool, error) {
+	res, err := s.w.Exec(`INSERT INTO settings (key, value) VALUES (?, ?) ON CONFLICT (key) DO NOTHING`, key, value)
+	if err != nil {
+		return false, err
+	}
+	n, err := res.RowsAffected()
+	return n == 1, err
+}
+
 func mediaCap(q queryer) (int64, error) {
 	var v string
 	err := q.QueryRow(`SELECT value FROM settings WHERE key = ?`, mediaCapKey).Scan(&v)
````

Change `internal/web/auth.go`:

````diff
--- a/internal/web/auth.go
+++ b/internal/web/auth.go
@@ -44,9 +44,10 @@ const (
 )
 
 var (
-	errShortPassword = fmt.Errorf("the password needs at least %d characters", minPasswordLen)
-	errEnvPassword   = errors.New("the password is set by ARC5_PASSWORD and cannot be changed here")
-	errLoginBusy     = errors.New("another sign-in is being checked")
+	errShortPassword  = fmt.Errorf("the password needs at least %d characters", minPasswordLen)
+	errEnvPassword    = errors.New("the password is set by ARC5_PASSWORD and cannot be changed here")
+	errLoginBusy      = errors.New("another sign-in is being checked")
+	errPasswordExists = errors.New("a password was already chosen")
 )
 
 func hashPassword(password string) (string, error) {
@@ -72,6 +73,12 @@ func verifyPassword(encoded, password string) bool {
 	if err1 != nil || err2 != nil || err3 != nil || err4 != nil || err5 != nil || len(want) == 0 || threads == 0 {
 		return false
 	}
+	// Only arc5 writes these hashes, but a damaged one must not make the
+	// check panic or allocate without bound.
+	if memory < 1024 || memory > 256*1024 || passes < 1 || passes > 16 || threads < 1 || threads > 16 ||
+		len(salt) < 8 || len(want) < 16 || len(want) > 64 {
+		return false
+	}
 	got := argon2.IDKey([]byte(password), salt, uint32(passes), uint32(memory), uint8(threads), uint32(len(want)))
 	return subtle.ConstantTimeCompare(got, want) == 1
 }
@@ -151,6 +158,27 @@ func (a *auth) setPassword(password string) error {
 	return a.store.DeleteAllSessions()
 }
 
+// setFirstPassword stores the password chosen at first run. It fails with
+// errPasswordExists if one was chosen meanwhile, so two first-run forms sent
+// at once cannot both set it.
+func (a *auth) setFirstPassword(password string) error {
+	if a.usesEnv() {
+		return errEnvPassword
+	}
+	if utf8.RuneCountInString(password) < minPasswordLen {
+		return errShortPassword
+	}
+	hash, err := hashPassword(password)
+	if err != nil {
+		return err
+	}
+	set, err := a.store.SetSettingIfAbsent(passwordKey, hash)
+	if err == nil && !set {
+		err = errPasswordExists
+	}
+	return err
+}
+
 func (a *auth) check(password string) (bool, error) {
 	if a.usesEnv() {
 		got, want := sha256.Sum256([]byte(password)), sha256.Sum256([]byte(a.envPassword))
````

Change `internal/web/auth_handlers.go`:

````diff
--- a/internal/web/auth_handlers.go
+++ b/internal/web/auth_handlers.go
@@ -52,8 +52,11 @@ func (s *Server) setup(w http.ResponseWriter, r *http.Request) {
 	problem := ""
 	if password != r.PostFormValue("password_again") {
 		problem = "The two passwords are not the same. Type the same one twice."
-	} else if err := s.auth.setPassword(password); errors.Is(err, errShortPassword) {
+	} else if err := s.auth.setFirstPassword(password); errors.Is(err, errShortPassword) {
 		problem = "That password is too short. Use at least 8 characters."
+	} else if errors.Is(err, errPasswordExists) {
+		redirect(w, r, "/login") // another first-run form got there first
+		return
 	} else if err != nil {
 		s.fail(w, err)
 		return
````

Change `internal/web/media.go`:

````diff
--- a/internal/web/media.go
+++ b/internal/web/media.go
@@ -76,7 +76,9 @@ func (s *Server) serveMedia(w http.ResponseWriter, r *http.Request, _ archive.Se
 
 // localPath returns back if it is an address on this site, else "/".
 func localPath(back string) string {
-	if !strings.HasPrefix(back, "/") || strings.HasPrefix(back, "//") || strings.ContainsAny(back, "\\\r\n") {
+	if !strings.HasPrefix(back, "/") || strings.HasPrefix(back, "//") || strings.ContainsFunc(back, func(r rune) bool {
+		return r == '\\' || r < 0x20 || r == 0x7f // browsers drop tabs and newlines, which would turn "/\t/x" into "//x"
+	}) {
 		return "/"
 	}
 	return back
````

- [ ] **Step 4: Run the tests to verify they pass**

Run: `gofmt -l internal cmd; go vet ./... && go test -race -count=1 -timeout 180s ./...`
Expected: `gofmt -l` prints nothing; every package PASSES.

- [ ] **Step 5: Commit**

```bash
git add internal/archive internal/web
git commit -m "Refuse control characters in redirects, bound stored hash parameters, and set the first password once"
```

---

### Task 14: Conversation polish

An edit made on a later day says which day ("Edited on 29 Sep at 14:00"). When a conversation opens at its newest message, pictures that load afterwards no longer push that message out of view, unless the reader has scrolled up.

**Files:**
- Modify: `internal/web/conversation.go`, `internal/web/static/js/scroll_controller.js`, `internal/web/templates/conversation.html`
- Test: `internal/web/conversation_test.go`

**Interfaces:**
- Produces: `Message.Edited` holds `"at 15:04"`, `"on 2 Oct at 15:04"` or `"on 2 Oct 2025 at 15:04"`; the scroll controller follows `load` events while at the end.

- [ ] **Step 1: Write the failing tests**

Change `internal/web/conversation_test.go`:

````diff
--- a/internal/web/conversation_test.go
+++ b/internal/web/conversation_test.go
@@ -297,3 +297,24 @@ func TestRailFollowsTheTimeZone(t *testing.T) {
 	has(t, s.page(path), fmt.Sprintf(`href="%s?date=2025-10-01" aria-label="October to December 2025, 1 message"`, path))
 	has(t, s.page(path+"?date=2025-10-01"), "happy new year")
 }
+
+// An edit made on a later day says which day.
+func TestEditedOnAnotherDayShowsTheDay(t *testing.T) {
+	s := newSite(t, "")
+	s.signIn()
+	acc := s.account("Personal", "telegram")
+	s.say(acc, "Dan", "1", "first", noon)
+	s.say(acc, "Dan", "1", "second", noon, func(m *model.Message) { m.EditedAt = noon.Add(26 * time.Hour) })
+	body := s.page(fmt.Sprintf("/s/%d/c/%d", acc, s.chatID(acc, "Dan")))
+	has(t, body, "Edited on 29 Sep at 14:00, show earlier")
+}
+
+// The page tells the scroll controller to keep the newest message in view
+// while pictures above it load.
+func TestScrollControllerFollowsLoadingPictures(t *testing.T) {
+	s := newSite(t, "")
+	body := s.get(s.srv.asset("js/scroll_controller.js")).Body.String()
+	if !strings.Contains(body, `addEventListener("load"`) {
+		t.Error("the scroll controller does not scroll again when a picture loads")
+	}
+}
````

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go vet ./internal/web/; go test -count=1 ./internal/web/`
Expected: FAIL: `TestEditedOnAnotherDayShowsTheDay` lacks "Edited on 29 Sep at 14:00, show earlier"; `TestScrollControllerFollowsLoadingPictures` fails.

- [ ] **Step 3: Write the implementation**

Change `internal/web/conversation.go`:

````diff
--- a/internal/web/conversation.go
+++ b/internal/web/conversation.go
@@ -43,7 +43,7 @@ type Message struct {
 	Text          string
 	Marked        []Piece // Text with the search's words marked, for the message opened from a search
 	Time          string
-	Edited        string // when it was last edited, or ""
+	Edited        string // when it was last edited, "at 15:04" or "on 2 Oct at 15:04", or ""
 	Deleted       string // when the source deleted it, or ""
 	Versions      []Version
 	Attachments   []Attachment
@@ -307,7 +307,15 @@ func (s *Server) conversationView(w http.ResponseWriter, r *http.Request, sess a
 			v.Sender = m.SenderName
 		}
 		if !m.EditedAt.IsZero() {
-			v.Edited = m.EditedAt.In(s.deps.Location).Format("15:04")
+			edited := m.EditedAt.In(s.deps.Location)
+			switch {
+			case edited.Format(time.DateOnly) == sent.Format(time.DateOnly):
+				v.Edited = edited.Format("at 15:04")
+			case edited.Year() == sent.Year():
+				v.Edited = edited.Format("on 2 Jan at 15:04")
+			default:
+				v.Edited = edited.Format("on 2 Jan 2006 at 15:04")
+			}
 		}
 		if !m.DeletedAt.IsZero() {
 			v.Deleted = m.DeletedAt.In(s.deps.Location).Format("2 Jan 2006")
````

Change `internal/web/static/js/scroll_controller.js`:

````diff
--- a/internal/web/static/js/scroll_controller.js
+++ b/internal/web/static/js/scroll_controller.js
@@ -8,10 +8,31 @@ export default class extends Controller {
 
   connect() {
     if (this.toValue === "end") {
-      this.element.scrollTop = this.element.scrollHeight
+      this.toEnd()
+      // Pictures load after this runs and push the newest message down.
+      // Follow them, unless the reader has scrolled up meanwhile.
+      this.onScroll = () => {
+        const el = this.element
+        this.atEnd = el.scrollHeight - el.scrollTop - el.clientHeight < 40
+      }
+      this.onLoad = () => {
+        if (this.atEnd) this.toEnd()
+      }
+      this.element.addEventListener("scroll", this.onScroll, { passive: true })
+      this.element.addEventListener("load", this.onLoad, true) // load does not bubble; catch it on the way down
     } else if (this.toValue !== "start") {
       const target = document.getElementById(this.toValue)
       if (target) target.scrollIntoView({ block: "center" })
     }
   }
+
+  disconnect() {
+    if (this.onScroll) this.element.removeEventListener("scroll", this.onScroll)
+    if (this.onLoad) this.element.removeEventListener("load", this.onLoad, true)
+  }
+
+  toEnd() {
+    this.element.scrollTop = this.element.scrollHeight
+    this.atEnd = true
+  }
 }
````

Change `internal/web/templates/conversation.html`:

````diff
--- a/internal/web/templates/conversation.html
+++ b/internal/web/templates/conversation.html
@@ -23,7 +23,7 @@
 {{range $m.Attachments}}{{template "attachment" (dict "A" . "CSRF" $.CSRF "Back" $.Back)}}
 {{end}}{{if $m.Text}}<p class="msg__text">{{if $m.Marked}}{{template "pieces" $m.Marked}}{{else}}{{$m.Text}}{{end}}</p>{{end}}
 <div class="msg__meta"><time>{{$m.Time}}</time>{{if $m.Deleted}}<span class="tag tag--warn">Deleted at source on {{$m.Deleted}}</span>{{end}}</div>
-{{if $m.Versions}}<details class="versions"><summary>Edited at {{$m.Edited}}, show earlier</summary>{{range $m.Versions}}<p class="versions__item">{{.Text}} <span class="versions__when">until {{.Until}}</span></p>{{end}}</details>{{else if $m.Edited}}<div class="versions">Edited at {{$m.Edited}}</div>{{end}}
+{{if $m.Versions}}<details class="versions"><summary>Edited {{$m.Edited}}, show earlier</summary>{{range $m.Versions}}<p class="versions__item">{{.Text}} <span class="versions__when">until {{.Until}}</span></p>{{end}}</details>{{else if $m.Edited}}<div class="versions">Edited {{$m.Edited}}</div>{{end}}
 </article>{{end}}{{end}}
 
 {{define "attachment"}}{{$a := .A}}{{if eq $a.Mode "image"}}<a href="{{$a.URL}}"><img class="media media--image" src="{{$a.URL}}" alt="{{$a.Name}}" loading="lazy"></a>
````

- [ ] **Step 4: Run the tests to verify they pass**

Run: `gofmt -l internal cmd; go vet ./... && go test -race -count=1 -timeout 180s ./...`
Expected: `gofmt -l` prints nothing; every package PASSES.

- [ ] **Step 5: Commit**

```bash
git add internal/web internal/web/static/js internal/web/templates
git commit -m "Date edits made on another day, and keep the newest message in view as pictures load"
```

---

### Task 15: Wiring, documentation, and a look at it

`serve` already passes the indexer to the interface (Task 5). This task checks the whole path through a real server, points the empty home page at "Add a source", updates the README, and compares the result with the mockups.

**Files:**
- Modify: `README.md`, `internal/web/templates/empty.html`
- Test: `cmd/arc5/main_test.go`, `internal/web/server_test.go`

**Interfaces:**
- Consumes: everything above.

- [ ] **Step 1: Write the failing tests**

Change `cmd/arc5/main_test.go`:

````diff
--- a/cmd/arc5/main_test.go
+++ b/cmd/arc5/main_test.go
@@ -304,6 +304,19 @@ func TestDemoCommandAndTheInterface(t *testing.T) {
 		_, body = read(client.Get(base + "/s/chats"))
 	}
 
+	// Search runs on the index the demo archive was written with.
+	if code, body := read(client.Get(base + "/s/chats?q=marina")); code != http.StatusOK ||
+		!strings.Contains(body, `<mark class="hit">Marina</mark>`) || !strings.Contains(body, "Chats and people") {
+		t.Fatalf("searching the demo: %d", code)
+	}
+	if code, body := read(client.Get(base + "/settings")); code != http.StatusOK || !strings.Contains(body, "The index is up to date for English.") {
+		t.Fatalf("settings: %d", code)
+	}
+	// The demo is not offered as a source to add.
+	if _, body := read(client.Get(base + "/sources/new")); strings.Contains(body, `value="demo"`) {
+		t.Error("the demo connector is offered under Add a source")
+	}
+
 	cancel()
 	select {
 	case err := <-done:
````

Change `internal/web/server_test.go`:

````diff
--- a/internal/web/server_test.go
+++ b/internal/web/server_test.go
@@ -203,7 +203,8 @@ func TestFirstRunSetup(t *testing.T) {
 		}
 	}
 	s.signIn()
-	if rec := s.get("/"); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "No sources yet") {
+	if rec := s.get("/"); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "No sources yet") ||
+		!strings.Contains(rec.Body.String(), `href="/sources/new"`) {
 		t.Fatalf("home after setup: %d", rec.Code)
 	}
 }
````

- [ ] **Step 2: Run the tests to verify they fail**

Run: `go vet ./cmd/arc5/ ./internal/web/; go test -count=1 ./cmd/arc5/ ./internal/web/`
Expected: `go test ./cmd/arc5/` PASSES already (the wiring landed in Task 5; the new assertions pin it). `TestFirstRunSetup` FAILS: the empty home page lacks `href="/sources/new"`.

- [ ] **Step 3: Write the implementation**

Change `README.md`:

````diff
--- a/README.md
+++ b/README.md
@@ -4,10 +4,10 @@ A self-hosted archive for your message history. It keeps a durable, searchable
 copy of your chats so you can clean out accounts or use auto-delete without
 losing anything.
 
-**Status:** early development. The archive, the search engine and a web
-interface for reading chats run. Search in the interface and the Telegram
-connector are not built yet, so the only thing to read is the demo archive.
-The design is in [`docs/superpowers/specs`](docs/superpowers/specs).
+**Status:** early development. The archive, the search engine and the web
+interface run: reading chats, searching, managing sources and settings. The
+Telegram connector is not built yet, so the only thing to read is the demo
+archive. The design is in [`docs/superpowers/specs`](docs/superpowers/specs).
 
 ## Run
 
@@ -36,10 +36,30 @@ arc5 listens on this computer only. To reach it from elsewhere, put it behind
 a reverse proxy that adds TLS, or a VPN. Behind a proxy that sends
 `X-Forwarded-Proto: https`, the login cookie is marked `Secure`.
 
+## Use
+
+- **Search** with the box at the top. It covers the source chosen in the
+  picker, or every source of that kind under "Everything at once". Words are
+  ANDed; `"quoted words"` match as a phrase; the last word also matches as the
+  start of a longer one. Results list matching chats and people first, then
+  messages, best match first or newest first, with filters for attachments,
+  messages deleted at the source, joins and calls, and dates.
+- **Sources** lists every account arc5 keeps a copy of. Choose one to rename
+  it, choose which of its chats are archived, change its settings, sign it
+  out, or remove it. Removing a source stops it and forgets its login; its
+  messages stay in the archive. "Add a source" runs the source's login.
+- **Settings** has the theme, the search languages, the largest attachment
+  downloaded automatically, and the password.
+
+The demo source can be signed out and logged in again from the Sources page;
+its login code is 12345.
+
 ## Search languages
 
 Search finds other forms of a word (`cat` finds `cats`) in the languages you
-choose. The default is English. Choose your own and rebuild the index with:
+choose under Settings, which rebuilds the index in the background. The
+default is English. The same can be done from the command line, with the
+server stopped:
 
 ```bash
 ./arc5 reindex --data ./data --languages en,ru,es
@@ -54,8 +74,8 @@ prefix and by phrase, and Chinese, Japanese and Thai are matched by character
 sequence. List only the languages you write in: each extra one that shares an
 alphabet makes the index larger and matches looser.
 
-Restart a running server after changing the list, so it indexes new messages
-the same way.
+A server running while `arc5 reindex` changes the list keeps its old list
+until it is restarted; changing it under Settings needs no restart.
 
 ## Develop
 
````

Change `internal/web/templates/empty.html`:

````diff
--- a/internal/web/templates/empty.html
+++ b/internal/web/templates/empty.html
@@ -3,7 +3,8 @@
 {{template "topbar" .}}
 <main class="plain plain--wide">
 <h1>No sources yet</h1>
-<p>A source is an account arc5 keeps a copy of. Connecting one arrives with the next release.</p>
+<p>A source is an account arc5 keeps a copy of.</p>
+<p><a class="button" href="/sources/new">Add a source</a></p>
 <p>To look around first, stop the server, run <span class="code">arc5 demo --data &lt;folder&gt;</span> and start it again on that folder.</p>
 </main>
 </body>
````

- [ ] **Step 4: Run the tests to verify they pass**

Run: `gofmt -l internal cmd; go vet ./... && go test -race -count=1 -timeout 180s ./...`
Expected: `gofmt -l` prints nothing; every package PASSES.

- [ ] **Step 5: Look at it**

```bash
DEMO=$(mktemp -d)
go run ./cmd/arc5 demo --data "$DEMO"
ARC5_PASSWORD=demo-password go run ./cmd/arc5 serve --data "$DEMO" --listen 127.0.0.1:8085
```

Open <http://127.0.0.1:8085>, sign in with `demo-password`, and compare with the mockups in `docs/superpowers/mockups/2026-10-13-interface/`: `StrataDark` (search, dark), `Sources` (light), `Settings` (dark) and `PhoneChats` (phone). Expected:

- The top bar has the logo, the picker, the search box across the middle, the theme button and "Settings".
- Typing `marina` replaces the chat list after a moment, without a reload, with "Best match / Newest first" and "Filters", then "Chats and people" (Marina Sokolova, Familia: Marina y Pablo, and Marina Sokolova as a person with "Everything from this person, in any chat"), then "Messages" with "Marina" marked; the address now ends in `?q=marina`.
- Opening Dan Whitfield's result shows his chat at "Marina's flight lands", the word marked and the message bordered in the search colour, with the results still on the left and the opened one tinted.
- The person row lists everything Marina wrote, with "All results" to go back. Escape in the search box brings the chat list back.
- Sources shows "Messengers, shown as chats" with Personal ("Demo, last message on 28 Sep") and the panel "Settings for Personal": Rename, "Choose which of the 5 chats are archived", the state, the demo's two settings, "Sign this source out" and "Remove this source and keep its archive". "Add a source" says no kind of source is built in yet.
- "Sign this source out" turns the source's mark into a padlock, shows the red banner "Personal is not archiving. Needs login since …" with "Log in again", and the panel offers "Log in again". The login asks for a code; 12345 brings it back to Live and the banner goes.
- Settings shows Appearance (choosing Dark applies at once), the 18 languages in name order with English ticked and "The index is up to date for English.", the attachment limit 50, the note that the password comes from ARC5_PASSWORD, and "Manage sources" and "Sign out".
- Ticking Russian and saving shows the rebuild, then "The index is up to date for English and Russian."
- On a phone-sized window the search box sits under the picker, Settings is an icon, and results fill the screen.
- The browser console reports no content-security-policy violations.

Stop the server with Ctrl-C. Fix anything that differs before committing; a fix to the styles needs no new test, a fix to the markup or a handler does.


- [ ] **Step 6: Commit**

```bash
git add README.md cmd/arc5 internal/web internal/web/templates
git commit -m "Point the empty archive at Add a source and document search, sources and settings"
```

---

## After the plan

Add a "Post-review changes" section to this plan once it is executed and
reviewed, and update `docs/superpowers/STATUS.md`: milestone 3 is then done,
and the next plan is milestone 4, the Telegram connector, which needs the two
decisions STATUS lists before it is planned. Telegram's connector implements
`connector.SignOuter` and its setup flow implements `io.Closer` if it keeps a
connection open between login steps.

## Post-review changes

Executed on branch `search-sources-settings`, one commit per task, from the
code blocks above. The changes made on top of them:

**During the tasks.** Task 6's `TestSearchShowsHostileTextAsText` passed
before the search existed, because it only checked that markup was absent.
It now also checks that each hostile string appears escaped and that each page
answers 200. It was seen to fail, then to pass.

**From looking at the demo archive beside the mockups (Task 15, Step 5).**
Nothing needed changing. Two differences remain on purpose:
- A connector's own settings sit in a row of their own under Name, Chats and
  State, instead of in the mockup's third column, because they are rendered
  from the connector's field list.
- The border of a message opened from a search is a darker gold than the
  mockup's yellow.

**From the whole-branch review.** No critical findings. Two were important,
and both are fixed in one commit:

| Finding | Change | Test |
|---|---|---|
| A login that finished while its source was being removed stored its session after the removal had deleted it. The source then answered 409 to everything, so the owner could not clear it | One lock per source (`Server.logins`) covers each login step, cancelling, signing out and removing, and the account is read after the lock is taken. Removal ends the login in progress first. `SetAccountEnabled` checks `removed_at` inside its `UPDATE` | `TestRemovingWaitsForALoginInFlight` |
| Data race on `flow.expires`: it was read under the flows' lock and written under the flow's own. `get` also closed flows while holding the map lock, even when a step was still running | `expires` is touched only under the flows' lock (`extend`). Expired flows are closed after the map is unlocked, under the flow's lock, so a step in flight finishes first. A request that was waiting on a closed flow starts again | `TestLoginsOfTwoSourcesAtOnce` (fails under `-race` without the change) |

Deferred minor findings, listed in STATUS:
- Unfinished logins are closed only when a later request looks at them, and
  none are closed at shutdown.
- The login page can be opened for a source that is running fine.
- "Add a source" creates the account switched on, then switches it off.
- A name made only of control characters gives a 500 and leaves an orphan
  account.
- Saving the languages card always rebuilds the index, even when the list is
  unchanged.
- A failed save of the language setting leaves a half-built index reported as
  current.
- Paging a conversation opened from filtered results drops the filters.
- The Sources page polls itself twice every 5 s, and each render counts every
  message in each chat.
- `Runner.SignOut` holds the restart lock while it signs out over the network.
- A refused login step loses its non-secret input.
- `<meta name="csrf">` is used only by tests.
