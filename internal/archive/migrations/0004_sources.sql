-- When the owner removed the source. A removed account is switched off and
-- its connector's session is gone, but its messages stay in the archive.
ALTER TABLE accounts ADD COLUMN removed_at INTEGER;
