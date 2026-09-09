DROP TABLE IF EXISTS workflow_host_actions;
DROP TABLE IF EXISTS workflow_review_checkpoints;
ALTER TABLE plugin_session_steps DROP COLUMN submission_hash;
ALTER TABLE plugin_session_steps DROP COLUMN review_required;
ALTER TABLE plugin_sessions DROP COLUMN control_binding_json;
ALTER TABLE plugin_sessions DROP COLUMN control_protocol;
