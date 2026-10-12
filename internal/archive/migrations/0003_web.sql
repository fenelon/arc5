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
