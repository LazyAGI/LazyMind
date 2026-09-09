CREATE TABLE IF NOT EXISTS conversation_fork_origins (
    conversation_id VARCHAR(36) PRIMARY KEY,
    source_conversation_id VARCHAR(36) NOT NULL,
    source_history_id VARCHAR(36) NOT NULL,
    source_seq INTEGER NOT NULL,
    source_history_revision VARCHAR(80) NOT NULL,
    source_prefix_revision VARCHAR(80) NOT NULL,
    source_title_snapshot VARCHAR(255) NOT NULL,
    forked_at TIMESTAMP NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_conversation_fork_origins_source_conversation_id ON conversation_fork_origins(source_conversation_id);
CREATE TABLE IF NOT EXISTS conversation_fork_requests (
    actor_user_id VARCHAR(255) NOT NULL,
    idempotency_key VARCHAR(128) NOT NULL,
    request_hash VARCHAR(80) NOT NULL,
    conversation_id VARCHAR(36) NOT NULL,
    created_at TIMESTAMP NOT NULL,
    PRIMARY KEY (actor_user_id, idempotency_key)
);
