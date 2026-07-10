CREATE TABLE IF NOT EXISTS public.skills (
    id VARCHAR(36) PRIMARY KEY,
    owner_user_id VARCHAR(255) NOT NULL,
    owner_user_name VARCHAR(255) NOT NULL DEFAULT '',
    create_user_id VARCHAR(255) NOT NULL,
    create_user_name VARCHAR(255) NOT NULL DEFAULT '',
    category VARCHAR(128) NOT NULL,
    skill_name VARCHAR(255) NOT NULL,
    origin_builtin_skill_uid VARCHAR(64) NOT NULL DEFAULT '',
    description TEXT,
    tags JSON,
    relative_root VARCHAR(1024) NOT NULL,
    skill_md_path VARCHAR(1024) NOT NULL DEFAULT 'SKILL.md',
    head_revision_id VARCHAR(36),
    version BIGINT NOT NULL DEFAULT 1,
    auto_evo BOOLEAN NOT NULL DEFAULT FALSE,
    auto_evo_apply_status VARCHAR(32) NOT NULL DEFAULT 'idle',
    auto_evo_generation BIGINT NOT NULL DEFAULT 0,
    auto_evo_started_at TIMESTAMP,
    auto_evo_finished_at TIMESTAMP,
    auto_evo_error TEXT NOT NULL DEFAULT '',
    is_enabled BOOLEAN NOT NULL DEFAULT TRUE,
    update_status VARCHAR(32) NOT NULL DEFAULT 'up_to_date',
    ext JSON,
    created_at TIMESTAMP NOT NULL,
    updated_at TIMESTAMP NOT NULL
);

CREATE UNIQUE INDEX IF NOT EXISTS uk_skills_owner_identity
    ON public.skills(owner_user_id, category, skill_name);

CREATE UNIQUE INDEX IF NOT EXISTS uk_skills_owner_relative_root
    ON public.skills(owner_user_id, relative_root);

CREATE INDEX IF NOT EXISTS idx_skills_owner_enabled
    ON public.skills(owner_user_id, is_enabled, category);

CREATE TABLE IF NOT EXISTS public.skill_blobs (
    hash VARCHAR(64) PRIMARY KEY,
    size BIGINT NOT NULL,
    mime VARCHAR(128),
    file_type VARCHAR(32) NOT NULL DEFAULT 'unknown',
    "binary" BOOLEAN NOT NULL DEFAULT FALSE,
    storage_backend VARCHAR(32) NOT NULL,
    storage_key TEXT,
    content BYTEA,
    created_at TIMESTAMP NOT NULL,
    CONSTRAINT chk_skill_blob_storage_backend CHECK (storage_backend IN ('postgres', 'local_file', 's3')),
    CONSTRAINT chk_skill_blob_storage_shape CHECK (
        ("binary" = FALSE AND storage_backend = 'postgres' AND content IS NOT NULL AND storage_key IS NULL)
        OR
        ("binary" = TRUE AND storage_backend IN ('local_file', 's3') AND content IS NULL AND storage_key IS NOT NULL)
    )
);

CREATE TABLE IF NOT EXISTS public.skill_revisions (
    id VARCHAR(36) PRIMARY KEY,
    skill_id VARCHAR(36) NOT NULL,
    parent_revision_id VARCHAR(36),
    revision_no BIGINT NOT NULL,
    tree_hash VARCHAR(64) NOT NULL,
    message TEXT,
    change_source VARCHAR(32) NOT NULL DEFAULT 'draft_commit',
    source_ref_type VARCHAR(64) NOT NULL DEFAULT '',
    source_ref_id VARCHAR(128) NOT NULL DEFAULT '',
    created_by VARCHAR(255),
    created_at TIMESTAMP NOT NULL
);

CREATE UNIQUE INDEX IF NOT EXISTS uk_skill_revisions_skill_no
    ON public.skill_revisions(skill_id, revision_no);

CREATE INDEX IF NOT EXISTS idx_skill_revisions_skill_created
    ON public.skill_revisions(skill_id, created_at DESC);

