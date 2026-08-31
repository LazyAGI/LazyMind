-- 20260825022749_add_schedules_feature_control
-- +migrate Up
-- +migrate Dialect postgres
ALTER TABLE user_ui_preferences
    ADD COLUMN IF NOT EXISTS schedules_enabled BOOLEAN NOT NULL DEFAULT TRUE;
UPDATE user_ui_preferences SET schedules_enabled = task_center_enabled;

-- +migrate Dialect sqlite
ALTER TABLE user_ui_preferences ADD COLUMN schedules_enabled BOOLEAN NOT NULL DEFAULT TRUE;
UPDATE user_ui_preferences SET schedules_enabled = task_center_enabled;
