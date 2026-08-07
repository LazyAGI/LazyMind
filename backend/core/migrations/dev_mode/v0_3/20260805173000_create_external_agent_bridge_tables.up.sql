CREATE TABLE IF NOT EXISTS external_agent_bindings (
    id VARCHAR(36) PRIMARY KEY,
    conversation_id VARCHAR(36) NOT NULL,
    provider VARCHAR(32) NOT NULL,
    provider_thread_id VARCHAR(128) NOT NULL,
    managed_by_lazymind BOOLEAN NOT NULL DEFAULT FALSE,
    created_by_user_id VARCHAR(255) NOT NULL,
    created_at TIMESTAMP NOT NULL,
    updated_at TIMESTAMP NOT NULL,
    CONSTRAINT uk_external_agent_binding_conversation UNIQUE (conversation_id),
    CONSTRAINT uk_external_agent_binding_thread UNIQUE (provider, provider_thread_id)
);

CREATE TABLE IF NOT EXISTS external_agent_runs (
    id VARCHAR(36) PRIMARY KEY,
    request_id VARCHAR(255) NOT NULL,
    conversation_id VARCHAR(36) NOT NULL,
    history_id VARCHAR(36) NOT NULL,
    provider VARCHAR(32) NOT NULL,
    provider_thread_id VARCHAR(128) NOT NULL,
    provider_turn_id VARCHAR(128),
    actor_user_id VARCHAR(255) NOT NULL,
    action VARCHAR(32) NOT NULL DEFAULT 'start',
    status VARCHAR(32) NOT NULL,
    error_message TEXT,
    created_at TIMESTAMP NOT NULL,
    updated_at TIMESTAMP NOT NULL,
    CONSTRAINT uk_external_agent_run_request UNIQUE (provider, request_id)
);

CREATE INDEX IF NOT EXISTS idx_external_agent_runs_conversation_id ON external_agent_runs (conversation_id);
CREATE INDEX IF NOT EXISTS idx_external_agent_runs_provider_thread_id ON external_agent_runs (provider_thread_id);
CREATE INDEX IF NOT EXISTS idx_external_agent_runs_status ON external_agent_runs (status);
