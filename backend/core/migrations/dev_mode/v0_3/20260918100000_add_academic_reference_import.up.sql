-- +migrate Dialect *
CREATE TABLE IF NOT EXISTS academic_works (
  id VARCHAR(36) PRIMARY KEY, canonical_title TEXT NOT NULL, normalized_title TEXT NOT NULL,
  authors_json JSON NOT NULL, first_author_normalized VARCHAR(255) NOT NULL DEFAULT '',
  publication_year INTEGER NOT NULL DEFAULT 0, venue TEXT NOT NULL DEFAULT '', abstract TEXT NOT NULL DEFAULT '',
  doi_normalized VARCHAR(512) NOT NULL DEFAULT '', arxiv_id_base VARCHAR(64) NOT NULL DEFAULT '',
  external_ids_json JSON NOT NULL, metadata_provenance_json JSON NOT NULL,
  resolution_confidence DOUBLE PRECISION NOT NULL DEFAULT 0,
  created_at TIMESTAMP NOT NULL, updated_at TIMESTAMP NOT NULL
);
CREATE UNIQUE INDEX IF NOT EXISTS uk_academic_works_doi ON academic_works(doi_normalized) WHERE doi_normalized <> '';
CREATE UNIQUE INDEX IF NOT EXISTS uk_academic_works_arxiv ON academic_works(arxiv_id_base) WHERE arxiv_id_base <> '';
CREATE INDEX IF NOT EXISTS idx_academic_works_title ON academic_works(normalized_title);

CREATE TABLE IF NOT EXISTS academic_work_documents (
  academic_work_id VARCHAR(36) NOT NULL, dataset_id VARCHAR(255) NOT NULL, document_id VARCHAR(128) NOT NULL,
  version_kind VARCHAR(32) NOT NULL DEFAULT 'unknown', source_provider VARCHAR(64) NOT NULL DEFAULT '',
  source_locator TEXT NOT NULL DEFAULT '', source_version VARCHAR(64) NOT NULL DEFAULT '',
  content_sha256 VARCHAR(64) NOT NULL DEFAULT '', match_method VARCHAR(64) NOT NULL DEFAULT '',
  match_confidence DOUBLE PRECISION NOT NULL DEFAULT 0, is_preferred_version BOOLEAN NOT NULL DEFAULT FALSE,
  created_at TIMESTAMP NOT NULL, updated_at TIMESTAMP NOT NULL,
  PRIMARY KEY(academic_work_id, dataset_id, document_id)
);
CREATE INDEX IF NOT EXISTS idx_academic_work_documents_dataset ON academic_work_documents(dataset_id);
CREATE INDEX IF NOT EXISTS idx_academic_work_documents_document ON academic_work_documents(document_id);
CREATE INDEX IF NOT EXISTS idx_academic_work_documents_hash ON academic_work_documents(content_sha256);

CREATE TABLE IF NOT EXISTS academic_references (
  id VARCHAR(36) PRIMARY KEY, source_document_id VARCHAR(128) NOT NULL, source_work_id VARCHAR(36) NOT NULL DEFAULT '',
  reference_key VARCHAR(64) NOT NULL DEFAULT '', raw_text TEXT NOT NULL, title TEXT NOT NULL DEFAULT '',
  authors_json JSON NOT NULL, publication_year INTEGER NOT NULL DEFAULT 0,
  doi_normalized VARCHAR(512) NOT NULL DEFAULT '', arxiv_id_base VARCHAR(64) NOT NULL DEFAULT '',
  resolved_work_id VARCHAR(36) NOT NULL DEFAULT '', resolution_status VARCHAR(32) NOT NULL DEFAULT 'unresolved',
  resolution_method VARCHAR(64) NOT NULL DEFAULT '', resolution_confidence DOUBLE PRECISION NOT NULL DEFAULT 0,
  page INTEGER NOT NULL DEFAULT 0, bbox_json JSON NOT NULL, segment_ids_json JSON NOT NULL,
  extractor_name VARCHAR(128) NOT NULL DEFAULT '', extractor_version VARCHAR(64) NOT NULL DEFAULT '',
  source_fingerprint VARCHAR(128) NOT NULL DEFAULT '', created_at TIMESTAMP NOT NULL, updated_at TIMESTAMP NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_academic_references_source ON academic_references(source_document_id);
CREATE INDEX IF NOT EXISTS idx_academic_references_work ON academic_references(resolved_work_id);
CREATE INDEX IF NOT EXISTS idx_academic_references_doi ON academic_references(doi_normalized);
CREATE INDEX IF NOT EXISTS idx_academic_references_arxiv ON academic_references(arxiv_id_base);

CREATE TABLE IF NOT EXISTS paper_import_batches (
  id VARCHAR(36) PRIMARY KEY, entry_type VARCHAR(32) NOT NULL, target_dataset_id VARCHAR(255) NOT NULL,
  target_pid VARCHAR(255) NOT NULL DEFAULT '', source_document_ids_json JSON NOT NULL, policy_snapshot_json JSON NOT NULL,
  status VARCHAR(32) NOT NULL, total_items INTEGER NOT NULL DEFAULT 0, completed_items INTEGER NOT NULL DEFAULT 0,
  failed_items INTEGER NOT NULL DEFAULT 0, skipped_items INTEGER NOT NULL DEFAULT 0, needs_action_items INTEGER NOT NULL DEFAULT 0,
  async_job_id VARCHAR(36) NOT NULL DEFAULT '', idempotency_key VARCHAR(128) NOT NULL DEFAULT '',
  created_by VARCHAR(255) NOT NULL, created_at TIMESTAMP NOT NULL, updated_at TIMESTAMP NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_paper_import_batches_target ON paper_import_batches(target_dataset_id);
CREATE INDEX IF NOT EXISTS idx_paper_import_batches_status ON paper_import_batches(status);
CREATE INDEX IF NOT EXISTS idx_paper_import_batches_user ON paper_import_batches(created_by);
CREATE UNIQUE INDEX IF NOT EXISTS uk_paper_import_batches_idempotency ON paper_import_batches(created_by,idempotency_key) WHERE idempotency_key <> '';

CREATE TABLE IF NOT EXISTS paper_import_items (
  id VARCHAR(36) PRIMARY KEY, batch_id VARCHAR(36) NOT NULL, academic_work_id VARCHAR(36) NOT NULL,
  reference_ids_json JSON NOT NULL, presence_snapshot_json JSON NOT NULL, selected_candidate_json JSON NOT NULL,
  stage VARCHAR(32) NOT NULL, status VARCHAR(32) NOT NULL, content_sha256 VARCHAR(64) NOT NULL DEFAULT '',
  document_id VARCHAR(128) NOT NULL DEFAULT '', document_task_id VARCHAR(128) NOT NULL DEFAULT '',
  attempt_count INTEGER NOT NULL DEFAULT 0, error_code VARCHAR(64) NOT NULL DEFAULT '', error_details_json JSON NOT NULL,
  created_at TIMESTAMP NOT NULL, updated_at TIMESTAMP NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_paper_import_items_batch ON paper_import_items(batch_id);
CREATE INDEX IF NOT EXISTS idx_paper_import_items_work ON paper_import_items(academic_work_id);
CREATE INDEX IF NOT EXISTS idx_paper_import_items_status ON paper_import_items(status);
