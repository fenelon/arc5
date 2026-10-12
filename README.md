# arc5

A self-hosted archive for your message history. It keeps a durable, searchable
copy of your chats so you can clean out accounts or use auto-delete without
losing anything.

**Status:** early development. The archive core runs; search, the web
interface and the Telegram connector are not built yet. The design is in
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

## Develop

```bash
go vet ./... && go test -race ./...
```
