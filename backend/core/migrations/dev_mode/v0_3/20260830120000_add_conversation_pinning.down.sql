-- +migrate Dialect postgres
DROP INDEX IF EXISTS idx_conversations_user_pinned_history;
ALTER TABLE conversations DROP COLUMN IF EXISTS pinned_at;

-- +migrate Dialect sqlite
DROP INDEX IF EXISTS idx_conversations_user_pinned_history;
ALTER TABLE conversations DROP COLUMN pinned_at;
