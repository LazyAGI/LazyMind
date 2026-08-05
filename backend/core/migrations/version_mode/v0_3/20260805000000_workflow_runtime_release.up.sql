-- +migrate Dialect postgres
ALTER TABLE plugin_sessions ADD COLUMN IF NOT EXISTS origin_host VARCHAR(32) NOT NULL DEFAULT 'lazymind';
ALTER TABLE plugin_sessions ADD COLUMN IF NOT EXISTS origin_ref VARCHAR(255) NOT NULL DEFAULT '';
ALTER TABLE plugin_sessions ADD COLUMN IF NOT EXISTS controller_host VARCHAR(32) NOT NULL DEFAULT 'lazymind';
CREATE INDEX IF NOT EXISTS idx_plugin_sessions_origin ON plugin_sessions(origin_host, origin_ref);

-- +migrate Dialect sqlite
ALTER TABLE plugin_sessions ADD COLUMN origin_host varchar(32) NOT NULL DEFAULT 'lazymind';
ALTER TABLE plugin_sessions ADD COLUMN origin_ref varchar(255) NOT NULL DEFAULT '';
ALTER TABLE plugin_sessions ADD COLUMN controller_host varchar(32) NOT NULL DEFAULT 'lazymind';
CREATE INDEX IF NOT EXISTS idx_plugin_sessions_origin ON plugin_sessions(origin_host, origin_ref);

-- +migrate Dialect postgres
-- Expand-only Workflow v1 facade persistence. Legacy plugin_* Runtime tables
-- remain authoritative and unchanged during the shadow/compatibility window.
CREATE TABLE IF NOT EXISTS workflow_preparations (
    id VARCHAR(36) PRIMARY KEY,
    idempotency_key VARCHAR(255) NOT NULL,
    owner_user_id VARCHAR(255) NOT NULL,
    workflow_id VARCHAR(255) NOT NULL,
    contract_version VARCHAR(32) NOT NULL,
    request_json JSONB NOT NULL,
    response_json JSONB NOT NULL,
    consumed_at TIMESTAMP NULL,
    session_id VARCHAR(36) NOT NULL DEFAULT '',
    created_at TIMESTAMP NOT NULL,
    updated_at TIMESTAMP NOT NULL,
    CONSTRAINT uk_workflow_preparation_owner_key UNIQUE (owner_user_id, idempotency_key)
);
CREATE INDEX IF NOT EXISTS idx_workflow_preparations_owner ON workflow_preparations(owner_user_id);

