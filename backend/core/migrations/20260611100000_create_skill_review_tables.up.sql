-- 20260611100000_create_skill_review_tables
-- +migrate Up

CREATE TABLE IF NOT EXISTS public.skill_review_results (
    id TEXT NOT NULL,
    skill_name TEXT NOT NULL,
    "type" TEXT NOT NULL,
    review_status TEXT NOT NULL DEFAULT 'pending',
    userid TEXT NOT NULL DEFAULT '',
    requestid TEXT NOT NULL DEFAULT '',
    skill_content TEXT NOT NULL,
    summary TEXT NOT NULL DEFAULT '',
    "time" timestamp with time zone NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT skill_review_results_pkey PRIMARY KEY (id),
    CONSTRAINT chk_skill_review_results_type CHECK ("type" IN ('new', 'patch')),
    CONSTRAINT chk_skill_review_results_review_status CHECK (review_status IN ('pending', 'accepted', 'rejected', 'expired'))
);

CREATE INDEX IF NOT EXISTS idx_skill_review_results_pending_scan
ON public.skill_review_results (userid, review_status, "type", skill_name, "time" DESC);

CREATE TABLE IF NOT EXISTS public.skill_review_stats (
    id TEXT NOT NULL,
    requestid TEXT NOT NULL,
    userid TEXT NOT NULL,
    status TEXT NOT NULL,
    started_at TEXT NOT NULL,
    duration_ms INTEGER NOT NULL DEFAULT 0,
    summary JSONB NOT NULL DEFAULT '{}'::jsonb,
    CONSTRAINT skill_review_stats_pkey PRIMARY KEY (id),
    CONSTRAINT chk_skill_review_stats_status CHECK (status IN ('running', 'completed', 'skipped', 'failed')),
    CONSTRAINT chk_skill_review_stats_duration_ms_non_negative CHECK (duration_ms >= 0)
);

CREATE TABLE IF NOT EXISTS public.skill_review_run_stats (
    id TEXT NOT NULL,
    requestid TEXT NOT NULL,
    userid TEXT NOT NULL,
    status TEXT NOT NULL,
    started_at TEXT NOT NULL,
    duration_ms INTEGER NOT NULL DEFAULT 0,
    summary JSONB NOT NULL DEFAULT '{}'::jsonb,
    CONSTRAINT skill_review_run_stats_pkey PRIMARY KEY (id),
    CONSTRAINT chk_skill_review_run_stats_status CHECK (status IN ('running', 'completed', 'skipped', 'failed')),
    CONSTRAINT chk_skill_review_run_stats_duration_ms_non_negative CHECK (duration_ms >= 0)
);

CREATE TABLE IF NOT EXISTS public.memory_review (
    id TEXT NOT NULL,
    user_id TEXT NOT NULL DEFAULT '',
    target TEXT NOT NULL,
    session_id TEXT NOT NULL,
    source_content TEXT NOT NULL DEFAULT '',
    content TEXT NOT NULL DEFAULT '',
    operations JSONB NOT NULL DEFAULT '[]'::jsonb,
    state TEXT NOT NULL DEFAULT 'success',
    review_status TEXT NOT NULL DEFAULT 'pending',
    "time" timestamp with time zone NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT memory_review_pkey PRIMARY KEY (id),
    CONSTRAINT chk_memory_review_target CHECK (target IN ('memory', 'user_preference')),
    CONSTRAINT chk_memory_review_state CHECK (state IN ('success')),
    CONSTRAINT chk_memory_review_review_status CHECK (review_status IN ('pending', 'accepted', 'rejected', 'expired'))
);

CREATE INDEX IF NOT EXISTS idx_memory_review_pending_scan
ON public.memory_review (target, user_id, state, review_status, "time" ASC);
