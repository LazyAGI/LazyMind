-- +migrate Dialect postgres
CREATE TABLE IF NOT EXISTS user_selected_cloud_models (
    id BIGSERIAL PRIMARY KEY,
    user_id VARCHAR(255) NOT NULL,
    user_name VARCHAR(255) NOT NULL DEFAULT '',
    model_type VARCHAR(64) NOT NULL,
    public_model_key VARCHAR(96) NOT NULL,
    display_name_snapshot VARCHAR(128) NOT NULL,
    catalog_revision_snapshot VARCHAR(128),
    created_at TIMESTAMP WITH TIME ZONE NOT NULL,
    updated_at TIMESTAMP WITH TIME ZONE NOT NULL,
    CONSTRAINT uk_user_selected_cloud_models_user_type UNIQUE (user_id, model_type)
);
CREATE INDEX IF NOT EXISTS idx_user_selected_cloud_models_public_key
    ON user_selected_cloud_models (public_model_key);
ALTER TABLE conversations ADD COLUMN IF NOT EXISTS chat_model_source VARCHAR(16) NULL;
ALTER TABLE conversations ALTER COLUMN chat_model_id TYPE VARCHAR(128);

-- +migrate Dialect sqlite
CREATE TABLE IF NOT EXISTS user_selected_cloud_models (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    user_id VARCHAR(255) NOT NULL,
    user_name VARCHAR(255) NOT NULL DEFAULT '',
    model_type VARCHAR(64) NOT NULL,
    public_model_key VARCHAR(96) NOT NULL,
    display_name_snapshot VARCHAR(128) NOT NULL,
    catalog_revision_snapshot VARCHAR(128),
    created_at DATETIME NOT NULL,
    updated_at DATETIME NOT NULL,
    UNIQUE (user_id, model_type)
);
CREATE INDEX IF NOT EXISTS idx_user_selected_cloud_models_public_key
    ON user_selected_cloud_models (public_model_key);
ALTER TABLE conversations ADD COLUMN chat_model_source VARCHAR(16) NULL;
