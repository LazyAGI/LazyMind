-- 20260730100000_add_plugin_call_mode
-- +migrate Down
-- +migrate Dialect postgres
ALTER TABLE user_plugin_settings DROP COLUMN IF EXISTS call_mode;

-- +migrate Dialect sqlite
ALTER TABLE user_plugin_settings DROP COLUMN call_mode;
