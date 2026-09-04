-- 20260831080000_add_performance_stats_preference
-- +migrate Up
-- +migrate Dialect postgres
ALTER TABLE user_ui_preferences
    ADD COLUMN IF NOT EXISTS performance_stats_enabled BOOLEAN NOT NULL DEFAULT FALSE;

-- +migrate Dialect sqlite
ALTER TABLE user_ui_preferences ADD COLUMN performance_stats_enabled BOOLEAN NOT NULL DEFAULT FALSE;
