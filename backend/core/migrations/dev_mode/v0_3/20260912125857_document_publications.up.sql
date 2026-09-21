-- +migrate Dialect postgres
CREATE TABLE IF NOT EXISTS document_publication_operations (
 id VARCHAR(64) PRIMARY KEY, owner_user_id VARCHAR(255) NOT NULL,
 idempotency_key VARCHAR(128) NOT NULL, session_id VARCHAR(64) NOT NULL,
 slot_id VARCHAR(255) NOT NULL, item_index INTEGER NOT NULL,
 status VARCHAR(32) NOT NULL, source_revision_id VARCHAR(64) NOT NULL,
 source_revision INTEGER NOT NULL DEFAULT 0, source_draft_version BIGINT NOT NULL DEFAULT 0,
 source_schema TEXT NOT NULL DEFAULT '', source_content_type TEXT NOT NULL DEFAULT '',
 source_hash VARCHAR(64) NOT NULL DEFAULT '', source_value JSON,
 request_hash VARCHAR(64) NOT NULL DEFAULT '', provider VARCHAR(64) NOT NULL DEFAULT '',
 title TEXT NOT NULL DEFAULT '', parent_uri TEXT NOT NULL DEFAULT '', template TEXT NOT NULL DEFAULT '',
 allow_bound BOOLEAN NOT NULL DEFAULT FALSE, shared_target BOOLEAN NOT NULL DEFAULT FALSE,
 target_document JSON, remote_value JSON, candidate_value JSON, media_assets JSON, receipt_json JSON,
 result_revision_id VARCHAR(64) NOT NULL DEFAULT '', error_code VARCHAR(64) NOT NULL DEFAULT '',
 created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP, updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_document_publication_key ON document_publication_operations(owner_user_id,idempotency_key);
CREATE INDEX IF NOT EXISTS idx_document_publication_session ON document_publication_operations(session_id);
CREATE TABLE IF NOT EXISTS document_publication_bindings (
 id VARCHAR(64) PRIMARY KEY, session_id VARCHAR(64) NOT NULL, slot_id VARCHAR(255) NOT NULL,
 item_index INTEGER NOT NULL, owner_user_id VARCHAR(255) NOT NULL,
 pending_operation_id VARCHAR(64) NOT NULL DEFAULT '', provider VARCHAR(64) NOT NULL DEFAULT '',
 target_document JSON, remote_value JSON, source_revision_id VARCHAR(64) NOT NULL DEFAULT '',
 result_revision_id VARCHAR(64) NOT NULL DEFAULT ''
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_document_publication_item ON document_publication_bindings(session_id,slot_id,item_index);

-- +migrate Dialect sqlite
CREATE TABLE IF NOT EXISTS document_publication_operations (
 id VARCHAR(64) PRIMARY KEY, owner_user_id VARCHAR(255) NOT NULL,
 idempotency_key VARCHAR(128) NOT NULL, session_id VARCHAR(64) NOT NULL,
 slot_id VARCHAR(255) NOT NULL, item_index INTEGER NOT NULL,
 status VARCHAR(32) NOT NULL, source_revision_id VARCHAR(64) NOT NULL,
 source_revision INTEGER NOT NULL DEFAULT 0, source_draft_version BIGINT NOT NULL DEFAULT 0,
 source_schema TEXT NOT NULL DEFAULT '', source_content_type TEXT NOT NULL DEFAULT '',
 source_hash VARCHAR(64) NOT NULL DEFAULT '', source_value JSON,
 request_hash VARCHAR(64) NOT NULL DEFAULT '', provider VARCHAR(64) NOT NULL DEFAULT '',
 title TEXT NOT NULL DEFAULT '', parent_uri TEXT NOT NULL DEFAULT '', template TEXT NOT NULL DEFAULT '',
 allow_bound BOOLEAN NOT NULL DEFAULT FALSE, shared_target BOOLEAN NOT NULL DEFAULT FALSE,
 target_document JSON, remote_value JSON, candidate_value JSON, media_assets JSON, receipt_json JSON,
 result_revision_id VARCHAR(64) NOT NULL DEFAULT '', error_code VARCHAR(64) NOT NULL DEFAULT '',
 created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP, updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_document_publication_key ON document_publication_operations(owner_user_id,idempotency_key);
CREATE INDEX IF NOT EXISTS idx_document_publication_session ON document_publication_operations(session_id);
CREATE TABLE IF NOT EXISTS document_publication_bindings (
 id VARCHAR(64) PRIMARY KEY, session_id VARCHAR(64) NOT NULL, slot_id VARCHAR(255) NOT NULL,
 item_index INTEGER NOT NULL, owner_user_id VARCHAR(255) NOT NULL,
 pending_operation_id VARCHAR(64) NOT NULL DEFAULT '', provider VARCHAR(64) NOT NULL DEFAULT '',
 target_document JSON, remote_value JSON, source_revision_id VARCHAR(64) NOT NULL DEFAULT '',
 result_revision_id VARCHAR(64) NOT NULL DEFAULT ''
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_document_publication_item ON document_publication_bindings(session_id,slot_id,item_index);
