-- +migrate Dialect postgres
ALTER TABLE conversations DROP COLUMN IF EXISTS chat_model_version;
ALTER TABLE conversations DROP COLUMN IF EXISTS chat_model_snapshot;
ALTER TABLE conversations DROP COLUMN IF EXISTS chat_model_id;
ALTER TABLE conversations DROP COLUMN IF EXISTS chat_model_mode;

-- +migrate Dialect sqlite
ALTER TABLE conversations DROP COLUMN chat_model_version;
ALTER TABLE conversations DROP COLUMN chat_model_snapshot;
ALTER TABLE conversations DROP COLUMN chat_model_id;
ALTER TABLE conversations DROP COLUMN chat_model_mode;
