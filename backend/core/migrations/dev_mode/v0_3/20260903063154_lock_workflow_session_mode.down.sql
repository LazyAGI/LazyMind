-- +migrate Dialect postgres
ALTER TABLE plugin_sessions DROP COLUMN IF EXISTS workflow_mode;

-- +migrate Dialect sqlite
ALTER TABLE plugin_sessions DROP COLUMN workflow_mode;
