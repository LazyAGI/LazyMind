-- +migrate Down
-- +migrate Dialect postgres
ALTER TABLE user_ui_preferences DROP COLUMN IF EXISTS document_directories_enabled;
ALTER TABLE user_ui_preferences DROP COLUMN IF EXISTS document_directories_generation;

-- +migrate Dialect sqlite
ALTER TABLE user_ui_preferences DROP COLUMN document_directories_enabled;
ALTER TABLE user_ui_preferences DROP COLUMN document_directories_generation;
