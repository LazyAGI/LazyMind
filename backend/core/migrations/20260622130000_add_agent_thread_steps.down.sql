-- 20260622130000_add_agent_thread_steps
-- +migrate Down

DROP INDEX IF EXISTS public.idx_agent_thread_steps_thread_active;
DROP INDEX IF EXISTS public.idx_agent_thread_steps_thread_order;
DROP TABLE IF EXISTS public.agent_thread_steps;

DROP INDEX IF EXISTS public.idx_agent_thread_records_thread_step_stream_id;
ALTER TABLE public.agent_thread_records
    DROP COLUMN IF EXISTS step_id;
