-- 20260707100000_add_agent_thread_step_version
-- +migrate Up
-- +migrate Dialect postgres

ALTER TABLE public.agent_thread_steps
    ADD COLUMN IF NOT EXISTS version integer;

-- +migrate Dialect sqlite
SELECT 1; -- Historical SQLite change is included by the first v0.2 dev migration.
