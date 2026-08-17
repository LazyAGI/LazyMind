-- 20260811153000_add_workflows_feature_control
-- +migrate Up
-- +migrate Dialect postgres
ALTER TABLE user_ui_preferences
    ADD COLUMN IF NOT EXISTS workflows_enabled BOOLEAN NOT NULL DEFAULT TRUE;
UPDATE user_ui_preferences SET workflows_enabled = skills_enabled;

-- +migrate Dialect sqlite
ALTER TABLE user_ui_preferences ADD COLUMN workflows_enabled BOOLEAN NOT NULL DEFAULT TRUE;
UPDATE user_ui_preferences SET workflows_enabled = skills_enabled;
