-- 20260707100000_add_agent_thread_step_version
-- +migrate Up

ALTER TABLE public.agent_thread_steps
    ADD COLUMN IF NOT EXISTS version integer;
