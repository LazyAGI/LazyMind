-- +migrate Dialect postgres
ALTER TABLE conversations ADD COLUMN IF NOT EXISTS is_ephemeral BOOLEAN NOT NULL DEFAULT FALSE;
ALTER TABLE conversations ADD COLUMN IF NOT EXISTS ephemeral_expires_at TIMESTAMP NULL;
ALTER TABLE conversations ADD COLUMN IF NOT EXISTS source_type VARCHAR(32) NOT NULL DEFAULT '';
ALTER TABLE conversations ADD COLUMN IF NOT EXISTS source_dataset_id VARCHAR(255) NOT NULL DEFAULT '';
ALTER TABLE conversations ADD COLUMN IF NOT EXISTS source_document_id VARCHAR(255) NOT NULL DEFAULT '';
ALTER TABLE conversations ADD COLUMN IF NOT EXISTS source_display_name VARCHAR(255) NOT NULL DEFAULT '';
UPDATE conversations
SET is_ephemeral = TRUE,
    ephemeral_expires_at = CURRENT_TIMESTAMP + INTERVAL '1 day'
WHERE ext IS NOT NULL AND CAST(ext AS TEXT) LIKE '%"ephemeral":true%';
CREATE INDEX IF NOT EXISTS idx_conversations_user_ephemeral_history
    ON conversations(create_user_id, is_ephemeral, deleted_at, archived_at, is_task_conv, updated_at);
CREATE INDEX IF NOT EXISTS idx_conversations_user_source
    ON conversations(create_user_id, source_type, source_document_id, is_ephemeral, updated_at);
CREATE INDEX IF NOT EXISTS idx_conversations_ephemeral_expiry
    ON conversations(is_ephemeral, ephemeral_expires_at)
    WHERE is_ephemeral = TRUE;

-- +migrate Dialect sqlite
ALTER TABLE conversations ADD COLUMN is_ephemeral NUMERIC NOT NULL DEFAULT FALSE;
ALTER TABLE conversations ADD COLUMN ephemeral_expires_at DATETIME NULL;
ALTER TABLE conversations ADD COLUMN source_type VARCHAR(32) NOT NULL DEFAULT '';
ALTER TABLE conversations ADD COLUMN source_dataset_id VARCHAR(255) NOT NULL DEFAULT '';
ALTER TABLE conversations ADD COLUMN source_document_id VARCHAR(255) NOT NULL DEFAULT '';
ALTER TABLE conversations ADD COLUMN source_display_name VARCHAR(255) NOT NULL DEFAULT '';
UPDATE conversations
SET is_ephemeral = TRUE,
    ephemeral_expires_at = datetime('now', '+1 day')
WHERE ext IS NOT NULL AND CAST(ext AS TEXT) LIKE '%"ephemeral":true%';
CREATE INDEX IF NOT EXISTS idx_conversations_user_ephemeral_history
    ON conversations(create_user_id, is_ephemeral, deleted_at, archived_at, is_task_conv, updated_at);
CREATE INDEX IF NOT EXISTS idx_conversations_user_source
    ON conversations(create_user_id, source_type, source_document_id, is_ephemeral, updated_at);
CREATE INDEX IF NOT EXISTS idx_conversations_ephemeral_expiry
    ON conversations(is_ephemeral, ephemeral_expires_at)
    WHERE is_ephemeral = TRUE;
