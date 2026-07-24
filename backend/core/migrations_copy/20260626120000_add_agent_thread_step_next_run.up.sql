-- 20260626120000_add_agent_thread_step_next_run
-- +migrate Up

ALTER TABLE public.agent_thread_steps
    ADD COLUMN IF NOT EXISTS next_step_run_id character varying(128) DEFAULT ''::character varying NOT NULL;
