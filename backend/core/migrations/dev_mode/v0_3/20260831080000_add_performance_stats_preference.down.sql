-- 20260831080000_add_performance_stats_preference
-- +migrate Down
-- +migrate Dialect postgres
ALTER TABLE user_ui_preferences
    DROP COLUMN IF EXISTS performance_stats_enabled;

-- +migrate Dialect sqlite
ALTER TABLE user_ui_preferences DROP COLUMN performance_stats_enabled;
