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
