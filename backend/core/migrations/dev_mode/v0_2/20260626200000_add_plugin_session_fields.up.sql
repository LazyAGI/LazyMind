-- +migrate Dialect postgres
-- Add intent_context column to plugin_sessions
ALTER TABLE plugin_sessions
    ADD COLUMN IF NOT EXISTS intent_context TEXT NOT NULL DEFAULT '{}';

-- +migrate Dialect sqlite
SELECT 1; -- Historical SQLite change is included by the first v0.2 dev migration.
