-- A chat's or sender's picture: the source's identity for it and its file in
-- the media store. The last picture kept stays when the source removes it.
ALTER TABLE chats ADD COLUMN avatar_id TEXT NOT NULL DEFAULT '';
ALTER TABLE chats ADD COLUMN avatar_sha256 TEXT NOT NULL DEFAULT '';
ALTER TABLE senders ADD COLUMN avatar_id TEXT NOT NULL DEFAULT '';
ALTER TABLE senders ADD COLUMN avatar_sha256 TEXT NOT NULL DEFAULT '';

-- The source's identity for an attachment's file, and when an edit replaced
-- it. A replaced attachment leaves its position for the new file: its
-- position becomes minus its ID.
ALTER TABLE attachments ADD COLUMN source_id TEXT NOT NULL DEFAULT '';
ALTER TABLE attachments ADD COLUMN replaced_at INTEGER;
CREATE INDEX avatars_by_chat_file ON chats (avatar_sha256) WHERE avatar_sha256 != '';
CREATE INDEX avatars_by_sender_file ON senders (avatar_sha256) WHERE avatar_sha256 != '';
