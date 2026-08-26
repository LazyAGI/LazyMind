-- 20260825022749_add_schedules_feature_control
-- +migrate Down
-- +migrate Dialect postgres
ALTER TABLE user_ui_preferences DROP COLUMN IF EXISTS schedules_enabled;

-- +migrate Dialect sqlite
ALTER TABLE user_ui_preferences DROP COLUMN schedules_enabled;
