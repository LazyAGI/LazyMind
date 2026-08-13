-- +migrate Dialect postgres
ALTER TABLE external_agent_operations
    DROP COLUMN request_hash;

-- +migrate Dialect sqlite
CREATE TABLE external_agent_operations_next (
    id VARCHAR(36) PRIMARY KEY,
    actor_user_id VARCHAR(255) NOT NULL,
    operation_id VARCHAR(255) NOT NULL,
    kind VARCHAR(64) NOT NULL,
    status VARCHAR(32) NOT NULL,
    result TEXT,
    created_at DATETIME NOT NULL,
    updated_at DATETIME NOT NULL,
    CONSTRAINT uk_external_agent_operation UNIQUE
        (actor_user_id, operation_id, kind)
);
INSERT INTO external_agent_operations_next(
    id, actor_user_id, operation_id, kind,
    status, result, created_at, updated_at
)
SELECT
    id, actor_user_id, operation_id, kind,
    status, result, created_at, updated_at
FROM external_agent_operations;
DROP TABLE external_agent_operations;
ALTER TABLE external_agent_operations_next RENAME TO external_agent_operations;
