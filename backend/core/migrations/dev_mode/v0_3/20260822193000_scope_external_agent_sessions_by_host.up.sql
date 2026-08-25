-- +migrate Dialect postgres
ALTER TABLE external_agent_bindings ADD COLUMN IF NOT EXISTS host_id VARCHAR(128) NOT NULL DEFAULT 'host-legacy';
ALTER TABLE external_agent_sessions ADD COLUMN IF NOT EXISTS host_id VARCHAR(128) NOT NULL DEFAULT 'host-legacy';
ALTER TABLE external_agent_bindings DROP COLUMN IF EXISTS managed_by_lazymind;
ALTER TABLE external_agent_sessions DROP COLUMN IF EXISTS used_lazymind;
ALTER TABLE external_agent_sessions DROP COLUMN IF EXISTS import_state;

ALTER TABLE external_agent_bindings DROP CONSTRAINT IF EXISTS uk_external_agent_binding_conversation_provider;
ALTER TABLE external_agent_bindings DROP CONSTRAINT IF EXISTS uk_external_agent_binding_conversation;
ALTER TABLE external_agent_bindings DROP CONSTRAINT IF EXISTS uk_external_agent_binding_thread;
DROP INDEX IF EXISTS uk_external_agent_binding_conversation_provider;
DROP INDEX IF EXISTS uk_external_agent_binding_conversation;
DROP INDEX IF EXISTS uk_external_agent_binding_thread;
CREATE UNIQUE INDEX uk_external_agent_binding_conversation ON external_agent_bindings (conversation_id);
CREATE UNIQUE INDEX uk_external_agent_binding_thread ON external_agent_bindings (provider, host_id, provider_thread_id);

ALTER TABLE external_agent_sessions DROP CONSTRAINT IF EXISTS uk_external_agent_session;
DROP INDEX IF EXISTS uk_external_agent_session;
CREATE UNIQUE INDEX uk_external_agent_session ON external_agent_sessions (owner_user_id, provider, host_id, provider_thread_id);
DROP INDEX IF EXISTS idx_external_agent_session_catalog;
CREATE INDEX idx_external_agent_session_catalog
    ON external_agent_sessions(owner_user_id, provider, host_id, active, native_updated_at DESC);

-- +migrate Dialect sqlite
CREATE TABLE external_agent_bindings_next (
    id VARCHAR(36) PRIMARY KEY,
    conversation_id VARCHAR(36) NOT NULL,
    provider VARCHAR(32) NOT NULL,
    host_id VARCHAR(128) NOT NULL,
    provider_thread_id VARCHAR(128) NOT NULL,
    created_by_user_id VARCHAR(255) NOT NULL,
    created_at DATETIME NOT NULL,
    updated_at DATETIME NOT NULL,
    CONSTRAINT uk_external_agent_binding_conversation UNIQUE (conversation_id),
    CONSTRAINT uk_external_agent_binding_thread UNIQUE (provider, host_id, provider_thread_id)
);
INSERT INTO external_agent_bindings_next (
    id, conversation_id, provider, host_id, provider_thread_id,
    created_by_user_id, created_at, updated_at
)
SELECT
    id, conversation_id, provider, 'host-legacy', provider_thread_id,
    created_by_user_id, created_at, updated_at
FROM external_agent_bindings;
DROP TABLE external_agent_bindings;
ALTER TABLE external_agent_bindings_next RENAME TO external_agent_bindings;

CREATE TABLE external_agent_sessions_next (
    id VARCHAR(36) PRIMARY KEY,
    owner_user_id VARCHAR(255) NOT NULL,
    provider VARCHAR(32) NOT NULL,
    host_id VARCHAR(128) NOT NULL,
    provider_thread_id VARCHAR(128) NOT NULL,
    project_key VARCHAR(128) NOT NULL DEFAULT '',
    project_name VARCHAR(200) NOT NULL DEFAULT '',
    display_name VARCHAR(255) NOT NULL DEFAULT '',
    turn_count INTEGER NOT NULL DEFAULT 0,
    active BOOLEAN NOT NULL DEFAULT TRUE,
    native_updated_at DATETIME,
    last_seen_at DATETIME NOT NULL,
    created_at DATETIME NOT NULL,
    updated_at DATETIME NOT NULL,
    CONSTRAINT uk_external_agent_session UNIQUE (owner_user_id, provider, host_id, provider_thread_id)
);
INSERT INTO external_agent_sessions_next (
    id, owner_user_id, provider, host_id, provider_thread_id,
    project_key, project_name, display_name,
    turn_count, active, native_updated_at, last_seen_at, created_at, updated_at
)
SELECT
    id, owner_user_id, provider, 'host-legacy', provider_thread_id,
    project_key, project_name, display_name,
    turn_count, active, native_updated_at, last_seen_at, created_at, updated_at
FROM external_agent_sessions;
DROP TABLE external_agent_sessions;
ALTER TABLE external_agent_sessions_next RENAME TO external_agent_sessions;
CREATE INDEX idx_external_agent_session_catalog
    ON external_agent_sessions(owner_user_id, provider, host_id, active, native_updated_at DESC);
CREATE INDEX idx_external_agent_session_last_seen ON external_agent_sessions(last_seen_at);
