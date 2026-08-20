-- +migrate Dialect postgres
CREATE TABLE IF NOT EXISTS agent_invocations (
    id VARCHAR(80) PRIMARY KEY,
    owner_user_id VARCHAR(255) NOT NULL,
    client_name VARCHAR(128) NOT NULL DEFAULT '',
    client_version VARCHAR(128) NOT NULL DEFAULT '',
    connector_name VARCHAR(128) NOT NULL DEFAULT '',
    connector_version VARCHAR(64) NOT NULL DEFAULT '',
    connector_instance_id VARCHAR(80) NOT NULL DEFAULT '',
    protocol_version VARCHAR(64) NOT NULL DEFAULT '',
    transport VARCHAR(32) NOT NULL DEFAULT 'stdio',
    tool_name VARCHAR(128) NOT NULL,
    read_only BOOLEAN NOT NULL DEFAULT FALSE,
    status VARCHAR(32) NOT NULL,
    request_hash VARCHAR(64) NOT NULL,
    request_summary JSONB NOT NULL DEFAULT '{}',
    result_summary JSONB NOT NULL DEFAULT '{}',
    error_code VARCHAR(128) NOT NULL DEFAULT '',
    retryable BOOLEAN NOT NULL DEFAULT FALSE,
    workflow_id VARCHAR(255) NOT NULL DEFAULT '',
    session_id VARCHAR(80) NOT NULL DEFAULT '',
    step_id VARCHAR(128) NOT NULL DEFAULT '',
    attempt_id VARCHAR(80) NOT NULL DEFAULT '',
    resource_id VARCHAR(128) NOT NULL DEFAULT '',
    artifact_id VARCHAR(80) NOT NULL DEFAULT '',
    command_id VARCHAR(128) NOT NULL DEFAULT '',
    external_ref VARCHAR(255) NOT NULL DEFAULT '',
    started_at TIMESTAMP NOT NULL,
    finished_at TIMESTAMP,
    created_at TIMESTAMP NOT NULL,
    updated_at TIMESTAMP NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_agent_invocations_owner_started ON agent_invocations(owner_user_id, started_at DESC);
CREATE INDEX IF NOT EXISTS idx_agent_invocations_client_name ON agent_invocations(client_name);
CREATE INDEX IF NOT EXISTS idx_agent_invocations_connector_instance_id ON agent_invocations(connector_instance_id);
CREATE INDEX IF NOT EXISTS idx_agent_invocations_tool_name ON agent_invocations(tool_name);
CREATE INDEX IF NOT EXISTS idx_agent_invocations_status ON agent_invocations(status);
CREATE INDEX IF NOT EXISTS idx_agent_invocations_workflow_id ON agent_invocations(workflow_id);
CREATE INDEX IF NOT EXISTS idx_agent_invocations_session_id ON agent_invocations(session_id);
CREATE INDEX IF NOT EXISTS idx_agent_invocations_attempt_id ON agent_invocations(attempt_id);

-- +migrate Dialect sqlite
CREATE TABLE IF NOT EXISTS agent_invocations (
    id VARCHAR(80) PRIMARY KEY,
    owner_user_id VARCHAR(255) NOT NULL,
    client_name VARCHAR(128) NOT NULL DEFAULT '',
    client_version VARCHAR(128) NOT NULL DEFAULT '',
    connector_name VARCHAR(128) NOT NULL DEFAULT '',
    connector_version VARCHAR(64) NOT NULL DEFAULT '',
    connector_instance_id VARCHAR(80) NOT NULL DEFAULT '',
    protocol_version VARCHAR(64) NOT NULL DEFAULT '',
    transport VARCHAR(32) NOT NULL DEFAULT 'stdio',
    tool_name VARCHAR(128) NOT NULL,
    read_only BOOLEAN NOT NULL DEFAULT FALSE,
    status VARCHAR(32) NOT NULL,
    request_hash VARCHAR(64) NOT NULL,
    request_summary TEXT NOT NULL DEFAULT '{}',
    result_summary TEXT NOT NULL DEFAULT '{}',
    error_code VARCHAR(128) NOT NULL DEFAULT '',
    retryable BOOLEAN NOT NULL DEFAULT FALSE,
    workflow_id VARCHAR(255) NOT NULL DEFAULT '',
    session_id VARCHAR(80) NOT NULL DEFAULT '',
    step_id VARCHAR(128) NOT NULL DEFAULT '',
    attempt_id VARCHAR(80) NOT NULL DEFAULT '',
    resource_id VARCHAR(128) NOT NULL DEFAULT '',
    artifact_id VARCHAR(80) NOT NULL DEFAULT '',
    command_id VARCHAR(128) NOT NULL DEFAULT '',
    external_ref VARCHAR(255) NOT NULL DEFAULT '',
    started_at DATETIME NOT NULL,
    finished_at DATETIME,
    created_at DATETIME NOT NULL,
    updated_at DATETIME NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_agent_invocations_owner_started ON agent_invocations(owner_user_id, started_at DESC);
CREATE INDEX IF NOT EXISTS idx_agent_invocations_client_name ON agent_invocations(client_name);
CREATE INDEX IF NOT EXISTS idx_agent_invocations_connector_instance_id ON agent_invocations(connector_instance_id);
CREATE INDEX IF NOT EXISTS idx_agent_invocations_tool_name ON agent_invocations(tool_name);
CREATE INDEX IF NOT EXISTS idx_agent_invocations_status ON agent_invocations(status);
CREATE INDEX IF NOT EXISTS idx_agent_invocations_workflow_id ON agent_invocations(workflow_id);
CREATE INDEX IF NOT EXISTS idx_agent_invocations_session_id ON agent_invocations(session_id);
CREATE INDEX IF NOT EXISTS idx_agent_invocations_attempt_id ON agent_invocations(attempt_id);
