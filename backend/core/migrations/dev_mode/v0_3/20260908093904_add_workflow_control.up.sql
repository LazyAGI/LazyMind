-- Durable review and host-delivery facts for opt-in controlled Workflow sessions.
ALTER TABLE plugin_sessions ADD COLUMN control_protocol VARCHAR(32) NOT NULL DEFAULT '';
ALTER TABLE plugin_sessions ADD COLUMN control_binding_json TEXT NOT NULL DEFAULT '{}';
ALTER TABLE plugin_session_steps ADD COLUMN review_required BOOLEAN NOT NULL DEFAULT FALSE;
ALTER TABLE plugin_session_steps ADD COLUMN submission_hash VARCHAR(64) NOT NULL DEFAULT '';
CREATE TABLE workflow_review_checkpoints (
    id VARCHAR(36) PRIMARY KEY,
    session_id VARCHAR(36) NOT NULL,
    attempt_id VARCHAR(36) NOT NULL UNIQUE,
    step_id VARCHAR(64) NOT NULL,
    version BIGINT NOT NULL DEFAULT 1,
    status VARCHAR(16) NOT NULL DEFAULT 'pending',
    slots_json TEXT NOT NULL DEFAULT '[]',
    manifest_json TEXT NOT NULL DEFAULT '[]',
    manifest_hash VARCHAR(64) NOT NULL DEFAULT '',
    decision_command_id VARCHAR(255) NOT NULL DEFAULT '',
    accepted_by VARCHAR(255) NOT NULL DEFAULT '',
    accepted_at TIMESTAMP,
    created_at TIMESTAMP NOT NULL,
    updated_at TIMESTAMP NOT NULL
);
CREATE INDEX idx_workflow_reviews_session_status ON workflow_review_checkpoints(session_id, status);
CREATE TABLE workflow_host_actions (
    id VARCHAR(36) PRIMARY KEY,
    session_id VARCHAR(36) NOT NULL,
    command_id VARCHAR(255) NOT NULL UNIQUE,
    kind VARCHAR(16) NOT NULL,
    binding_generation BIGINT NOT NULL,
    connector_id VARCHAR(128) NOT NULL,
    native_session_id VARCHAR(255) NOT NULL,
    execution_id VARCHAR(36) NOT NULL DEFAULT '',
    status VARCHAR(16) NOT NULL DEFAULT 'pending',
    dispatch_owner VARCHAR(128) NOT NULL DEFAULT '',
    dispatch_token_hash VARCHAR(64) NOT NULL DEFAULT '',
    dispatch_expires_at TIMESTAMP,
    attempt_count INTEGER NOT NULL DEFAULT 0,
    last_error TEXT NOT NULL DEFAULT '',
    native_event_seq BIGINT NOT NULL DEFAULT 0,
    accepted_at TIMESTAMP,
    consumed_at TIMESTAMP,
    created_at TIMESTAMP NOT NULL,
    updated_at TIMESTAMP NOT NULL
);
CREATE INDEX idx_workflow_host_actions_delivery ON workflow_host_actions(connector_id, status, created_at);
CREATE INDEX idx_workflow_host_actions_session ON workflow_host_actions(session_id, created_at);