CREATE TABLE IF NOT EXISTS public.skill_revision_entries (
    revision_id VARCHAR(36) NOT NULL,
    path VARCHAR(1024) NOT NULL,
    entry_type VARCHAR(16) NOT NULL,
    blob_hash VARCHAR(64),
    size BIGINT NOT NULL DEFAULT 0,
    mime VARCHAR(128),
    file_type VARCHAR(32) NOT NULL DEFAULT 'unknown',
    "binary" BOOLEAN NOT NULL DEFAULT FALSE,
    mode INT NOT NULL DEFAULT 420,
    PRIMARY KEY (revision_id, path),
    CONSTRAINT chk_skill_revision_entry_type CHECK (entry_type IN ('file', 'dir')),
    CONSTRAINT chk_skill_revision_entry_blob_shape CHECK (
        (entry_type = 'file' AND blob_hash IS NOT NULL)
        OR
        (entry_type = 'dir' AND blob_hash IS NULL)
    )
);

CREATE INDEX IF NOT EXISTS idx_skill_revision_entries_blob
    ON public.skill_revision_entries(blob_hash);

CREATE TABLE IF NOT EXISTS public.skill_drafts (
    skill_id VARCHAR(36) PRIMARY KEY,
    base_revision_id VARCHAR(36),
    draft_status VARCHAR(32) NOT NULL DEFAULT '',
    draft_updated_at TIMESTAMP,
    task_id VARCHAR(128) NOT NULL DEFAULT '',
    conversation_id VARCHAR(36),
    updated_by VARCHAR(255),
    version BIGINT NOT NULL DEFAULT 1,
    created_at TIMESTAMP NOT NULL,
    updated_at TIMESTAMP NOT NULL
);

CREATE TABLE IF NOT EXISTS public.skill_draft_entries (
    skill_id VARCHAR(36) NOT NULL,
    path VARCHAR(1024) NOT NULL,
    op VARCHAR(16) NOT NULL,
    entry_type VARCHAR(16),
    blob_hash VARCHAR(64),
    size BIGINT NOT NULL DEFAULT 0,
    mime VARCHAR(128),
    file_type VARCHAR(32),
    "binary" BOOLEAN NOT NULL DEFAULT FALSE,
    mode INT NOT NULL DEFAULT 420,
    updated_at TIMESTAMP NOT NULL,
    PRIMARY KEY (skill_id, path),
    CONSTRAINT chk_skill_draft_entry_op CHECK (op IN ('upsert', 'delete')),
    CONSTRAINT chk_skill_draft_entry_shape CHECK (
        op = 'delete'
        OR
        (op = 'upsert' AND entry_type IN ('file', 'dir'))
    )
);

CREATE INDEX IF NOT EXISTS idx_skill_draft_entries_blob
    ON public.skill_draft_entries(blob_hash);

CREATE TABLE IF NOT EXISTS public.skill_search_indexes (
    skill_id VARCHAR(36) PRIMARY KEY,
    owner_user_id VARCHAR(255) NOT NULL,
    head_revision_id VARCHAR(36) NOT NULL,
    content TEXT NOT NULL,
    updated_at TIMESTAMP NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_skill_search_owner
    ON public.skill_search_indexes(owner_user_id);

CREATE TABLE IF NOT EXISTS public.skill_market_items (
    id VARCHAR(36) PRIMARY KEY,
    source_skill_id VARCHAR(36) NOT NULL,
    status VARCHAR(32) NOT NULL DEFAULT 'draft',
    icon TEXT NOT NULL DEFAULT '',
    sort_order INT NOT NULL DEFAULT 0,
    version_note TEXT NOT NULL DEFAULT '',
    created_by VARCHAR(255),
    updated_by VARCHAR(255),
    published_at TIMESTAMP,
    created_at TIMESTAMP NOT NULL,
    updated_at TIMESTAMP NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_skill_market_items_status
    ON public.skill_market_items(status, sort_order, updated_at DESC);

ALTER TABLE public.skill_share_items
    ADD COLUMN IF NOT EXISTS source_skill_id VARCHAR(36) NOT NULL DEFAULT '';

CREATE INDEX IF NOT EXISTS idx_skill_share_items_source_skill
    ON public.skill_share_items(source_skill_id);
