-- 20260730100000_add_plugin_call_mode
-- +migrate Up
-- +migrate Dialect postgres
ALTER TABLE user_plugin_settings
    ADD COLUMN IF NOT EXISTS call_mode VARCHAR(16) NOT NULL DEFAULT 'disabled';

UPDATE user_plugin_settings
SET call_mode = CASE WHEN enabled THEN 'auto' ELSE 'disabled' END
WHERE call_mode = 'disabled';

-- +migrate Dialect sqlite
ALTER TABLE user_plugin_settings ADD COLUMN call_mode varchar(16) NOT NULL DEFAULT 'disabled';
UPDATE user_plugin_settings
SET call_mode = CASE WHEN enabled THEN 'auto' ELSE 'disabled' END
WHERE call_mode = 'disabled';
