# arc5

A self-hosted archive for your message history. It keeps a durable, searchable
copy of your chats so you can clean out accounts or use auto-delete without
losing anything.

**Status:** early development. The archive, the search engine, the web
interface, the Telegram connector and the email (IMAP) connector run. Both
connectors have been tested against simulated servers only; neither has yet
been run against a real account. The design is in [`docs/superpowers/specs`](docs/superpowers/specs).

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

## Run in a container

The `Dockerfile` builds a small image that runs `arc5 serve` on port 8080,
keeps the archive in `/data`, and checks `/healthz`. arc5 needs no cgo, so an
image for another machine builds quickly on this one:

```sh
docker build --platform linux/amd64 -t arc5:latest .   # linux/arm64 for ARM
docker save arc5:latest -o dist/arc5-amd64.tar
```

**On a Synology NAS** with Container Manager:

1. Container Manager > Image > Add > Add From File: choose
   `dist/arc5-amd64.tar`. Most Synology models are amd64; for an ARM model,
   build with `--platform linux/arm64`.
2. In File Station, make a folder for the project, for example `docker/arc5`.
3. Container Manager > Project > Create: name it `arc5`, choose that folder,
   and upload [`deploy/nas/compose.yaml`](deploy/nas/compose.yaml). Set `TZ`
   in it to your time zone first. The archive goes in the folder's `data`
   subfolder, where Hyper Backup can include it.
4. Open `http://<nas>:8080`, for example over Tailscale, and choose a
   password.

To update, import a newly built image the same way, then stop the project and
build it again so the container is made from the new image. The archive in
`data` stays.

The compose file runs arc5 as root inside the container, because DSM's access
lists on shared folders may refuse a user DSM does not know. It describes how
to run as a DSM user instead.

## Use

- **Search** with the box at the top. It covers the source chosen in the
  picker, or every source of that kind under "Everything at once". Words are
  ANDed; `"quoted words"` match as a phrase; the last word also matches as the
  start of a longer one. Results list matching chats and people first, then
  messages, best match first or newest first, with filters for attachments,
  messages deleted at the source, joins and calls, and dates.
- **Sources** lists every account arc5 keeps a copy of. Choose one to rename
  it, choose which of its chats are archived, change its settings, sign it
  out, or remove it. Removing a source stops it and forgets its login; its
  messages stay in the archive. "Add a source" runs the source's login.
- **Settings** has the theme, the search languages, the largest attachment
  downloaded automatically, and the password.

The demo source can be signed out and logged in again from the Sources page;
its login code is 12345.

## Archiving Telegram

arc5 signs in to Telegram as you, the way a desktop app does, and keeps a copy
of your chats. It never sends, edits or deletes anything.

1. Create API keys of your own at <https://my.telegram.org>, under "API
   development tools". The form answers only "ERROR" when it dislikes
   something: use a title of letters only (such as `ArcFive`; `Arc5` was
   refused), choose Desktop, leave the URL empty, and turn off any VPN.
2. In arc5, open Sources, choose "Add a source", then Telegram. Enter the API
   ID and hash, your phone number, the code Telegram sends, and your two-step
   password if you have one.
3. arc5 then reads your chat list, fetches history (newest chats first) and
   saves new messages, edits and deletions as they happen. Its Sources panel
   says how far the catch-up has got.

What is kept: private chats and groups by default; channels and bots when you
switch them on under "Choose which chats are archived". Edits keep the earlier
text, and media an edit replaces. A message deleted in Telegram stays, marked
as deleted. Photos and files up to the attachment limit are downloaded; larger
ones can be fetched by hand. Chat and people pictures are kept too.

Each Telegram source's settings: how far back to fetch history, how fast, a
pause switch for the catch-up, and whether to keep self-destructing and
view-once media. That last one is off by default: the sender expected the
media to disappear. arc5 downloads it without reporting it as opened, which
by Telegram's documentation should leave it unopened for the sender. This
has not yet been confirmed with real accounts.

arc5 appears in Telegram's list of active sessions as "arc5 archive". Ending
that session from another device, or "Sign this source out" in arc5, stops
the archiving until you log in again. Telegram cannot show arc5 secret chats,
and nothing deleted before arc5 first saw it can be recovered.

## Archiving email

arc5 reads a mailbox over IMAP, the way a mail program does, and keeps a copy
of every message in the folders you choose. It never sends, moves, deletes or
flags mail, and never marks it as read.

1. In arc5, open Sources, choose "Add a source", then Email, and type the
   address. For Gmail, Fastmail, iCloud, Yahoo, GMX, mailbox.org, Yandex and
   Zoho the server is filled in; for other addresses check it.
2. Type the password. Most providers want an **app password** for mail
   programs rather than your usual one:
   - **Gmail**: turn on 2-Step Verification for the Google account, then
     create an app password at <https://myaccount.google.com/apppasswords>
     and paste its 16 letters. A Google Workspace account works only if its
     administrator allows app passwords and IMAP.
   - iCloud, Yahoo, Fastmail and Yandex: create one in the account's security
     settings; the login page says where.
3. arc5 tests the login, then lists your folders and fetches mail, newest
   first, while new mail keeps arriving within about a minute.

Microsoft mailboxes (Outlook.com, Hotmail, Microsoft 365) no longer accept
passwords from mail programs, so they cannot be added yet. Servers whose
certificate does not verify are refused.

What is kept: the Inbox, Sent and your own folders by default; Drafts, Junk
and Trash when you switch them on under "Choose which folders are archived".
On Gmail, whose All Mail folder holds every message once, only the Inbox,
Sent and All Mail are archived by default, and each message is downloaded
once however many labels it has. Every message keeps its original, which
"Download original (.eml)" gives back. Mail sent only as HTML is turned into
text; HTML is never shown. Attachments up to the attachment limit are kept as
files to open; larger ones stay inside the original and "Fetch now" takes them
out. A message deleted on the server stays, marked as deleted at source, once
it has left every archived folder it was in; moving it between folders is not
deletion.

Each email source's settings: how far back to fetch mail, how fast, a pause
switch for the catch-up, and your other addresses, so mail you sent from them
counts as yours.

## Search languages

Search finds other forms of a word (`cat` finds `cats`) in the languages you
choose under Settings, which rebuilds the index in the background. The
default is English. The same can be done from the command line, with the
server stopped:

```bash
./arc5 reindex --data ./data --languages en,ru,es
```

Word forms are available for Arabic (`ar`), Danish (`da`), Dutch (`nl`),
English (`en`), Finnish (`fi`), French (`fr`), German (`de`), Hungarian (`hu`),
Irish (`ga`), Italian (`it`), Norwegian (`no`), Portuguese (`pt`), Romanian
(`ro`), Russian (`ru`), Spanish (`es`), Swedish (`sv`), Tamil (`ta`) and
Turkish (`tr`). Every other language is still searchable by exact word, by
prefix and by phrase, and Chinese, Japanese and Thai are matched by character
sequence. List only the languages you write in: each extra one that shares an
alphabet makes the index larger and matches looser.

A server running while `arc5 reindex` changes the list keeps its old list
until it is restarted; changing it under Settings needs no restart.

## Develop

```bash
go vet ./... && go test -race ./...
```
