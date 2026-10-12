# arc5

A self-hosted message archive: Go, single binary, SQLite with FTS5,
server-rendered templates with Stimulus. Private GitHub repo `fenelon/arc5`,
default branch `master`.

Read `docs/superpowers/STATUS.md` before starting work. It says where each
milestone stands and what is still undecided.

## Standing instructions from the owner

- **Push without asking.** After committing or merging, push to GitHub as part
  of the same step and report it. This covers ordinary pushes only; a
  force-push or anything that rewrites remote history still needs an explicit
  yes. (The owner said "push without asking, continue" on 2026-10-12.)
- **Process.** Spec, then one plan per milestone, then native execution with
  `superpowers:executing-plans`: feature branch, test first for every task,
  one fresh whole-branch review at the end, fix important findings test-first,
  merge to `master` locally, push. Don't check in between tasks.
- **Interface.** The approved design is in
  `docs/superpowers/mockups/2026-10-13-interface/`. Dark mode is required. The
  owner rejected a plain grey admin look; follow the mockups rather than
  inventing a style.
- **Durability over speed** for the archive: `synchronous=FULL`, fsync files
  and directories.
- Self-destructing media capture is opt-in, and a connector must never call
  Telegram's `messages.readMessageContents`.

## Conventions

- No cgo. No inline styles or scripts in templates; pages are served under a
  strict content-security policy.
- `internal/archive` is the only package that writes to the database.
- Check before committing: `go vet ./... && go test -race -count=1 ./...`
- After a plan is executed, add a "Post-review changes" section to it and
  update `docs/superpowers/STATUS.md`.
