-- +migrate Dialect postgres
DELETE FROM conversation_tool_grants WHERE capability <> 'shell';
ALTER TABLE conversation_tool_grants DROP CONSTRAINT conversation_tool_grants_capability_check;
ALTER TABLE conversation_tool_grants ALTER COLUMN capability TYPE VARCHAR(32);
ALTER TABLE conversation_tool_grants ADD CONSTRAINT conversation_tool_grants_capability_check CHECK (capability = 'shell');

-- +migrate Dialect sqlite
CREATE TABLE conversation_tool_grants_next (
    conversation_id VARCHAR(36) NOT NULL REFERENCES conversations(id) ON DELETE CASCADE,
    capability VARCHAR(32) NOT NULL CHECK (capability = 'shell'),
    create_user_id VARCHAR(255) NOT NULL,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (conversation_id, capability)
);
INSERT INTO conversation_tool_grants_next (conversation_id, capability, create_user_id, created_at)
SELECT conversation_id, capability, create_user_id, created_at FROM conversation_tool_grants WHERE capability = 'shell';
DROP TABLE conversation_tool_grants;
ALTER TABLE conversation_tool_grants_next RENAME TO conversation_tool_grants;
