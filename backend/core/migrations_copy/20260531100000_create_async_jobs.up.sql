-- 20260531100000_create_async_jobs
-- +migrate Up

CREATE TABLE public.async_jobs (
    id character varying(64) NOT NULL,
    job_type character varying(64) NOT NULL,
    status character varying(32) NOT NULL,
    resource_type character varying(64) DEFAULT ''::character varying NOT NULL,
    resource_id character varying(128) DEFAULT ''::character varying NOT NULL,
    idempotency_key character varying(128) DEFAULT ''::character varying NOT NULL,
    payload_json json,
    result_json json,
    error_code character varying(64) DEFAULT ''::character varying NOT NULL,
    error_message text DEFAULT ''::text NOT NULL,
    error_details_json json,
    progress_current bigint DEFAULT 0 NOT NULL,
    progress_total bigint DEFAULT 0 NOT NULL,
    attempt_count integer DEFAULT 0 NOT NULL,
    max_attempts integer DEFAULT 1 NOT NULL,
    next_run_at timestamp with time zone NOT NULL,
    locked_by character varying(128) DEFAULT ''::character varying NOT NULL,
    lock_until timestamp with time zone,
    started_at timestamp with time zone,
    finished_at timestamp with time zone,
    heartbeat_at timestamp with time zone,
    create_user_id character varying(255) DEFAULT ''::character varying NOT NULL,
    create_user_name character varying(255) DEFAULT ''::character varying NOT NULL,
    created_at timestamp with time zone NOT NULL,
    updated_at timestamp with time zone NOT NULL,
    CONSTRAINT async_jobs_pkey PRIMARY KEY (id),
    CONSTRAINT chk_async_jobs_status CHECK ((status)::text IN ('pending', 'running', 'succeeded', 'failed', 'canceled'))
);

CREATE INDEX idx_async_jobs_status_next ON public.async_jobs(status, next_run_at);
CREATE INDEX idx_async_jobs_type_status ON public.async_jobs(job_type, status);
CREATE INDEX idx_async_jobs_resource ON public.async_jobs(resource_type, resource_id);
CREATE INDEX idx_async_jobs_lock_until ON public.async_jobs(lock_until);
CREATE INDEX idx_async_jobs_idempotency_key ON public.async_jobs(idempotency_key);
CREATE UNIQUE INDEX idx_async_jobs_type_idempotency_key_unique
    ON public.async_jobs(job_type, idempotency_key)
    WHERE idempotency_key <> '';
