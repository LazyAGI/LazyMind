-- 20260811153000_add_workflows_feature_control
-- +migrate Down
-- +migrate Dialect postgres
ALTER TABLE user_ui_preferences DROP COLUMN IF EXISTS workflows_enabled;

-- +migrate Dialect sqlite
ALTER TABLE user_ui_preferences DROP COLUMN workflows_enabled;
