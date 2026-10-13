-- arc5: foreign keys off
-- Mail. SQLite cannot change a CHECK constraint in place, so chats is rebuilt
-- with 'thread' among its kinds: same columns, same IDs, same index. The
-- migration runs with foreign keys off and is checked before it commits.
CREATE TABLE chats_new (
  id            INTEGER PRIMARY KEY,
  account_id    INTEGER NOT NULL REFERENCES accounts(id),
  source_id     TEXT    NOT NULL,
  kind          TEXT    NOT NULL CHECK (kind IN ('private', 'group', 'channel', 'bot', 'thread')),
  title         TEXT    NOT NULL,
  archived      INTEGER NOT NULL,
  created_at    INTEGER NOT NULL,
  avatar_id     TEXT    NOT NULL DEFAULT '',
  avatar_sha256 TEXT    NOT NULL DEFAULT '',
  UNIQUE (account_id, source_id)
);
INSERT INTO chats_new (id, account_id, source_id, kind, title, archived, created_at, avatar_id, avatar_sha256)
  SELECT id, account_id, source_id, kind, title, archived, created_at, avatar_id, avatar_sha256 FROM chats;
DROP TABLE chats;
ALTER TABLE chats_new RENAME TO chats;
CREATE INDEX avatars_by_chat_file ON chats (avatar_sha256) WHERE avatar_sha256 != '';

-- A mailbox's own address.
ALTER TABLE accounts ADD COLUMN address TEXT NOT NULL DEFAULT '';

-- A mail's subject, and the message as received, kept in the media store.
ALTER TABLE messages ADD COLUMN subject TEXT NOT NULL DEFAULT '';
ALTER TABLE messages ADD COLUMN original_sha256 TEXT NOT NULL DEFAULT '';

CREATE TABLE message_addresses (
  message_id INTEGER NOT NULL REFERENCES messages(id),
  role       TEXT    NOT NULL CHECK (role IN ('from', 'to', 'cc', 'bcc', 'reply_to')),
  position   INTEGER NOT NULL,
  address    TEXT    NOT NULL,
  name       TEXT    NOT NULL DEFAULT '',
  PRIMARY KEY (message_id, role, position)
);
CREATE INDEX message_addresses_address ON message_addresses (address);

-- A mailbox's folders, and which messages were seen in each. gone_at is
-- when a message left the folder at the source; a message gone from every
-- folder it was seen in is deleted at source.
CREATE TABLE folders (
  id         INTEGER PRIMARY KEY,
  account_id INTEGER NOT NULL REFERENCES accounts(id),
  source_id  TEXT    NOT NULL,
  name       TEXT    NOT NULL,
  role       TEXT    NOT NULL CHECK (role IN ('inbox', 'sent', 'all', 'drafts', 'junk', 'trash', 'flagged', 'important', 'other')),
  archived   INTEGER NOT NULL,
  created_at INTEGER NOT NULL,
  UNIQUE (account_id, source_id)
);

CREATE TABLE message_folders (
  message_id INTEGER NOT NULL REFERENCES messages(id),
  folder_id  INTEGER NOT NULL REFERENCES folders(id),
  uid        INTEGER NOT NULL DEFAULT 0,
  gone_at    INTEGER,
  PRIMARY KEY (message_id, folder_id)
);
CREATE INDEX message_folders_folder ON message_folders (folder_id, uid);
