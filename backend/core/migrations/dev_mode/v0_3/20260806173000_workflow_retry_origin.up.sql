-- +migrate Dialect postgres
ALTER TABLE plugin_transition_commands
    ADD COLUMN IF NOT EXISTS retry_origin VARCHAR(16) NOT NULL DEFAULT 'automatic';

-- +migrate Dialect sqlite
ALTER TABLE plugin_transition_commands
    ADD COLUMN retry_origin VARCHAR(16) NOT NULL DEFAULT 'automatic';
