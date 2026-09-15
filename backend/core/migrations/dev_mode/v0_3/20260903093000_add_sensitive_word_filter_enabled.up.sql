-- 20260903093000_add_sensitive_word_filter_enabled
-- +migrate Up
-- +migrate Dialect postgres
ALTER TABLE user_ui_preferences
    ADD COLUMN IF NOT EXISTS sensitive_word_filter_enabled BOOLEAN NOT NULL DEFAULT FALSE;

-- +migrate Dialect sqlite
ALTER TABLE user_ui_preferences ADD COLUMN sensitive_word_filter_enabled BOOLEAN NOT NULL DEFAULT FALSE;
