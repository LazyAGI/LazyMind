CREATE TABLE IF NOT EXISTS public.skill_draft_review_sessions (
    id VARCHAR(36) PRIMARY KEY,
    skill_id VARCHAR(36) NOT NULL,
    base_revision_id VARCHAR(36) NOT NULL,
    draft_version_at_start BIGINT NOT NULL,
    draft_snapshot_hash VARCHAR(64) NOT NULL,
    status VARCHAR(32) NOT NULL DEFAULT 'active',
    version BIGINT NOT NULL DEFAULT 1,
    undo_limit INT NOT NULL DEFAULT 20,
    created_by VARCHAR(255),
    updated_by VARCHAR(255),
    created_at TIMESTAMP NOT NULL,
    updated_at TIMESTAMP NOT NULL,
    CONSTRAINT chk_skill_draft_review_session_status
        CHECK (status IN ('active', 'invalidated', 'committed', 'discarded'))
);

CREATE INDEX IF NOT EXISTS idx_skill_draft_review_sessions_skill_status
    ON public.skill_draft_review_sessions(skill_id, status, updated_at DESC);

CREATE TABLE IF NOT EXISTS public.skill_draft_review_action_batches (
    id VARCHAR(36) PRIMARY KEY,
    review_session_id VARCHAR(36) NOT NULL,
    sequence BIGINT NOT NULL,
    undo_locked BOOLEAN NOT NULL DEFAULT FALSE,
    undone_at TIMESTAMP,
    undone_by VARCHAR(255),
    created_by VARCHAR(255),
    created_at TIMESTAMP NOT NULL,
    UNIQUE(review_session_id, sequence)
);

CREATE INDEX IF NOT EXISTS idx_skill_draft_review_batches_session_created
    ON public.skill_draft_review_action_batches(review_session_id, created_at DESC);

CREATE TABLE IF NOT EXISTS public.skill_draft_review_action_items (
    id VARCHAR(36) PRIMARY KEY,
    batch_id VARCHAR(36) NOT NULL,
    review_session_id VARCHAR(36) NOT NULL,
    path VARCHAR(1024) NOT NULL,
    hunk_id VARCHAR(128) NOT NULL,
    before_decision VARCHAR(16) NOT NULL DEFAULT 'pending',
    after_decision VARCHAR(16) NOT NULL,
    created_at TIMESTAMP NOT NULL,
    CONSTRAINT chk_skill_draft_review_item_before_decision
        CHECK (before_decision IN ('pending', 'accepted', 'rejected')),
    CONSTRAINT chk_skill_draft_review_item_after_decision
        CHECK (after_decision IN ('accepted', 'rejected'))
);

CREATE INDEX IF NOT EXISTS idx_skill_draft_review_items_session_hunk
    ON public.skill_draft_review_action_items(review_session_id, path, hunk_id);

CREATE INDEX IF NOT EXISTS idx_skill_draft_review_items_batch
    ON public.skill_draft_review_action_items(batch_id);
