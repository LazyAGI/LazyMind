-- 20260903093000_add_sensitive_word_filter_enabled
-- +migrate Down
-- +migrate Dialect postgres
ALTER TABLE user_ui_preferences
    DROP COLUMN IF EXISTS sensitive_word_filter_enabled;

-- +migrate Dialect sqlite
ALTER TABLE user_ui_preferences DROP COLUMN sensitive_word_filter_enabled;
