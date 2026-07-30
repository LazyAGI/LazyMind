-- 20260626120000_add_agent_thread_step_next_run
-- +migrate Up
-- +migrate Dialect postgres

ALTER TABLE public.agent_thread_steps
    ADD COLUMN IF NOT EXISTS next_step_run_id character varying(128) DEFAULT ''::character varying NOT NULL;

-- +migrate Dialect sqlite
SELECT 1; -- Historical SQLite change is included by the first v0.2 dev migration.
