-- +migrate Dialect postgres
ALTER TABLE plugin_sessions
    ADD COLUMN IF NOT EXISTS workflow_mode VARCHAR(16) NOT NULL DEFAULT 'dynamic';

-- +migrate Dialect sqlite
ALTER TABLE plugin_sessions
    ADD COLUMN workflow_mode varchar(16) NOT NULL DEFAULT 'dynamic';
