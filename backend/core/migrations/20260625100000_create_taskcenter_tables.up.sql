-- 20260625100000_create_taskcenter_tables
-- +migrate Up

CREATE TABLE IF NOT EXISTS public.task_center_tasks (
    id character varying(36) NOT NULL,
    user_id character varying(255) NOT NULL,
    conversation_id character varying(36) NOT NULL,
    plugin_session_id character varying(36),
    task_type character varying(32) NOT NULL,
    title text,
    status character varying(16) NOT NULL DEFAULT 'pending',
    schedule_id character varying(36),
    progress_json text,
    predicted_completion_at timestamp with time zone,
    created_at timestamp with time zone NOT NULL,
    updated_at timestamp with time zone NOT NULL,
    finished_at timestamp with time zone,
    CONSTRAINT task_center_tasks_pkey PRIMARY KEY (id),
    CONSTRAINT chk_tct_status CHECK ((status)::text IN ('pending', 'running', 'waiting', 'succeeded', 'failed', 'canceled')),
    CONSTRAINT chk_tct_task_type CHECK ((task_type)::text IN ('plugin_run', 'background_chat', 'scheduled'))
);

CREATE INDEX IF NOT EXISTS idx_tct_user_status ON public.task_center_tasks(user_id, status);

CREATE TABLE IF NOT EXISTS public.user_schedules (
    id character varying(36) NOT NULL,
    user_id character varying(255) NOT NULL,
    conversation_id character varying(36),
    cron_expr character varying(64) NOT NULL,
    timezone character varying(64) NOT NULL DEFAULT 'Asia/Shanghai',
    prompt_template text NOT NULL,
    enabled boolean NOT NULL DEFAULT true,
    last_run_at timestamp with time zone,
    next_run_at timestamp with time zone NOT NULL,
    created_at timestamp with time zone NOT NULL,
    CONSTRAINT user_schedules_pkey PRIMARY KEY (id)
);

CREATE INDEX IF NOT EXISTS idx_us_user ON public.user_schedules(user_id);
CREATE INDEX IF NOT EXISTS idx_us_next_run ON public.user_schedules(next_run_at) WHERE enabled = true;
