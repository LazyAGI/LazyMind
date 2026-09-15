CREATE TABLE conversation_tool_grants (
    conversation_id VARCHAR(36) NOT NULL REFERENCES conversations(id) ON DELETE CASCADE,
    capability VARCHAR(32) NOT NULL CHECK (capability = 'shell'),
    create_user_id VARCHAR(255) NOT NULL,
    created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (conversation_id, capability)
);
