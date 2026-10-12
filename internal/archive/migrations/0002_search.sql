-- The search index. Contentless: it holds only terms, because the text is in
-- messages. rowid is messages.id. The ascii tokenizer splits on spaces only,
-- since the analyzer in package search has already produced the tokens.
CREATE VIRTUAL TABLE messages_fts USING fts5(
  exact,
  stem,
  content = '',
  contentless_delete = 1,
  tokenize = 'ascii'
);

-- Listing messages newest first across all chats (browsing with filters but
-- no search text) walks this index instead of sorting the whole table.
CREATE INDEX messages_sent ON messages (sent_at, id);
