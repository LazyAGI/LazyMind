CREATE TABLE paper_guides (
 id VARCHAR(80) PRIMARY KEY, scope_key VARCHAR(64) NOT NULL UNIQUE, owner_id VARCHAR(128) NOT NULL,
 dataset_id VARCHAR(128) NOT NULL, document_id VARCHAR(128) NOT NULL, language VARCHAR(8) NOT NULL,
 published_revision_id TEXT NOT NULL DEFAULT '', candidate_revision_id TEXT NOT NULL DEFAULT '',
 epoch BIGINT NOT NULL DEFAULT 0, change_version BIGINT NOT NULL DEFAULT 0, deleted BOOLEAN NOT NULL DEFAULT FALSE,
 created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP, updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX idx_paper_guides_document ON paper_guides(dataset_id, document_id);
CREATE INDEX idx_paper_guides_owner ON paper_guides(owner_id);
CREATE TABLE paper_guide_revisions (
 id VARCHAR(80) PRIMARY KEY, guide_id VARCHAR(80) NOT NULL, job_id TEXT NOT NULL DEFAULT '', epoch BIGINT NOT NULL DEFAULT 0,
 idempotency_key TEXT NOT NULL DEFAULT '', source_sha256 TEXT NOT NULL DEFAULT '', ir_digest TEXT NOT NULL DEFAULT '', snapshot_path TEXT NOT NULL DEFAULT '',
 model_json TEXT NOT NULL DEFAULT '{}', fingerprint TEXT NOT NULL DEFAULT '', status TEXT NOT NULL DEFAULT 'draft', stage TEXT NOT NULL DEFAULT 'queued',
 error_code TEXT NOT NULL DEFAULT '', dependency_id TEXT NOT NULL DEFAULT '', wait_count INTEGER NOT NULL DEFAULT 0,
 calls_used INTEGER NOT NULL DEFAULT 0, planned_calls INTEGER NOT NULL DEFAULT 0, extract_calls INTEGER NOT NULL DEFAULT 0, recovery_calls INTEGER NOT NULL DEFAULT 0,
 correction_count INTEGER NOT NULL DEFAULT 0, active_millis BIGINT NOT NULL DEFAULT 0, change_version BIGINT NOT NULL DEFAULT 0,
 created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP, updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX idx_paper_guide_revisions_guide ON paper_guide_revisions(guide_id);
CREATE TABLE paper_guide_steps (
 id VARCHAR(80) PRIMARY KEY, revision_id VARCHAR(80) NOT NULL, step_key VARCHAR(160) NOT NULL, input_hash TEXT NOT NULL DEFAULT '',
 output_json TEXT NOT NULL DEFAULT '', review_json TEXT NOT NULL DEFAULT '', epoch BIGINT NOT NULL DEFAULT 0,
 version BIGINT NOT NULL DEFAULT 0, published BOOLEAN NOT NULL DEFAULT FALSE, created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
 CONSTRAINT idx_paper_guide_step_key UNIQUE(revision_id, step_key)
);
CREATE TABLE paper_guide_progress (
 owner_id VARCHAR(128) NOT NULL, revision_id VARCHAR(80) NOT NULL, section_key TEXT NOT NULL DEFAULT '', block_id TEXT NOT NULL DEFAULT '',
 "offset" INTEGER NOT NULL DEFAULT 0, mode TEXT NOT NULL DEFAULT 'article', updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
 PRIMARY KEY(owner_id, revision_id)
);
