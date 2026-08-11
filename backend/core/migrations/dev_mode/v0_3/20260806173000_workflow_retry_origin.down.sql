-- +migrate Dialect postgres
ALTER TABLE plugin_transition_commands DROP COLUMN IF EXISTS retry_origin;

-- +migrate Dialect sqlite
ALTER TABLE plugin_transition_commands DROP COLUMN retry_origin;
