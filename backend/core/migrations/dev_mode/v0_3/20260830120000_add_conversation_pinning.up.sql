-- +migrate Dialect postgres
ALTER TABLE conversations ADD COLUMN IF NOT EXISTS pinned_at TIMESTAMP NULL;
CREATE INDEX IF NOT EXISTS idx_conversations_user_pinned_history
    ON conversations(create_user_id, pinned_at DESC, updated_at DESC)
    WHERE deleted_at IS NULL AND archived_at IS NULL;

-- +migrate Dialect sqlite
ALTER TABLE conversations ADD COLUMN pinned_at DATETIME NULL;
CREATE INDEX IF NOT EXISTS idx_conversations_user_pinned_history
    ON conversations(create_user_id, pinned_at DESC, updated_at DESC)
    WHERE deleted_at IS NULL AND archived_at IS NULL;
