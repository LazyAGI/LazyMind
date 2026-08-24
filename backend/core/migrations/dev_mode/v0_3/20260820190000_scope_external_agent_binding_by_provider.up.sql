-- +migrate Dialect postgres
ALTER TABLE external_agent_bindings
    DROP CONSTRAINT IF EXISTS uk_external_agent_binding_conversation;

DROP INDEX IF EXISTS uk_external_agent_binding_conversation;

CREATE UNIQUE INDEX IF NOT EXISTS uk_external_agent_binding_conversation_provider
    ON external_agent_bindings (conversation_id, provider);

-- +migrate Dialect sqlite
CREATE TABLE external_agent_bindings_next (
    id VARCHAR(36) PRIMARY KEY,
    conversation_id VARCHAR(36) NOT NULL,
    provider VARCHAR(32) NOT NULL,
    provider_thread_id VARCHAR(128) NOT NULL,
    managed_by_lazymind BOOLEAN NOT NULL DEFAULT FALSE,
    created_by_user_id VARCHAR(255) NOT NULL,
    created_at DATETIME NOT NULL,
    updated_at DATETIME NOT NULL,
    CONSTRAINT uk_external_agent_binding_conversation_provider
        UNIQUE (conversation_id, provider),
    CONSTRAINT uk_external_agent_binding_thread
        UNIQUE (provider, provider_thread_id)
);

INSERT INTO external_agent_bindings_next (
    id, conversation_id, provider, provider_thread_id,
    managed_by_lazymind, created_by_user_id, created_at, updated_at
)
SELECT
    id, conversation_id, provider, provider_thread_id,
    managed_by_lazymind, created_by_user_id, created_at, updated_at
FROM external_agent_bindings;

DROP TABLE external_agent_bindings;
ALTER TABLE external_agent_bindings_next RENAME TO external_agent_bindings;
