-- 20260625200000_add_plugin_settings_to_conversations
-- +migrate Up
-- +migrate Dialect postgres

ALTER TABLE conversations
    ADD COLUMN IF NOT EXISTS enable_plugin   BOOLEAN       DEFAULT NULL,
    ADD COLUMN IF NOT EXISTS plugin_mode     VARCHAR(16)   DEFAULT NULL,
    ADD COLUMN IF NOT EXISTS enable_subagent BOOLEAN       DEFAULT NULL;

-- +migrate Dialect sqlite
SELECT 1; -- Historical SQLite change is included by the first v0.2 dev migration.
