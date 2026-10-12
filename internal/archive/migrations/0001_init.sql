CREATE TABLE accounts (
  id         INTEGER PRIMARY KEY,
  connector  TEXT    NOT NULL,
  label      TEXT    NOT NULL,
  settings   TEXT    NOT NULL DEFAULT '{}',
  enabled    INTEGER NOT NULL DEFAULT 1,
  created_at INTEGER NOT NULL
);

CREATE TABLE chats (
  id         INTEGER PRIMARY KEY,
  account_id INTEGER NOT NULL REFERENCES accounts(id),
  source_id  TEXT    NOT NULL,
  kind       TEXT    NOT NULL CHECK (kind IN ('private', 'group', 'channel', 'bot')),
  title      TEXT    NOT NULL,
  archived   INTEGER NOT NULL,
  created_at INTEGER NOT NULL,
  UNIQUE (account_id, source_id)
);

CREATE TABLE senders (
  id           INTEGER PRIMARY KEY,
  account_id   INTEGER NOT NULL REFERENCES accounts(id),
  source_id    TEXT    NOT NULL,
  display_name TEXT    NOT NULL DEFAULT '',
  username     TEXT    NOT NULL DEFAULT '',
  UNIQUE (account_id, source_id)
);

CREATE TABLE messages (
  id                 INTEGER PRIMARY KEY,
  chat_id            INTEGER NOT NULL REFERENCES chats(id),
  source_id          TEXT    NOT NULL,
  sender_id          INTEGER REFERENCES senders(id),
  kind               TEXT    NOT NULL CHECK (kind IN ('message', 'service')),
  sent_at            INTEGER NOT NULL,
  text               TEXT    NOT NULL,
  reply_to_source_id TEXT    NOT NULL DEFAULT '',
  forwarded_from     TEXT    NOT NULL DEFAULT '',
  edited_at          INTEGER,
  edit_dated         INTEGER NOT NULL DEFAULT 0, -- 1 when edited_at came from the source, 0 when it is our capture time
  deleted_at         INTEGER,
  captured_at        INTEGER NOT NULL,
  raw                BLOB,
  UNIQUE (chat_id, source_id)
);
CREATE INDEX messages_chat_sent ON messages (chat_id, sent_at, id);
CREATE INDEX messages_source ON messages (source_id);

CREATE TABLE message_versions (
  id          INTEGER PRIMARY KEY,
  message_id  INTEGER NOT NULL REFERENCES messages(id),
  text        TEXT    NOT NULL,
  replaced_at INTEGER NOT NULL
);
CREATE INDEX message_versions_message ON message_versions (message_id);

CREATE TABLE attachments (
  id         INTEGER PRIMARY KEY,
  message_id INTEGER NOT NULL REFERENCES messages(id),
  position   INTEGER NOT NULL,
  kind       TEXT    NOT NULL CHECK (kind IN ('photo', 'video', 'audio', 'voice', 'document', 'sticker', 'other')),
  file_name  TEXT    NOT NULL DEFAULT '',
  mime_type  TEXT    NOT NULL DEFAULT '',
  size       INTEGER NOT NULL DEFAULT 0,
  sha256     TEXT    NOT NULL DEFAULT '',
  status     TEXT    NOT NULL CHECK (status IN ('pending', 'stored', 'skipped', 'failed', 'unavailable')),
  reason     TEXT    NOT NULL DEFAULT '',
  ephemeral  INTEGER NOT NULL DEFAULT 0,
  requested  INTEGER NOT NULL DEFAULT 0,
  UNIQUE (message_id, position)
);
CREATE INDEX attachments_requested ON attachments (requested) WHERE requested = 1;

CREATE TABLE connector_state (
  account_id INTEGER NOT NULL REFERENCES accounts(id),
  key        TEXT    NOT NULL,
  value      BLOB    NOT NULL,
  PRIMARY KEY (account_id, key)
);

CREATE TABLE settings (
  key   TEXT PRIMARY KEY,
  value TEXT NOT NULL
);
