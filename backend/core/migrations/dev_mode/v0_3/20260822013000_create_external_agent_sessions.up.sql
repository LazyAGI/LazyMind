-- +migrate Dialect postgres
CREATE TABLE IF NOT EXISTS external_agent_sessions (
    id VARCHAR(36) PRIMARY KEY,
    owner_user_id VARCHAR(255) NOT NULL,
    provider VARCHAR(32) NOT NULL,
    provider_thread_id VARCHAR(128) NOT NULL,
    project_key VARCHAR(128) NOT NULL DEFAULT '',
    project_name VARCHAR(200) NOT NULL DEFAULT '',
    display_name VARCHAR(255) NOT NULL DEFAULT '',
    used_lazymind BOOLEAN NOT NULL DEFAULT FALSE,
    import_state VARCHAR(32) NOT NULL DEFAULT 'unlinked',
    turn_count INTEGER NOT NULL DEFAULT 0,
    active BOOLEAN NOT NULL DEFAULT TRUE,
    native_updated_at TIMESTAMP,
    last_seen_at TIMESTAMP NOT NULL,
    created_at TIMESTAMP NOT NULL,
    updated_at TIMESTAMP NOT NULL,
    CONSTRAINT uk_external_agent_session UNIQUE (owner_user_id, provider, provider_thread_id)
);
CREATE INDEX IF NOT EXISTS idx_external_agent_session_catalog
    ON external_agent_sessions(owner_user_id, provider, active, used_lazymind, import_state, native_updated_at DESC);
CREATE INDEX IF NOT EXISTS idx_external_agent_session_last_seen
    ON external_agent_sessions(last_seen_at);

-- +migrate Dialect sqlite
CREATE TABLE IF NOT EXISTS external_agent_sessions (
    id VARCHAR(36) PRIMARY KEY,
    owner_user_id VARCHAR(255) NOT NULL,
    provider VARCHAR(32) NOT NULL,
    provider_thread_id VARCHAR(128) NOT NULL,
    project_key VARCHAR(128) NOT NULL DEFAULT '',
    project_name VARCHAR(200) NOT NULL DEFAULT '',
    display_name VARCHAR(255) NOT NULL DEFAULT '',
    used_lazymind BOOLEAN NOT NULL DEFAULT FALSE,
    import_state VARCHAR(32) NOT NULL DEFAULT 'unlinked',
    turn_count INTEGER NOT NULL DEFAULT 0,
    active BOOLEAN NOT NULL DEFAULT TRUE,
    native_updated_at DATETIME,
    last_seen_at DATETIME NOT NULL,
    created_at DATETIME NOT NULL,
    updated_at DATETIME NOT NULL,
    CONSTRAINT uk_external_agent_session UNIQUE (owner_user_id, provider, provider_thread_id)
);
CREATE INDEX IF NOT EXISTS idx_external_agent_session_catalog
    ON external_agent_sessions(owner_user_id, provider, active, used_lazymind, import_state, native_updated_at DESC);
CREATE INDEX IF NOT EXISTS idx_external_agent_session_last_seen
    ON external_agent_sessions(last_seen_at);
