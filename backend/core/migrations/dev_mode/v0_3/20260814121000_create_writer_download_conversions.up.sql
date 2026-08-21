-- +migrate Dialect postgres
CREATE TABLE IF NOT EXISTS writer_download_conversions (
    id VARCHAR(36) PRIMARY KEY,
    user_id VARCHAR(255) NOT NULL,
    source_hash VARCHAR(64) NOT NULL,
    target_format VARCHAR(16) NOT NULL,
    filename VARCHAR(255) NOT NULL,
    mime_type VARCHAR(128) NOT NULL,
    storage_path VARCHAR(1024) NOT NULL,
    size BIGINT NOT NULL,
    content_hash VARCHAR(64) NOT NULL,
    created_at TIMESTAMP NOT NULL,
    updated_at TIMESTAMP NOT NULL,
    CONSTRAINT uk_writer_download_conversion UNIQUE (user_id, source_hash, target_format)
);
CREATE INDEX IF NOT EXISTS idx_writer_download_conversions_user_updated
    ON writer_download_conversions(user_id, updated_at);

-- +migrate Dialect sqlite
CREATE TABLE IF NOT EXISTS writer_download_conversions (
    id TEXT PRIMARY KEY,
    user_id TEXT NOT NULL,
    source_hash TEXT NOT NULL,
    target_format TEXT NOT NULL,
    filename TEXT NOT NULL,
    mime_type TEXT NOT NULL,
    storage_path TEXT NOT NULL,
    size INTEGER NOT NULL,
    content_hash TEXT NOT NULL,
    created_at DATETIME NOT NULL,
    updated_at DATETIME NOT NULL,
    UNIQUE(user_id, source_hash, target_format)
);
CREATE INDEX IF NOT EXISTS idx_writer_download_conversions_user_updated
    ON writer_download_conversions(user_id, updated_at);
