-- Artifact V2 metadata baseline. Product write paths stay behind feature flags.
-- +migrate Dialect postgres
CREATE TABLE IF NOT EXISTS artifacts (
    id VARCHAR(36) PRIMARY KEY,
    tenant_id VARCHAR(128) NOT NULL,
    owner_user_id VARCHAR(255) NOT NULL,
    project_id VARCHAR(36),
    kind VARCHAR(32) NOT NULL,
    title VARCHAR(255) NOT NULL,
    logical_key VARCHAR(255),
    status VARCHAR(24) NOT NULL,
    classification VARCHAR(32) NOT NULL DEFAULT 'internal',
    created_at TIMESTAMP WITH TIME ZONE NOT NULL,
    updated_at TIMESTAMP WITH TIME ZONE NOT NULL,
    deleted_at TIMESTAMP WITH TIME ZONE
);
CREATE INDEX IF NOT EXISTS idx_artifacts_owner_created ON artifacts (tenant_id, owner_user_id, created_at);

CREATE TABLE IF NOT EXISTS artifact_blobs (
    id VARCHAR(64) PRIMARY KEY,
    tenant_id VARCHAR(128) NOT NULL,
    sha256 VARCHAR(64) NOT NULL,
    size BIGINT NOT NULL,
    mime_type VARCHAR(255) NOT NULL,
    storage_backend VARCHAR(32) NOT NULL,
    storage_key TEXT NOT NULL,
    encryption_key_ref VARCHAR(255),
    state VARCHAR(24) NOT NULL,
    created_at TIMESTAMP WITH TIME ZONE NOT NULL,
    CONSTRAINT uk_artifact_blobs_tenant_hash_size UNIQUE (tenant_id, sha256, size)
);

CREATE TABLE IF NOT EXISTS artifact_revisions (
    id VARCHAR(36) PRIMARY KEY,
    artifact_id VARCHAR(36) NOT NULL,
    revision_no BIGINT NOT NULL,
    parent_revision_id VARCHAR(36),
    merge_parent_revision_id VARCHAR(36),
    blob_id VARCHAR(64),
    inline_json JSONB,
    content_type VARCHAR(64) NOT NULL,
    schema_name VARCHAR(128),
    schema_version VARCHAR(32),
    content_hash VARCHAR(80) NOT NULL,
    size BIGINT NOT NULL,
    caption TEXT,
    metadata JSONB NOT NULL DEFAULT '{}'::jsonb,
    producer_type VARCHAR(32) NOT NULL,
    producer_id VARCHAR(128),
    producer_run_id VARCHAR(128),
    producer_event_id VARCHAR(128),
    created_by VARCHAR(255) NOT NULL,
    created_at TIMESTAMP WITH TIME ZONE NOT NULL,
    CONSTRAINT uk_artifact_revision_no UNIQUE (artifact_id, revision_no)
);
CREATE INDEX IF NOT EXISTS idx_artifact_revisions_producer ON artifact_revisions (producer_run_id, producer_event_id);

CREATE TABLE IF NOT EXISTS artifact_heads (
    artifact_id VARCHAR(36) NOT NULL,
    channel VARCHAR(32) NOT NULL,
    revision_id VARCHAR(36) NOT NULL,
    version BIGINT NOT NULL,
    updated_at TIMESTAMP WITH TIME ZONE NOT NULL,
    PRIMARY KEY (artifact_id, channel)
);

