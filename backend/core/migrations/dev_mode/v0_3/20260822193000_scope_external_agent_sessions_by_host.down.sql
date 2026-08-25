-- +migrate Dialect postgres
DROP INDEX IF EXISTS uk_external_agent_binding_thread;
DROP INDEX IF EXISTS uk_external_agent_binding_conversation;
CREATE UNIQUE INDEX uk_external_agent_binding_conversation_provider ON external_agent_bindings (conversation_id, provider);
CREATE UNIQUE INDEX uk_external_agent_binding_thread ON external_agent_bindings (provider, provider_thread_id);
ALTER TABLE external_agent_bindings DROP COLUMN IF EXISTS host_id;
ALTER TABLE external_agent_bindings ADD COLUMN IF NOT EXISTS managed_by_lazymind BOOLEAN NOT NULL DEFAULT FALSE;

DROP INDEX IF EXISTS uk_external_agent_session;
CREATE UNIQUE INDEX uk_external_agent_session ON external_agent_sessions (owner_user_id, provider, provider_thread_id);
ALTER TABLE external_agent_sessions DROP COLUMN IF EXISTS host_id;
ALTER TABLE external_agent_sessions ADD COLUMN IF NOT EXISTS used_lazymind BOOLEAN NOT NULL DEFAULT FALSE;
ALTER TABLE external_agent_sessions ADD COLUMN IF NOT EXISTS import_state VARCHAR(32) NOT NULL DEFAULT 'unlinked';

-- +migrate Dialect sqlite
CREATE TABLE external_agent_bindings_previous (
    id VARCHAR(36) PRIMARY KEY,
    conversation_id VARCHAR(36) NOT NULL,
    provider VARCHAR(32) NOT NULL,
    provider_thread_id VARCHAR(128) NOT NULL,
    managed_by_lazymind BOOLEAN NOT NULL DEFAULT FALSE,
    created_by_user_id VARCHAR(255) NOT NULL,
    created_at DATETIME NOT NULL,
    updated_at DATETIME NOT NULL,
    CONSTRAINT uk_external_agent_binding_conversation_provider UNIQUE (conversation_id, provider),
    CONSTRAINT uk_external_agent_binding_thread UNIQUE (provider, provider_thread_id)
);
INSERT INTO external_agent_bindings_previous
SELECT id, conversation_id, provider, provider_thread_id, FALSE,
       created_by_user_id, created_at, updated_at
FROM external_agent_bindings;
DROP TABLE external_agent_bindings;
ALTER TABLE external_agent_bindings_previous RENAME TO external_agent_bindings;

CREATE TABLE external_agent_sessions_previous (
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
INSERT INTO external_agent_sessions_previous
SELECT id, owner_user_id, provider, provider_thread_id, project_key, project_name,
       display_name, FALSE, 'unlinked', turn_count, active,
       native_updated_at, last_seen_at, created_at, updated_at
FROM external_agent_sessions;
DROP TABLE external_agent_sessions;
ALTER TABLE external_agent_sessions_previous RENAME TO external_agent_sessions;
CREATE INDEX idx_external_agent_session_catalog
    ON external_agent_sessions(owner_user_id, provider, active, used_lazymind, import_state, native_updated_at DESC);
CREATE INDEX idx_external_agent_session_last_seen ON external_agent_sessions(last_seen_at);
