-- 20260811120000_add_settings_feature_controls
-- +migrate Up
-- +migrate Dialect postgres
ALTER TABLE user_ui_preferences
    ADD COLUMN IF NOT EXISTS task_center_enabled BOOLEAN NOT NULL DEFAULT TRUE;
ALTER TABLE user_ui_preferences
    ADD COLUMN IF NOT EXISTS skills_enabled BOOLEAN NOT NULL DEFAULT TRUE;
ALTER TABLE user_ui_preferences
    ADD COLUMN IF NOT EXISTS mcp_enabled BOOLEAN NOT NULL DEFAULT TRUE;

-- +migrate Dialect sqlite
ALTER TABLE user_ui_preferences ADD COLUMN task_center_enabled BOOLEAN NOT NULL DEFAULT TRUE;
ALTER TABLE user_ui_preferences ADD COLUMN skills_enabled BOOLEAN NOT NULL DEFAULT TRUE;
ALTER TABLE user_ui_preferences ADD COLUMN mcp_enabled BOOLEAN NOT NULL DEFAULT TRUE;