CREATE TABLE IF NOT EXISTS artifact_bindings (
    id VARCHAR(36) PRIMARY KEY,
    artifact_id VARCHAR(36) NOT NULL,
    revision_id VARCHAR(36),
    scope_type VARCHAR(32) NOT NULL,
    scope_id VARCHAR(128) NOT NULL,
    role VARCHAR(32) NOT NULL,
    slot_key VARCHAR(255),
    list_item_key VARCHAR(128),
    position INTEGER,
    validity VARCHAR(24) NOT NULL,
    follow_head BOOLEAN NOT NULL DEFAULT FALSE,
    created_at TIMESTAMP WITH TIME ZONE NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_artifact_bindings_scope ON artifact_bindings (scope_type, scope_id, role);

CREATE TABLE IF NOT EXISTS artifact_dependencies (
    output_revision_id VARCHAR(36) NOT NULL,
    input_revision_id VARCHAR(36) NOT NULL,
    role VARCHAR(64) NOT NULL,
    required BOOLEAN NOT NULL,
    PRIMARY KEY (output_revision_id, input_revision_id, role)
);

CREATE TABLE IF NOT EXISTS artifact_idempotency (
    tenant_id VARCHAR(128) NOT NULL,
    idempotency_key VARCHAR(255) NOT NULL,
    operation VARCHAR(64) NOT NULL,
    request_hash VARCHAR(64) NOT NULL,
    response_json JSONB NOT NULL,
    created_at TIMESTAMP WITH TIME ZONE NOT NULL,
    PRIMARY KEY (tenant_id, idempotency_key, operation)
);

CREATE TABLE IF NOT EXISTS artifact_event_outbox (
    id VARCHAR(36) PRIMARY KEY,
    event_type VARCHAR(64) NOT NULL,
    payload JSONB NOT NULL,
    status VARCHAR(24) NOT NULL,
    attempt_count INTEGER NOT NULL DEFAULT 0,
    next_attempt_at TIMESTAMP WITH TIME ZONE NOT NULL,
    created_at TIMESTAMP WITH TIME ZONE NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_artifact_outbox_retry ON artifact_event_outbox (status, next_attempt_at);

CREATE OR REPLACE FUNCTION artifact_revisions_immutable() RETURNS trigger AS $$
BEGIN
  RAISE EXCEPTION 'artifact revision payload is immutable';
END;
$$ LANGUAGE plpgsql;
DROP TRIGGER IF EXISTS artifact_revisions_no_update ON artifact_revisions;
CREATE TRIGGER artifact_revisions_no_update
BEFORE UPDATE ON artifact_revisions
FOR EACH ROW EXECUTE PROCEDURE artifact_revisions_immutable();

-- +migrate Dialect sqlite
CREATE TABLE IF NOT EXISTS artifacts (
    id TEXT PRIMARY KEY,
    tenant_id TEXT NOT NULL,
    owner_user_id TEXT NOT NULL,
    project_id TEXT,
    kind TEXT NOT NULL,
    title TEXT NOT NULL,
    logical_key TEXT,
    status TEXT NOT NULL,
    classification TEXT NOT NULL DEFAULT 'internal',
    created_at DATETIME NOT NULL,
    updated_at DATETIME NOT NULL,
    deleted_at DATETIME
);
CREATE INDEX IF NOT EXISTS idx_artifacts_owner_created ON artifacts (tenant_id, owner_user_id, created_at);

CREATE TABLE IF NOT EXISTS artifact_blobs (
    id TEXT PRIMARY KEY,
    tenant_id TEXT NOT NULL,
    sha256 TEXT NOT NULL,
    size INTEGER NOT NULL,
    mime_type TEXT NOT NULL,
    storage_backend TEXT NOT NULL,
    storage_key TEXT NOT NULL,
    encryption_key_ref TEXT,
    state TEXT NOT NULL,
    created_at DATETIME NOT NULL,
    UNIQUE (tenant_id, sha256, size)
);

CREATE TABLE IF NOT EXISTS artifact_revisions (
    id TEXT PRIMARY KEY,
    artifact_id TEXT NOT NULL,
    revision_no INTEGER NOT NULL,
    parent_revision_id TEXT,
    merge_parent_revision_id TEXT,
    blob_id TEXT,
    inline_json TEXT,
    content_type TEXT NOT NULL,
    schema_name TEXT,
    schema_version TEXT,
    content_hash TEXT NOT NULL,
    size INTEGER NOT NULL,
    caption TEXT,
    metadata TEXT NOT NULL DEFAULT '{}',
    producer_type TEXT NOT NULL,
    producer_id TEXT,
    producer_run_id TEXT,
    producer_event_id TEXT,
    created_by TEXT NOT NULL,
    created_at DATETIME NOT NULL,
    UNIQUE (artifact_id, revision_no)
);
CREATE INDEX IF NOT EXISTS idx_artifact_revisions_producer ON artifact_revisions (producer_run_id, producer_event_id);

CREATE TABLE IF NOT EXISTS artifact_heads (
    artifact_id TEXT NOT NULL,
    channel TEXT NOT NULL,
    revision_id TEXT NOT NULL,
    version INTEGER NOT NULL,
    updated_at DATETIME NOT NULL,
    PRIMARY KEY (artifact_id, channel)
);

CREATE TABLE IF NOT EXISTS artifact_bindings (
    id TEXT PRIMARY KEY,
    artifact_id TEXT NOT NULL,
    revision_id TEXT,
    scope_type TEXT NOT NULL,
    scope_id TEXT NOT NULL,
    role TEXT NOT NULL,
    slot_key TEXT,
    list_item_key TEXT,
    position INTEGER,
    validity TEXT NOT NULL,
    follow_head INTEGER NOT NULL DEFAULT 0,
    created_at DATETIME NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_artifact_bindings_scope ON artifact_bindings (scope_type, scope_id, role);

CREATE TABLE IF NOT EXISTS artifact_dependencies (
    output_revision_id TEXT NOT NULL,
    input_revision_id TEXT NOT NULL,
    role TEXT NOT NULL,
    required INTEGER NOT NULL,
    PRIMARY KEY (output_revision_id, input_revision_id, role)
);

CREATE TABLE IF NOT EXISTS artifact_idempotency (
    tenant_id TEXT NOT NULL,
    idempotency_key TEXT NOT NULL,
    operation TEXT NOT NULL,
    request_hash TEXT NOT NULL,
    response_json TEXT NOT NULL,
    created_at DATETIME NOT NULL,
    PRIMARY KEY (tenant_id, idempotency_key, operation)
);

CREATE TABLE IF NOT EXISTS artifact_event_outbox (
    id TEXT PRIMARY KEY,
    event_type TEXT NOT NULL,
    payload TEXT NOT NULL,
    status TEXT NOT NULL,
    attempt_count INTEGER NOT NULL DEFAULT 0,
    next_attempt_at DATETIME NOT NULL,
    created_at DATETIME NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_artifact_outbox_retry ON artifact_event_outbox (status, next_attempt_at);

CREATE TRIGGER IF NOT EXISTS artifact_revisions_no_update
BEFORE UPDATE ON artifact_revisions
BEGIN
  SELECT RAISE(ABORT, 'artifact revision payload is immutable');
END;

