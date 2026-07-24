-- 20260622130000_add_agent_thread_steps
-- +migrate Up

ALTER TABLE public.agent_thread_records
    ADD COLUMN IF NOT EXISTS step_id character varying(128) DEFAULT ''::character varying NOT NULL;

CREATE INDEX IF NOT EXISTS idx_agent_thread_records_thread_step_stream_id
    ON public.agent_thread_records USING btree (thread_id, step_id, stream_kind, id);

CREATE TABLE IF NOT EXISTS public.agent_thread_steps (
    thread_id character varying(128) NOT NULL,
    step_id character varying(128) NOT NULL,
    title character varying(255) DEFAULT ''::character varying NOT NULL,
    status character varying(32) DEFAULT 'running'::character varying NOT NULL,
    active boolean DEFAULT false NOT NULL,
    order_index integer DEFAULT 0 NOT NULL,
    event_count bigint DEFAULT 0 NOT NULL,
    current_task_id character varying(128) DEFAULT ''::character varying NOT NULL,
    started_at timestamp with time zone,
    ended_at timestamp with time zone,
    created_at timestamp with time zone NOT NULL,
    updated_at timestamp with time zone NOT NULL,
    CONSTRAINT agent_thread_steps_pkey PRIMARY KEY (thread_id, step_id)
);

CREATE INDEX IF NOT EXISTS idx_agent_thread_steps_thread_order
    ON public.agent_thread_steps USING btree (thread_id, order_index, step_id);

CREATE INDEX IF NOT EXISTS idx_agent_thread_steps_thread_active
    ON public.agent_thread_steps USING btree (thread_id, active, updated_at);
