CREATE TABLE IF NOT EXISTS tool_configuration_actions (
    id VARCHAR(64) PRIMARY KEY,
    user_id VARCHAR(255) NOT NULL,
    conversation_id VARCHAR(36) NOT NULL,
    history_id VARCHAR(36) NOT NULL,
    run_id VARCHAR(64) NOT NULL,
    service VARCHAR(255) NOT NULL,
    label VARCHAR(255) NOT NULL,
    status VARCHAR(64) NOT NULL,
    revision VARCHAR(64) NOT NULL DEFAULT '',
    version BIGINT NOT NULL DEFAULT 1,
    delivered_version BIGINT NOT NULL DEFAULT 0,
    request_id VARCHAR(64) NOT NULL DEFAULT '',
    created_at TIMESTAMP NOT NULL,
    updated_at TIMESTAMP NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_tool_configuration_owner ON tool_configuration_actions(user_id, conversation_id);

ALTER TABLE mcp_servers ADD COLUMN discovery_enabled BOOLEAN NOT NULL DEFAULT FALSE;
UPDATE mcp_servers SET discovery_enabled = TRUE WHERE enabled = TRUE;
