-- +migrate Dialect postgres,sqlite
CREATE TABLE IF NOT EXISTS external_capability_grants (
    id VARCHAR(64) PRIMARY KEY,
    owner_user_id VARCHAR(255) NOT NULL,
    agent VARCHAR(64) NOT NULL,
    capability_type VARCHAR(16) NOT NULL,
    capability_id VARCHAR(128) NOT NULL,
    enabled BOOLEAN NOT NULL DEFAULT FALSE,
    created_at TIMESTAMP NOT NULL,
    updated_at TIMESTAMP NOT NULL,
    CONSTRAINT uk_external_capability_grant UNIQUE (owner_user_id, agent, capability_type, capability_id)
);
CREATE INDEX IF NOT EXISTS idx_external_capability_grants_owner_user_id ON external_capability_grants(owner_user_id);
CREATE INDEX IF NOT EXISTS idx_external_capability_grants_agent ON external_capability_grants(agent);
CREATE INDEX IF NOT EXISTS idx_external_capability_grants_capability_id ON external_capability_grants(capability_id);

CREATE TABLE IF NOT EXISTS external_capability_invocations (
    id VARCHAR(80) PRIMARY KEY,
    owner_user_id VARCHAR(255) NOT NULL,
    agent VARCHAR(64) NOT NULL,
    invocation_id VARCHAR(80) NOT NULL DEFAULT '',
    capability_type VARCHAR(16) NOT NULL,
    capability_id VARCHAR(128) NOT NULL,
    capability_name VARCHAR(512) NOT NULL,
    status VARCHAR(32) NOT NULL,
    usage_json JSON NOT NULL,
    error_code VARCHAR(64) NOT NULL DEFAULT '',
    error_message TEXT NOT NULL DEFAULT '',
    started_at TIMESTAMP NOT NULL,
    finished_at TIMESTAMP,
    created_at TIMESTAMP NOT NULL,
    updated_at TIMESTAMP NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_external_capability_invocations_owner_started ON external_capability_invocations(owner_user_id, started_at DESC);
CREATE INDEX IF NOT EXISTS idx_external_capability_invocations_agent ON external_capability_invocations(agent);
CREATE INDEX IF NOT EXISTS idx_external_capability_invocations_invocation_id ON external_capability_invocations(invocation_id);
CREATE INDEX IF NOT EXISTS idx_external_capability_invocations_capability_type ON external_capability_invocations(capability_type);
CREATE INDEX IF NOT EXISTS idx_external_capability_invocations_capability_id ON external_capability_invocations(capability_id);
CREATE INDEX IF NOT EXISTS idx_external_capability_invocations_status ON external_capability_invocations(status);
