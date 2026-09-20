-- +migrate Dialect postgres
DROP INDEX IF EXISTS idx_user_selected_cloud_models_public_key;
DROP TABLE IF EXISTS user_selected_cloud_models;
ALTER TABLE conversations DROP COLUMN IF EXISTS chat_model_source;
ALTER TABLE conversations ALTER COLUMN chat_model_id TYPE VARCHAR(64);

-- +migrate Dialect sqlite
DROP INDEX IF EXISTS idx_user_selected_cloud_models_public_key;
DROP TABLE IF EXISTS user_selected_cloud_models;
ALTER TABLE conversations DROP COLUMN chat_model_source;
