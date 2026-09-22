-- Result receipts are independent of browser storage and deployment versions.
CREATE TABLE IF NOT EXISTS conversation_result_reads (
    user_id VARCHAR(255) NOT NULL,
    conversation_id VARCHAR(36) NOT NULL,
    terminal_version VARCHAR(64) NOT NULL,
    PRIMARY KEY (user_id, conversation_id, terminal_version)
);
CREATE TABLE IF NOT EXISTS conversation_result_read_state (
    id BIGINT NOT NULL PRIMARY KEY,
    initialized BOOLEAN NOT NULL DEFAULT FALSE
);
