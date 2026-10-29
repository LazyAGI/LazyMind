-- Vocabulary cloud connections and durable outbound operations; portable PostgreSQL/SQLite SQL.
-- +migrate Dialect postgres,sqlite
CREATE TABLE IF NOT EXISTS vocabulary_cloud_connections (
    id VARCHAR(64) PRIMARY KEY,
    owner_id VARCHAR(64) NOT NULL,
    provider VARCHAR(32) NOT NULL,
    token_cipher TEXT NOT NULL,
    account_key VARCHAR(64) NOT NULL DEFAULT '',
    enabled BOOLEAN NOT NULL DEFAULT FALSE,
    target_id TEXT NOT NULL DEFAULT '',
    target_name TEXT NOT NULL DEFAULT '',
    add_to_study BOOLEAN NOT NULL DEFAULT FALSE,
    created_at TIMESTAMP NOT NULL,
    updated_at TIMESTAMP NOT NULL,
    UNIQUE(owner_id, provider)
);

CREATE TABLE IF NOT EXISTS vocabulary_cloud_operations (
    id VARCHAR(64) PRIMARY KEY,
    owner_id VARCHAR(64) NOT NULL,
    connection_id VARCHAR(64) NOT NULL,
    provider VARCHAR(32) NOT NULL,
    word_id VARCHAR(64) NOT NULL,
    term TEXT NOT NULL,
    target_id TEXT NOT NULL,
    add_to_study BOOLEAN NOT NULL DEFAULT FALSE,
    status VARCHAR(32) NOT NULL DEFAULT 'pending',
    last_error TEXT NOT NULL DEFAULT '',
    job_id VARCHAR(64) NOT NULL DEFAULT '',
    created_at TIMESTAMP NOT NULL,
    updated_at TIMESTAMP NOT NULL,
    UNIQUE(connection_id, word_id, target_id, add_to_study)
);
CREATE INDEX IF NOT EXISTS idx_vocabulary_cloud_operations_owner_updated
    ON vocabulary_cloud_operations(owner_id, updated_at);
