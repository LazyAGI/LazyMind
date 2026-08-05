-- 20260707100000_add_agent_thread_step_version
-- +migrate Down
-- +migrate Dialect postgres

ALTER TABLE public.agent_thread_steps
    DROP COLUMN IF EXISTS version;

-- +migrate Dialect sqlite
SELECT 1; -- Historical SQLite change is included by the first v0.2 dev migration.
