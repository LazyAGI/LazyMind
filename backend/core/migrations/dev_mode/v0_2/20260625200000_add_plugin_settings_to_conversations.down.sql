-- 20260625200000_add_plugin_settings_to_conversations
-- +migrate Down
-- +migrate Dialect postgres

ALTER TABLE conversations
    DROP COLUMN IF EXISTS enable_plugin,
    DROP COLUMN IF EXISTS plugin_mode,
    DROP COLUMN IF EXISTS enable_subagent;

-- +migrate Dialect sqlite
SELECT 1; -- Historical SQLite change is included by the first v0.2 dev migration.
