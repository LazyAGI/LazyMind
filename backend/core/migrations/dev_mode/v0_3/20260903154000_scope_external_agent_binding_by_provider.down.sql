-- +migrate Dialect postgres
DELETE FROM external_agent_bindings
WHERE id NOT IN (
    SELECT DISTINCT ON (conversation_id) id
    FROM external_agent_bindings
    ORDER BY conversation_id, updated_at DESC, id DESC
);
DROP INDEX IF EXISTS uk_external_agent_binding_conversation_provider;
ALTER TABLE external_agent_bindings
    ADD CONSTRAINT uk_external_agent_binding_conversation UNIQUE (conversation_id);

-- +migrate Dialect sqlite
CREATE TABLE external_agent_bindings_previous (
    id VARCHAR(36) PRIMARY KEY,
    conversation_id VARCHAR(36) NOT NULL,
    provider VARCHAR(32) NOT NULL,
    host_id VARCHAR(128) NOT NULL,
    provider_thread_id VARCHAR(128) NOT NULL,
    managed_by_lazymind BOOLEAN NOT NULL DEFAULT FALSE,
    created_by_user_id VARCHAR(255) NOT NULL,
    created_at DATETIME NOT NULL,
    updated_at DATETIME NOT NULL,
    CONSTRAINT uk_external_agent_binding_conversation UNIQUE (conversation_id),
    CONSTRAINT uk_external_agent_binding_thread UNIQUE (provider, host_id, provider_thread_id)
);
INSERT INTO external_agent_bindings_previous (
    id, conversation_id, provider, host_id, provider_thread_id,
    managed_by_lazymind, created_by_user_id, created_at, updated_at
)
SELECT
    current.id, current.conversation_id, current.provider, current.host_id,
    current.provider_thread_id, current.managed_by_lazymind,
    current.created_by_user_id, current.created_at, current.updated_at
FROM external_agent_bindings AS current
WHERE NOT EXISTS (
    SELECT 1
    FROM external_agent_bindings AS newer
    WHERE newer.conversation_id = current.conversation_id
      AND (newer.updated_at > current.updated_at OR
           (newer.updated_at = current.updated_at AND newer.id > current.id))
);
DROP TABLE external_agent_bindings;
ALTER TABLE external_agent_bindings_previous RENAME TO external_agent_bindings;
