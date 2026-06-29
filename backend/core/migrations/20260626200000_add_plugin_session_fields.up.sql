-- Add intent_context column to plugin_sessions
ALTER TABLE plugin_sessions
    ADD COLUMN IF NOT EXISTS intent_context TEXT NOT NULL DEFAULT '{}';