CREATE TABLE IF NOT EXISTS workflow_commands (
    command_id VARCHAR(255) PRIMARY KEY,
    owner_user_id VARCHAR(255) NOT NULL,
    session_id VARCHAR(36) NOT NULL,
    contract_version VARCHAR(32) NOT NULL,
    request_hash VARCHAR(64) NOT NULL,
    http_status INTEGER NOT NULL,
    response_json JSONB NOT NULL,
    created_at TIMESTAMP NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_workflow_commands_owner ON workflow_commands(owner_user_id);
CREATE INDEX IF NOT EXISTS idx_workflow_commands_session ON workflow_commands(session_id);

CREATE TABLE IF NOT EXISTS workflow_events (
    id BIGSERIAL PRIMARY KEY,
    session_id VARCHAR(36) NOT NULL,
    owner_user_id VARCHAR(255) NOT NULL,
    contract_version VARCHAR(32) NOT NULL,
    event_type VARCHAR(64) NOT NULL,
    entity_id VARCHAR(255) NOT NULL DEFAULT '',
    state_version BIGINT NOT NULL DEFAULT 0,
    command_id VARCHAR(255) NOT NULL DEFAULT '',
    payload_json JSONB NOT NULL,
    created_at TIMESTAMP NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_workflow_events_session_cursor ON workflow_events(session_id, id);
CREATE INDEX IF NOT EXISTS idx_workflow_events_owner ON workflow_events(owner_user_id);
CREATE INDEX IF NOT EXISTS idx_workflow_events_command ON workflow_events(command_id);

-- +migrate Dialect sqlite
CREATE TABLE IF NOT EXISTS workflow_preparations (
    id TEXT PRIMARY KEY, idempotency_key TEXT NOT NULL, owner_user_id TEXT NOT NULL,
    workflow_id TEXT NOT NULL, contract_version TEXT NOT NULL, request_json TEXT NOT NULL,
    response_json TEXT NOT NULL, consumed_at DATETIME NULL, session_id TEXT NOT NULL DEFAULT '',
    created_at DATETIME NOT NULL, updated_at DATETIME NOT NULL,
    UNIQUE(owner_user_id, idempotency_key)
);
CREATE INDEX IF NOT EXISTS idx_workflow_preparations_owner ON workflow_preparations(owner_user_id);
CREATE TABLE IF NOT EXISTS workflow_commands (
    command_id TEXT PRIMARY KEY, owner_user_id TEXT NOT NULL, session_id TEXT NOT NULL,
    contract_version TEXT NOT NULL, request_hash TEXT NOT NULL, http_status INTEGER NOT NULL,
    response_json TEXT NOT NULL, created_at DATETIME NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_workflow_commands_owner ON workflow_commands(owner_user_id);
CREATE INDEX IF NOT EXISTS idx_workflow_commands_session ON workflow_commands(session_id);
CREATE TABLE IF NOT EXISTS workflow_events (
    id INTEGER PRIMARY KEY AUTOINCREMENT, session_id TEXT NOT NULL, owner_user_id TEXT NOT NULL,
    contract_version TEXT NOT NULL, event_type TEXT NOT NULL, entity_id TEXT NOT NULL DEFAULT '',
    state_version INTEGER NOT NULL DEFAULT 0, command_id TEXT NOT NULL DEFAULT '',
    payload_json TEXT NOT NULL, created_at DATETIME NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_workflow_events_session_cursor ON workflow_events(session_id, id);
CREATE INDEX IF NOT EXISTS idx_workflow_events_owner ON workflow_events(owner_user_id);
CREATE INDEX IF NOT EXISTS idx_workflow_events_command ON workflow_events(command_id);

-- +migrate Dialect postgres
ALTER TABLE plugin_session_steps ADD COLUMN IF NOT EXISTS lease_owner VARCHAR(255) NOT NULL DEFAULT '';
ALTER TABLE plugin_session_steps ADD COLUMN IF NOT EXISTS lease_token VARCHAR(255) NOT NULL DEFAULT '';
ALTER TABLE plugin_session_steps ADD COLUMN IF NOT EXISTS fencing_generation BIGINT NOT NULL DEFAULT 0;
ALTER TABLE plugin_session_steps ADD COLUMN IF NOT EXISTS lease_expires_at TIMESTAMP NULL;
ALTER TABLE plugin_session_steps ADD COLUMN IF NOT EXISTS heartbeat_at TIMESTAMP NULL;
ALTER TABLE plugin_session_steps ADD COLUMN IF NOT EXISTS progress_json JSONB NOT NULL DEFAULT '{}';
ALTER TABLE plugin_session_steps ADD COLUMN IF NOT EXISTS terminal_code VARCHAR(64) NOT NULL DEFAULT '';
ALTER TABLE plugin_session_steps ADD COLUMN IF NOT EXISTS result_json JSONB NOT NULL DEFAULT '{}';
CREATE INDEX IF NOT EXISTS idx_plugin_session_steps_claim ON plugin_session_steps(status, lease_expires_at, id);
CREATE TABLE IF NOT EXISTS workflow_outbox (
    id VARCHAR(36) PRIMARY KEY,
    attempt_id VARCHAR(36) NOT NULL UNIQUE,
    session_id VARCHAR(36) NOT NULL,
    payload_json JSONB NOT NULL,
    status VARCHAR(16) NOT NULL DEFAULT 'pending',
    created_at TIMESTAMP NOT NULL,
    updated_at TIMESTAMP NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_workflow_outbox_status ON workflow_outbox(status, created_at);
CREATE INDEX IF NOT EXISTS idx_workflow_outbox_session ON workflow_outbox(session_id);

-- +migrate Dialect sqlite
ALTER TABLE plugin_session_steps ADD COLUMN lease_owner TEXT NOT NULL DEFAULT '';
ALTER TABLE plugin_session_steps ADD COLUMN lease_token TEXT NOT NULL DEFAULT '';
ALTER TABLE plugin_session_steps ADD COLUMN fencing_generation INTEGER NOT NULL DEFAULT 0;
ALTER TABLE plugin_session_steps ADD COLUMN lease_expires_at DATETIME NULL;
ALTER TABLE plugin_session_steps ADD COLUMN heartbeat_at DATETIME NULL;
ALTER TABLE plugin_session_steps ADD COLUMN progress_json TEXT NOT NULL DEFAULT '{}';
ALTER TABLE plugin_session_steps ADD COLUMN terminal_code TEXT NOT NULL DEFAULT '';
ALTER TABLE plugin_session_steps ADD COLUMN result_json TEXT NOT NULL DEFAULT '{}';
CREATE INDEX IF NOT EXISTS idx_plugin_session_steps_claim ON plugin_session_steps(status, lease_expires_at, id);
CREATE TABLE IF NOT EXISTS workflow_outbox (
    id TEXT PRIMARY KEY, attempt_id TEXT NOT NULL UNIQUE, session_id TEXT NOT NULL,
    payload_json TEXT NOT NULL, status TEXT NOT NULL DEFAULT 'pending',
    created_at DATETIME NOT NULL, updated_at DATETIME NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_workflow_outbox_status ON workflow_outbox(status, created_at);
CREATE INDEX IF NOT EXISTS idx_workflow_outbox_session ON workflow_outbox(session_id);

-- +migrate Dialect postgres
CREATE TABLE IF NOT EXISTS workflow_input_resources (
    id varchar(36) PRIMARY KEY,
    owner_user_id varchar(255) NOT NULL,
    name varchar(255) NOT NULL,
    mime_type varchar(255) NOT NULL,
    size bigint NOT NULL,
    content_hash varchar(80) NOT NULL,
    revision bigint NOT NULL DEFAULT 1,
    content bytea NOT NULL,
    created_at timestamp NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_workflow_input_resources_owner_hash
    ON workflow_input_resources(owner_user_id, content_hash);

CREATE TABLE IF NOT EXISTS workflow_input_bindings (
    id varchar(36) PRIMARY KEY,
    workflow_session_id varchar(36) NOT NULL,
    material_id varchar(64) NOT NULL,
    resource_type varchar(32) NOT NULL,
    resource_id varchar(36) NOT NULL,
    resource_revision bigint NOT NULL,
    content_hash varchar(80) NOT NULL,
    validity varchar(16) NOT NULL DEFAULT 'effective',
    created_by_command_id varchar(64) NOT NULL,
    created_at timestamp NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_workflow_input_bindings_session
    ON workflow_input_bindings(workflow_session_id);
CREATE INDEX IF NOT EXISTS idx_workflow_input_bindings_resource
    ON workflow_input_bindings(resource_id);

ALTER TABLE plugin_attempt_input_bindings ADD COLUMN source_type varchar(32) NOT NULL DEFAULT 'artifact';
ALTER TABLE plugin_attempt_input_bindings ADD COLUMN source_id varchar(128) NOT NULL DEFAULT '';
ALTER TABLE plugin_attempt_input_bindings ADD COLUMN source_revision varchar(64) NOT NULL DEFAULT '';
ALTER TABLE plugin_attempt_input_bindings ADD COLUMN content_hash varchar(80) NOT NULL DEFAULT '';

-- +migrate Dialect sqlite
CREATE TABLE IF NOT EXISTS workflow_input_resources (
    id TEXT PRIMARY KEY,
    owner_user_id TEXT NOT NULL,
    name TEXT NOT NULL,
    mime_type TEXT NOT NULL,
    size INTEGER NOT NULL,
    content_hash TEXT NOT NULL,
    revision INTEGER NOT NULL DEFAULT 1,
    content BLOB NOT NULL,
    created_at DATETIME NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_workflow_input_resources_owner_hash
    ON workflow_input_resources(owner_user_id, content_hash);

CREATE TABLE IF NOT EXISTS workflow_input_bindings (
    id TEXT PRIMARY KEY,
    workflow_session_id TEXT NOT NULL,
    material_id TEXT NOT NULL,
    resource_type TEXT NOT NULL,
    resource_id TEXT NOT NULL,
    resource_revision INTEGER NOT NULL,
    content_hash TEXT NOT NULL,
    validity TEXT NOT NULL DEFAULT 'effective',
    created_by_command_id TEXT NOT NULL,
    created_at DATETIME NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_workflow_input_bindings_session
    ON workflow_input_bindings(workflow_session_id);
CREATE INDEX IF NOT EXISTS idx_workflow_input_bindings_resource
    ON workflow_input_bindings(resource_id);

ALTER TABLE plugin_attempt_input_bindings ADD COLUMN source_type TEXT NOT NULL DEFAULT 'artifact';
ALTER TABLE plugin_attempt_input_bindings ADD COLUMN source_id TEXT NOT NULL DEFAULT '';
ALTER TABLE plugin_attempt_input_bindings ADD COLUMN source_revision TEXT NOT NULL DEFAULT '';
ALTER TABLE plugin_attempt_input_bindings ADD COLUMN content_hash TEXT NOT NULL DEFAULT '';

-- +migrate Dialect postgres
ALTER TABLE plugin_drafts ADD COLUMN IF NOT EXISTS driver_content TEXT NOT NULL DEFAULT '';

-- +migrate Dialect sqlite
ALTER TABLE plugin_drafts ADD COLUMN driver_content TEXT NOT NULL DEFAULT '';
