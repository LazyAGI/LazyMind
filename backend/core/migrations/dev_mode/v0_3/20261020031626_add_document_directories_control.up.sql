-- +migrate Up
-- +migrate Dialect postgres
ALTER TABLE user_ui_preferences ADD COLUMN IF NOT EXISTS document_directories_enabled BOOLEAN NOT NULL DEFAULT TRUE;
ALTER TABLE user_ui_preferences ADD COLUMN IF NOT EXISTS document_directories_generation BIGINT NOT NULL DEFAULT 0;

-- +migrate Dialect sqlite
ALTER TABLE user_ui_preferences ADD COLUMN document_directories_enabled BOOLEAN NOT NULL DEFAULT TRUE;
ALTER TABLE user_ui_preferences ADD COLUMN document_directories_generation BIGINT NOT NULL DEFAULT 0;
