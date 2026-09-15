-- +migrate Dialect postgres
ALTER TABLE conversations ADD COLUMN title_source VARCHAR(16) NOT NULL DEFAULT 'unknown';
ALTER TABLE conversations ADD COLUMN title_revision BIGINT NOT NULL DEFAULT 0;
CREATE TABLE conversation_opening_metadata (
 conversation_id VARCHAR(36) PRIMARY KEY REFERENCES conversations(id) ON DELETE CASCADE,
 user_id VARCHAR(255) NOT NULL,
 summary TEXT NOT NULL DEFAULT '', intent_status VARCHAR(16) NOT NULL DEFAULT '', missing_context JSON,
 input_json JSON NOT NULL, source_history_ids JSON NOT NULL,
 source_hash VARCHAR(64) NOT NULL, evidence_hash VARCHAR(64) NOT NULL, opening_turns INTEGER NOT NULL,
 seed_revision BIGINT NOT NULL DEFAULT 1, metadata_revision BIGINT NOT NULL DEFAULT 0,
 title_revision BIGINT NOT NULL DEFAULT 0, generator_version VARCHAR(32) NOT NULL,
 generation_count INTEGER NOT NULL DEFAULT 0, call_count INTEGER NOT NULL DEFAULT 0,
 window_closed BOOLEAN NOT NULL DEFAULT FALSE, status VARCHAR(16) NOT NULL,
 error_code VARCHAR(64) NOT NULL DEFAULT '', model_id JSON, usage_json JSON,
 job_id VARCHAR(64) NOT NULL DEFAULT '', backfill_id VARCHAR(64) NOT NULL DEFAULT '',
 updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX idx_opening_user_status ON conversation_opening_metadata(user_id, status);
CREATE INDEX idx_opening_backfill ON conversation_opening_metadata(backfill_id);
CREATE TABLE conversation_opening_backfills (
 id VARCHAR(64) PRIMARY KEY, user_id VARCHAR(255) NOT NULL UNIQUE,
 version VARCHAR(32) NOT NULL, status VARCHAR(16) NOT NULL,
 cursor_time TIMESTAMP, cursor_id VARCHAR(36) NOT NULL DEFAULT '',
 scanned BIGINT NOT NULL DEFAULT 0, skipped BIGINT NOT NULL DEFAULT 0,
 scan_complete BOOLEAN NOT NULL DEFAULT FALSE,
 created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP, updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);

-- +migrate Dialect sqlite
ALTER TABLE conversations ADD COLUMN title_source VARCHAR(16) NOT NULL DEFAULT 'unknown';
ALTER TABLE conversations ADD COLUMN title_revision BIGINT NOT NULL DEFAULT 0;
CREATE TABLE conversation_opening_metadata (
 conversation_id VARCHAR(36) PRIMARY KEY REFERENCES conversations(id) ON DELETE CASCADE,
 user_id VARCHAR(255) NOT NULL,
 summary TEXT NOT NULL DEFAULT '', intent_status VARCHAR(16) NOT NULL DEFAULT '', missing_context JSON,
 input_json JSON NOT NULL, source_history_ids JSON NOT NULL,
 source_hash VARCHAR(64) NOT NULL, evidence_hash VARCHAR(64) NOT NULL, opening_turns INTEGER NOT NULL,
 seed_revision BIGINT NOT NULL DEFAULT 1, metadata_revision BIGINT NOT NULL DEFAULT 0,
 title_revision BIGINT NOT NULL DEFAULT 0, generator_version VARCHAR(32) NOT NULL,
 generation_count INTEGER NOT NULL DEFAULT 0, call_count INTEGER NOT NULL DEFAULT 0,
 window_closed BOOLEAN NOT NULL DEFAULT FALSE, status VARCHAR(16) NOT NULL,
 error_code VARCHAR(64) NOT NULL DEFAULT '', model_id JSON, usage_json JSON,
 job_id VARCHAR(64) NOT NULL DEFAULT '', backfill_id VARCHAR(64) NOT NULL DEFAULT '',
 updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX idx_opening_user_status ON conversation_opening_metadata(user_id, status);
CREATE INDEX idx_opening_backfill ON conversation_opening_metadata(backfill_id);
CREATE TABLE conversation_opening_backfills (
 id VARCHAR(64) PRIMARY KEY, user_id VARCHAR(255) NOT NULL UNIQUE,
 version VARCHAR(32) NOT NULL, status VARCHAR(16) NOT NULL,
 cursor_time TIMESTAMP, cursor_id VARCHAR(36) NOT NULL DEFAULT '',
 scanned BIGINT NOT NULL DEFAULT 0, skipped BIGINT NOT NULL DEFAULT 0,
 scan_complete BOOLEAN NOT NULL DEFAULT FALSE,
 created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP, updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);
