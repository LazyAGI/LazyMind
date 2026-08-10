CREATE TABLE IF NOT EXISTS external_agent_operations (
    id VARCHAR(36) PRIMARY KEY,
    actor_user_id VARCHAR(255) NOT NULL,
    operation_id VARCHAR(255) NOT NULL,
    kind VARCHAR(64) NOT NULL,
    request_hash VARCHAR(64) NOT NULL DEFAULT '',
    status VARCHAR(32) NOT NULL,
    result JSONB,
    created_at TIMESTAMP NOT NULL,
    updated_at TIMESTAMP NOT NULL,
    CONSTRAINT uk_external_agent_operation UNIQUE
        (actor_user_id, operation_id, kind)
);

ALTER TABLE external_agent_operations
    ADD COLUMN IF NOT EXISTS request_hash VARCHAR(64) NOT NULL DEFAULT '';
