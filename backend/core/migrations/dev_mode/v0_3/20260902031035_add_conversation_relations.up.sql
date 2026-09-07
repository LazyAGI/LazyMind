-- +migrate Dialect postgres
ALTER TABLE conversations ADD COLUMN IF NOT EXISTS parent_conversation_id VARCHAR(36) NULL;
ALTER TABLE conversations ADD COLUMN IF NOT EXISTS relation_type VARCHAR(16) NOT NULL DEFAULT '';
ALTER TABLE conversations ADD COLUMN IF NOT EXISTS source_history_id VARCHAR(36) NULL;
ALTER TABLE conversations ADD COLUMN IF NOT EXISTS source_seq INTEGER NULL;
ALTER TABLE conversations ADD COLUMN IF NOT EXISTS source_selected_text TEXT NOT NULL DEFAULT '';
ALTER TABLE conversations ADD COLUMN IF NOT EXISTS source_context JSON NULL;
ALTER TABLE conversations DROP CONSTRAINT IF EXISTS chk_conversations_relation_type;
ALTER TABLE conversations ADD CONSTRAINT chk_conversations_relation_type
    CHECK (relation_type IN ('', 'sidechat', 'fork'));
CREATE INDEX IF NOT EXISTS idx_conversations_parent_relation
    ON conversations(create_user_id, parent_conversation_id, relation_type, updated_at);

-- +migrate Dialect sqlite
ALTER TABLE conversations ADD COLUMN parent_conversation_id VARCHAR(36) NULL;
ALTER TABLE conversations ADD COLUMN relation_type VARCHAR(16) NOT NULL DEFAULT ''
    CHECK (relation_type IN ('', 'sidechat', 'fork'));
ALTER TABLE conversations ADD COLUMN source_history_id VARCHAR(36) NULL;
ALTER TABLE conversations ADD COLUMN source_seq INTEGER NULL;
ALTER TABLE conversations ADD COLUMN source_selected_text TEXT NOT NULL DEFAULT '';
ALTER TABLE conversations ADD COLUMN source_context JSON NULL;
CREATE INDEX IF NOT EXISTS idx_conversations_parent_relation
    ON conversations(create_user_id, parent_conversation_id, relation_type, updated_at);
