-- +migrate Dialect postgres
DROP INDEX IF EXISTS idx_conversations_ephemeral_expiry;
DROP INDEX IF EXISTS idx_conversations_user_source;
DROP INDEX IF EXISTS idx_conversations_user_ephemeral_history;
ALTER TABLE conversations DROP COLUMN IF EXISTS source_display_name;
ALTER TABLE conversations DROP COLUMN IF EXISTS source_document_id;
ALTER TABLE conversations DROP COLUMN IF EXISTS source_dataset_id;
ALTER TABLE conversations DROP COLUMN IF EXISTS source_type;
ALTER TABLE conversations DROP COLUMN IF EXISTS ephemeral_expires_at;
ALTER TABLE conversations DROP COLUMN IF EXISTS is_ephemeral;

-- +migrate Dialect sqlite
DROP INDEX IF EXISTS idx_conversations_ephemeral_expiry;
DROP INDEX IF EXISTS idx_conversations_user_source;
DROP INDEX IF EXISTS idx_conversations_user_ephemeral_history;
ALTER TABLE conversations DROP COLUMN source_display_name;
ALTER TABLE conversations DROP COLUMN source_document_id;
ALTER TABLE conversations DROP COLUMN source_dataset_id;
ALTER TABLE conversations DROP COLUMN source_type;
ALTER TABLE conversations DROP COLUMN ephemeral_expires_at;
ALTER TABLE conversations DROP COLUMN is_ephemeral;
