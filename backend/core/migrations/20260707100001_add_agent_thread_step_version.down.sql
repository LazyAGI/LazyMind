-- 20260707100000_add_agent_thread_step_version
-- +migrate Down

ALTER TABLE public.agent_thread_steps
    DROP COLUMN IF EXISTS version;
