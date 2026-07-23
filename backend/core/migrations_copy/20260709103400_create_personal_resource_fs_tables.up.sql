CREATE TABLE IF NOT EXISTS public.personal_resources (
    id VARCHAR(36) PRIMARY KEY,
    user_id VARCHAR(255) NOT NULL,
    resource_type VARCHAR(64) NOT NULL,
    head_revision_id VARCHAR(36),
    version BIGINT NOT NULL DEFAULT 1,
    created_at TIMESTAMP NOT NULL,
    updated_at TIMESTAMP NOT NULL,
    CONSTRAINT chk_personal_resources_type
        CHECK (resource_type IN ('memory', 'user_preference'))
);

CREATE UNIQUE INDEX IF NOT EXISTS uk_personal_resources_user_type
    ON public.personal_resources(user_id, resource_type);

CREATE TABLE IF NOT EXISTS public.personal_resource_blobs (
    hash VARCHAR(64) PRIMARY KEY,
    size BIGINT NOT NULL,
    mime VARCHAR(128),
    file_type VARCHAR(32) NOT NULL DEFAULT 'unknown',
    "binary" BOOLEAN NOT NULL DEFAULT FALSE,
    storage_backend VARCHAR(32) NOT NULL,
    storage_key TEXT,
    content BYTEA,
    created_at TIMESTAMP NOT NULL,
    CONSTRAINT chk_personal_resource_blob_storage_backend CHECK (storage_backend IN ('postgres', 'local_file', 's3'))
);

CREATE TABLE IF NOT EXISTS public.personal_resource_revisions (
    id VARCHAR(36) PRIMARY KEY,
    resource_id VARCHAR(36) NOT NULL,
    parent_revision_id VARCHAR(36),
    revision_no BIGINT NOT NULL,
    path VARCHAR(1024) NOT NULL,
    blob_hash VARCHAR(64) NOT NULL,
    content_hash VARCHAR(64) NOT NULL,
    size BIGINT NOT NULL DEFAULT 0,
    mime VARCHAR(128),
    file_type VARCHAR(32) NOT NULL DEFAULT 'unknown',
    "binary" BOOLEAN NOT NULL DEFAULT FALSE,
    message TEXT,
    change_source VARCHAR(32) NOT NULL DEFAULT 'draft_commit',
    source_ref_type VARCHAR(64) NOT NULL DEFAULT '',
    source_ref_id VARCHAR(128) NOT NULL DEFAULT '',
    created_by VARCHAR(255),
    created_at TIMESTAMP NOT NULL
);

CREATE UNIQUE INDEX IF NOT EXISTS uk_personal_resource_revisions_no
    ON public.personal_resource_revisions(resource_id, revision_no);

CREATE INDEX IF NOT EXISTS idx_personal_resource_revisions_created
    ON public.personal_resource_revisions(resource_id, created_at DESC);

CREATE INDEX IF NOT EXISTS idx_personal_resource_revisions_blob
    ON public.personal_resource_revisions(blob_hash);

CREATE TABLE IF NOT EXISTS public.personal_resource_drafts (
    resource_id VARCHAR(36) PRIMARY KEY,
    base_revision_id VARCHAR(36),
    path VARCHAR(1024) NOT NULL,
    blob_hash VARCHAR(64) NOT NULL,
    content_hash VARCHAR(64) NOT NULL,
    size BIGINT NOT NULL DEFAULT 0,
    mime VARCHAR(128),
    file_type VARCHAR(32) NOT NULL DEFAULT 'unknown',
    "binary" BOOLEAN NOT NULL DEFAULT FALSE,
    draft_status VARCHAR(32) NOT NULL DEFAULT '',
    draft_updated_at TIMESTAMP,
    task_id VARCHAR(128) NOT NULL DEFAULT '',
    conversation_id VARCHAR(36),
    updated_by VARCHAR(255),
    version BIGINT NOT NULL DEFAULT 1,
    created_at TIMESTAMP NOT NULL,
    updated_at TIMESTAMP NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_personal_resource_drafts_blob
    ON public.personal_resource_drafts(blob_hash);

CREATE TABLE IF NOT EXISTS public.personal_resource_review_sessions (
    id VARCHAR(36) PRIMARY KEY,
    resource_id VARCHAR(36) NOT NULL,
    path VARCHAR(1024) NOT NULL,
    base_revision_id VARCHAR(36) NOT NULL,
    head_revision_id VARCHAR(36) NOT NULL,
    draft_version BIGINT NOT NULL,
    draft_blob_hash VARCHAR(64) NOT NULL,
    review_version BIGINT NOT NULL DEFAULT 1,
    status VARCHAR(32) NOT NULL DEFAULT 'active',
    created_by VARCHAR(255),
    created_at TIMESTAMP NOT NULL,
    updated_at TIMESTAMP NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_personal_resource_review_sessions_resource_status
    ON public.personal_resource_review_sessions(resource_id, status);

CREATE TABLE IF NOT EXISTS public.personal_resource_review_action_batches (
    id VARCHAR(36) PRIMARY KEY,
    session_id VARCHAR(36) NOT NULL,
    resource_id VARCHAR(36) NOT NULL,
    before_draft_blob_hash VARCHAR(64) NOT NULL,
    after_draft_blob_hash VARCHAR(64) NOT NULL,
    before_draft_version BIGINT NOT NULL,
    after_draft_version BIGINT NOT NULL,
    review_version BIGINT NOT NULL,
    created_by VARCHAR(255),
    created_at TIMESTAMP NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_personal_resource_review_batches_session_created
    ON public.personal_resource_review_action_batches(session_id, created_at DESC);

CREATE TABLE IF NOT EXISTS public.personal_resource_review_action_items (
    id VARCHAR(36) PRIMARY KEY,
    batch_id VARCHAR(36) NOT NULL,
    hunk_id VARCHAR(128) NOT NULL,
    decision VARCHAR(16) NOT NULL,
    old_start INT NOT NULL DEFAULT 0,
    old_lines INT NOT NULL DEFAULT 0,
    new_start INT NOT NULL DEFAULT 0,
    new_lines INT NOT NULL DEFAULT 0,
    created_at TIMESTAMP NOT NULL,
    CONSTRAINT chk_personal_resource_review_action_decision
        CHECK (decision IN ('accept', 'reject', 'accepted', 'rejected'))
);

CREATE INDEX IF NOT EXISTS idx_personal_resource_review_items_batch
    ON public.personal_resource_review_action_items(batch_id);
