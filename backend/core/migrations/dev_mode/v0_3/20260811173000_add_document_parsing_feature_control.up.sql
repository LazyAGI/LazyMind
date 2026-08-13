-- 20260811173000_add_document_parsing_feature_control
-- +migrate Up
-- +migrate Dialect postgres
ALTER TABLE user_ui_preferences
    ADD COLUMN IF NOT EXISTS document_parsing_enabled BOOLEAN NOT NULL DEFAULT TRUE;

-- +migrate Dialect sqlite
ALTER TABLE user_ui_preferences ADD COLUMN document_parsing_enabled BOOLEAN NOT NULL DEFAULT TRUE;
