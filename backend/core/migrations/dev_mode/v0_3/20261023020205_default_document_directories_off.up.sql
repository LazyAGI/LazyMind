-- +migrate Up
-- +migrate Dialect postgres
ALTER TABLE user_ui_preferences ALTER COLUMN document_directories_enabled SET DEFAULT FALSE;

-- +migrate Dialect sqlite
ALTER TABLE user_ui_preferences RENAME COLUMN document_directories_enabled TO document_directories_enabled_previous_default;
ALTER TABLE user_ui_preferences ADD COLUMN document_directories_enabled BOOLEAN NOT NULL DEFAULT FALSE;
UPDATE user_ui_preferences SET document_directories_enabled = document_directories_enabled_previous_default;
ALTER TABLE user_ui_preferences DROP COLUMN document_directories_enabled_previous_default;
