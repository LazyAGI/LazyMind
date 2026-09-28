-- +migrate Dialect postgres
DROP TABLE IF EXISTS desktop_notification_receipts;
DROP TABLE IF EXISTS task_notifications;
DROP TABLE IF EXISTS user_notification_preferences;
ALTER TABLE task_center_tasks DROP COLUMN notification_revision;
ALTER TABLE task_center_tasks DROP COLUMN notification_config;
ALTER TABLE user_schedules DROP COLUMN notification_revision;
ALTER TABLE user_schedules DROP COLUMN notification_config;

-- +migrate Dialect sqlite
DROP TABLE IF EXISTS desktop_notification_receipts;
DROP TABLE IF EXISTS task_notifications;
DROP TABLE IF EXISTS user_notification_preferences;
ALTER TABLE task_center_tasks DROP COLUMN notification_revision;
ALTER TABLE task_center_tasks DROP COLUMN notification_config;
ALTER TABLE user_schedules DROP COLUMN notification_revision;
ALTER TABLE user_schedules DROP COLUMN notification_config;
