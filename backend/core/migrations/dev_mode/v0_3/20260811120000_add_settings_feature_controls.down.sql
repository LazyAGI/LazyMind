-- 20260811120000_add_settings_feature_controls
-- +migrate Down
-- +migrate Dialect postgres
ALTER TABLE user_ui_preferences
    DROP COLUMN IF EXISTS task_center_enabled;
ALTER TABLE user_ui_preferences
    DROP COLUMN IF EXISTS skills_enabled;
ALTER TABLE user_ui_preferences
    DROP COLUMN IF EXISTS mcp_enabled;

-- +migrate Dialect sqlite
ALTER TABLE user_ui_preferences DROP COLUMN task_center_enabled;
ALTER TABLE user_ui_preferences DROP COLUMN skills_enabled;
ALTER TABLE user_ui_preferences DROP COLUMN mcp_enabled;
