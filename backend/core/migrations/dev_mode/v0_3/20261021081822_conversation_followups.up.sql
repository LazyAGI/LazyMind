-- +migrate Dialect postgres,sqlite
CREATE TABLE conversation_followups (
    id VARCHAR(36) PRIMARY KEY,
    conversation_id VARCHAR(36) NOT NULL,
    content JSON NOT NULL,
    text TEXT NOT NULL,
    mode VARCHAR(16) NOT NULL DEFAULT 'queued',
    status VARCHAR(16) NOT NULL DEFAULT 'pending',
    sort_order BIGINT NOT NULL DEFAULT 0,
    revision BIGINT NOT NULL DEFAULT 1,
    target_run_id VARCHAR(64) NOT NULL DEFAULT '',
    target_history_id VARCHAR(36) NOT NULL DEFAULT '',
    steer_id VARCHAR(64) NOT NULL DEFAULT '',
    idempotency_key VARCHAR(64) NOT NULL,
    error_code VARCHAR(64) NOT NULL DEFAULT '',
    error_message TEXT NOT NULL DEFAULT '',
    accepted_at TIMESTAMP,
    create_user_id VARCHAR(255) NOT NULL,
    create_user_name VARCHAR(255) NOT NULL,
    created_at TIMESTAMP NOT NULL,
    updated_at TIMESTAMP NOT NULL,
    deleted_at TIMESTAMP
);

CREATE INDEX idx_conversation_followups_queue
    ON conversation_followups(conversation_id, status, sort_order);
CREATE UNIQUE INDEX uk_conversation_followups_idempotency
    ON conversation_followups(create_user_id, conversation_id, idempotency_key);
