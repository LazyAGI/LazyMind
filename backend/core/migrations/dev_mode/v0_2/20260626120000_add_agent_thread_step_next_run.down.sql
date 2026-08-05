-- 20260626120000_add_agent_thread_step_next_run
-- +migrate Down
-- +migrate Dialect postgres

ALTER TABLE public.agent_thread_steps
    DROP COLUMN IF EXISTS next_step_run_id;

-- +migrate Dialect sqlite
SELECT 1; -- Historical SQLite change is included by the first v0.2 dev migration.
