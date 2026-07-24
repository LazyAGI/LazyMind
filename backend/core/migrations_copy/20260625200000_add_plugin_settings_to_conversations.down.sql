-- 20260625200000_add_plugin_settings_to_conversations
-- +migrate Down

ALTER TABLE conversations
    DROP COLUMN IF EXISTS enable_plugin,
    DROP COLUMN IF EXISTS plugin_mode,
    DROP COLUMN IF EXISTS enable_subagent;
