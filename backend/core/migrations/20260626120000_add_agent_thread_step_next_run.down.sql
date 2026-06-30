-- 20260626120000_add_agent_thread_step_next_run
-- +migrate Down

ALTER TABLE public.agent_thread_steps
    DROP COLUMN IF EXISTS next_step_run_id;
