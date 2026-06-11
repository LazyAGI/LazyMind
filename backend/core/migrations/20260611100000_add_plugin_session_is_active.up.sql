-- Add is_active flag to plugin_sessions so that completed sessions are not
-- mistakenly picked up by GetActivePluginSession on subsequent chat turns.
-- Existing rows are treated as active (NULL is coerced to TRUE via the DEFAULT).
ALTER TABLE plugin_sessions
    ADD COLUMN IF NOT EXISTS is_active BOOLEAN NOT NULL DEFAULT TRUE;

CREATE INDEX IF NOT EXISTS idx_plugin_sessions_active
    ON plugin_sessions(conversation_id, is_active);
