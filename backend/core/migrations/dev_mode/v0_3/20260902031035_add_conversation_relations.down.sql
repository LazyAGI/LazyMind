-- +migrate Dialect postgres
DROP INDEX IF EXISTS idx_conversations_parent_relation;
ALTER TABLE conversations DROP CONSTRAINT IF EXISTS chk_conversations_relation_type;
ALTER TABLE conversations DROP COLUMN IF EXISTS source_context;
ALTER TABLE conversations DROP COLUMN IF EXISTS source_selected_text;
ALTER TABLE conversations DROP COLUMN IF EXISTS source_seq;
ALTER TABLE conversations DROP COLUMN IF EXISTS source_history_id;
ALTER TABLE conversations DROP COLUMN IF EXISTS relation_type;
ALTER TABLE conversations DROP COLUMN IF EXISTS parent_conversation_id;

-- +migrate Dialect sqlite
DROP INDEX IF EXISTS idx_conversations_parent_relation;
ALTER TABLE conversations DROP COLUMN source_context;
ALTER TABLE conversations DROP COLUMN source_selected_text;
ALTER TABLE conversations DROP COLUMN source_seq;
ALTER TABLE conversations DROP COLUMN source_history_id;
ALTER TABLE conversations DROP COLUMN relation_type;
ALTER TABLE conversations DROP COLUMN parent_conversation_id;
