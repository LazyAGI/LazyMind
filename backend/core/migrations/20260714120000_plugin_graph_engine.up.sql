ALTER TABLE plugin_sessions ADD COLUMN IF NOT EXISTS state_version BIGINT NOT NULL DEFAULT 0;
ALTER TABLE plugin_sessions ADD COLUMN IF NOT EXISTS graph_hash VARCHAR(64) NOT NULL DEFAULT '';
ALTER TABLE plugin_sessions ADD COLUMN IF NOT EXISTS graph_schema_version VARCHAR(16) NOT NULL DEFAULT '';

ALTER TABLE plugin_session_steps ADD COLUMN IF NOT EXISTS validity VARCHAR(16) NOT NULL DEFAULT 'effective';
ALTER TABLE plugin_slot_revisions ADD COLUMN IF NOT EXISTS validity VARCHAR(16) NOT NULL DEFAULT 'effective';
ALTER TABLE plugin_slot_revisions ADD COLUMN IF NOT EXISTS producer_attempt_id VARCHAR(36) NOT NULL DEFAULT '';

ALTER TABLE plugin_revisions ADD COLUMN IF NOT EXISTS compiled_graph JSONB;
ALTER TABLE plugin_revisions ADD COLUMN IF NOT EXISTS graph_hash VARCHAR(64) NOT NULL DEFAULT '';
ALTER TABLE plugin_revisions ADD COLUMN IF NOT EXISTS graph_schema_version VARCHAR(16) NOT NULL DEFAULT '';

CREATE TABLE IF NOT EXISTS plugin_attempt_input_bindings (
    id VARCHAR(36) PRIMARY KEY,
    session_id VARCHAR(36) NOT NULL,
    attempt_id VARCHAR(36) NOT NULL,
    material_id VARCHAR(64) NOT NULL,
    material_revision_id VARCHAR(36) NOT NULL,
    bind_as VARCHAR(64) NOT NULL DEFAULT '',
    created_at TIMESTAMP NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_paib_attempt ON plugin_attempt_input_bindings(attempt_id);
CREATE INDEX IF NOT EXISTS idx_paib_material_revision ON plugin_attempt_input_bindings(material_revision_id);

CREATE TABLE IF NOT EXISTS plugin_route_decisions (
    id VARCHAR(36) PRIMARY KEY,
    session_id VARCHAR(36) NOT NULL,
    from_step_id VARCHAR(64) NOT NULL,
    source_attempt_id VARCHAR(36) NOT NULL DEFAULT '',
    activated_json JSONB NOT NULL DEFAULT '[]',
    pruned_json JSONB NOT NULL DEFAULT '[]',
    bypassed_json JSONB NOT NULL DEFAULT '[]',
    witness_json JSONB NOT NULL DEFAULT '[]',
    validity VARCHAR(16) NOT NULL DEFAULT 'effective',
    state_version BIGINT NOT NULL,
    created_at TIMESTAMP NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_prd_session ON plugin_route_decisions(session_id, from_step_id);

CREATE TABLE IF NOT EXISTS plugin_transition_commands (
    command_id VARCHAR(36) PRIMARY KEY,
    session_id VARCHAR(36) NOT NULL DEFAULT '',
    operation VARCHAR(16) NOT NULL,
    target_step_id VARCHAR(64) NOT NULL DEFAULT '',
    status VARCHAR(16) NOT NULL,
    task_id VARCHAR(36) NOT NULL DEFAULT '',
    expected_state_version BIGINT NOT NULL DEFAULT 0,
    resulting_state_version BIGINT NOT NULL DEFAULT 0,
    response_json JSONB NOT NULL,
    created_at TIMESTAMP NOT NULL,
    updated_at TIMESTAMP NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_ptc_session ON plugin_transition_commands(session_id, created_at);
