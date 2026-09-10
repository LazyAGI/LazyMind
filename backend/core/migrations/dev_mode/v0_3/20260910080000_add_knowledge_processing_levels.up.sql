-- +migrate Dialect postgres
ALTER TABLE datasets ADD COLUMN IF NOT EXISTS processing_level VARCHAR(16) NOT NULL DEFAULT 'indexed';
ALTER TABLE datasets ADD COLUMN IF NOT EXISTS processing_revision BIGINT NOT NULL DEFAULT 1;
ALTER TABLE datasets ADD COLUMN IF NOT EXISTS transition_status VARCHAR(32) NOT NULL DEFAULT 'idle';
ALTER TABLE datasets ADD COLUMN IF NOT EXISTS reader_fallback_accepted BOOLEAN NOT NULL DEFAULT FALSE;
ALTER TABLE datasets ADD COLUMN IF NOT EXISTS processing_config JSON;

-- +migrate Dialect sqlite
ALTER TABLE datasets ADD COLUMN processing_level TEXT NOT NULL DEFAULT 'indexed';
ALTER TABLE datasets ADD COLUMN processing_revision INTEGER NOT NULL DEFAULT 1;
ALTER TABLE datasets ADD COLUMN transition_status TEXT NOT NULL DEFAULT 'idle';
ALTER TABLE datasets ADD COLUMN reader_fallback_accepted INTEGER NOT NULL DEFAULT 0;
ALTER TABLE datasets ADD COLUMN processing_config TEXT;

-- +migrate Dialect *
CREATE INDEX IF NOT EXISTS idx_datasets_processing_level ON datasets(processing_level);
CREATE TABLE IF NOT EXISTS document_processing_states (
  dataset_id VARCHAR(255) NOT NULL,
  document_id VARCHAR(128) NOT NULL,
  parse_status VARCHAR(16) NOT NULL DEFAULT 'pending',
  chunk_status VARCHAR(16) NOT NULL DEFAULT 'pending',
  index_status VARCHAR(16) NOT NULL DEFAULT 'pending',
  parse_error_code VARCHAR(64) NOT NULL DEFAULT '', parse_error_message TEXT NOT NULL DEFAULT '',
  chunk_error_code VARCHAR(64) NOT NULL DEFAULT '', chunk_error_message TEXT NOT NULL DEFAULT '',
  index_error_code VARCHAR(64) NOT NULL DEFAULT '', index_error_message TEXT NOT NULL DEFAULT '',
  source_fingerprint VARCHAR(128) NOT NULL DEFAULT '', parse_fingerprint VARCHAR(128) NOT NULL DEFAULT '',
  chunk_fingerprint VARCHAR(128) NOT NULL DEFAULT '', index_fingerprint VARCHAR(128) NOT NULL DEFAULT '',
  parser_version VARCHAR(128) NOT NULL DEFAULT '', chunker_version VARCHAR(128) NOT NULL DEFAULT '', embedding_version VARCHAR(128) NOT NULL DEFAULT '',
  parse_artifact_ref TEXT NOT NULL DEFAULT '', chunk_artifact_ref TEXT NOT NULL DEFAULT '', index_artifact_ref TEXT NOT NULL DEFAULT '',
  revision BIGINT NOT NULL DEFAULT 1, updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
  PRIMARY KEY(dataset_id, document_id)
);
CREATE INDEX IF NOT EXISTS idx_document_processing_status ON document_processing_states(dataset_id, parse_status, chunk_status, index_status);
