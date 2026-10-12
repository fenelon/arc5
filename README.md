# arc5

A self-hosted archive for your message history. It keeps a durable, searchable
copy of your chats so you can clean out accounts or use auto-delete without
losing anything.

**Status:** early development. The archive core and search engine run; the web
interface and the Telegram connector are not built yet, so there is nothing to
search except in tests. The design is in
[`docs/superpowers/specs`](docs/superpowers/specs).

## Run

```bash
go build -o arc5 ./cmd/arc5
./arc5 serve --data ./data
```

| Flag | Environment | Default | Meaning |
|---|---|---|---|
| `--listen` | `ARC5_LISTEN` | `127.0.0.1:8080` | Address to listen on |
| `--data` | `ARC5_DATA` | `data` | Folder for the database and media |

`GET /healthz` answers `{"status":"ok"}`.

The data folder holds everything arc5 knows, including connector sessions that
grant full access to your accounts. It is created with mode `0700`; keep it
that way.

## Search languages

Search finds other forms of a word (`cat` finds `cats`) in the languages you
choose. The default is English. Choose your own and rebuild the index with:

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

Restart a running server after changing the list, so it indexes new messages
the same way.

## Develop

```bash
go vet ./... && go test -race ./...
```
