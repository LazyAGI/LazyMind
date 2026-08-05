-- +migrate Dialect postgres
ALTER TABLE plugin_drafts DROP COLUMN IF EXISTS driver_content;
ALTER TABLE plugin_attempt_input_bindings
    DROP COLUMN IF EXISTS content_hash,
    DROP COLUMN IF EXISTS source_revision,
    DROP COLUMN IF EXISTS source_id,
    DROP COLUMN IF EXISTS source_type;
DROP TABLE IF EXISTS workflow_input_bindings;
DROP TABLE IF EXISTS workflow_input_resources;
DROP TABLE IF EXISTS workflow_outbox;
DROP INDEX IF EXISTS idx_plugin_session_steps_claim;
ALTER TABLE plugin_session_steps
    DROP COLUMN IF EXISTS result_json,
    DROP COLUMN IF EXISTS terminal_code,
    DROP COLUMN IF EXISTS progress_json,
    DROP COLUMN IF EXISTS heartbeat_at,
    DROP COLUMN IF EXISTS lease_expires_at,
    DROP COLUMN IF EXISTS fencing_generation,
    DROP COLUMN IF EXISTS lease_token,
    DROP COLUMN IF EXISTS lease_owner;
DROP TABLE IF EXISTS workflow_events;
DROP TABLE IF EXISTS workflow_commands;
DROP TABLE IF EXISTS workflow_preparations;
DROP INDEX IF EXISTS idx_plugin_sessions_origin;
ALTER TABLE plugin_sessions
    DROP COLUMN IF EXISTS controller_host,
    DROP COLUMN IF EXISTS origin_ref,
    DROP COLUMN IF EXISTS origin_host;

-- +migrate Dialect sqlite
ALTER TABLE plugin_drafts DROP COLUMN driver_content;
ALTER TABLE plugin_attempt_input_bindings DROP COLUMN content_hash;
ALTER TABLE plugin_attempt_input_bindings DROP COLUMN source_revision;
ALTER TABLE plugin_attempt_input_bindings DROP COLUMN source_id;
ALTER TABLE plugin_attempt_input_bindings DROP COLUMN source_type;
DROP TABLE IF EXISTS workflow_input_bindings;
DROP TABLE IF EXISTS workflow_input_resources;
DROP TABLE IF EXISTS workflow_outbox;
DROP INDEX IF EXISTS idx_plugin_session_steps_claim;
ALTER TABLE plugin_session_steps DROP COLUMN result_json;
ALTER TABLE plugin_session_steps DROP COLUMN terminal_code;
ALTER TABLE plugin_session_steps DROP COLUMN progress_json;
ALTER TABLE plugin_session_steps DROP COLUMN heartbeat_at;
ALTER TABLE plugin_session_steps DROP COLUMN lease_expires_at;
ALTER TABLE plugin_session_steps DROP COLUMN fencing_generation;
ALTER TABLE plugin_session_steps DROP COLUMN lease_token;
ALTER TABLE plugin_session_steps DROP COLUMN lease_owner;
DROP TABLE IF EXISTS workflow_events;
DROP TABLE IF EXISTS workflow_commands;
DROP TABLE IF EXISTS workflow_preparations;
DROP INDEX IF EXISTS idx_plugin_sessions_origin;
ALTER TABLE plugin_sessions DROP COLUMN controller_host;
ALTER TABLE plugin_sessions DROP COLUMN origin_ref;
ALTER TABLE plugin_sessions DROP COLUMN origin_host;
