-- +migrate Dialect postgres
ALTER TABLE conversations ADD COLUMN IF NOT EXISTS chat_model_mode VARCHAR(16) NULL;
ALTER TABLE conversations ADD COLUMN IF NOT EXISTS chat_model_id VARCHAR(64) NULL;
ALTER TABLE conversations ADD COLUMN IF NOT EXISTS chat_model_snapshot JSON NULL;
ALTER TABLE conversations ADD COLUMN IF NOT EXISTS chat_model_version BIGINT NOT NULL DEFAULT 0;

-- +migrate Dialect sqlite
ALTER TABLE conversations ADD COLUMN chat_model_mode VARCHAR(16) NULL;
ALTER TABLE conversations ADD COLUMN chat_model_id VARCHAR(64) NULL;
ALTER TABLE conversations ADD COLUMN chat_model_snapshot JSON NULL;
ALTER TABLE conversations ADD COLUMN chat_model_version INTEGER NOT NULL DEFAULT 0;
