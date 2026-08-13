-- 20260811173000_add_document_parsing_feature_control
-- +migrate Down
-- +migrate Dialect postgres
ALTER TABLE user_ui_preferences
    DROP COLUMN IF EXISTS document_parsing_enabled;

-- +migrate Dialect sqlite
ALTER TABLE user_ui_preferences DROP COLUMN document_parsing_enabled;
