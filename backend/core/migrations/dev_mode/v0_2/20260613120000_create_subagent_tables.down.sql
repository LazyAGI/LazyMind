-- 20260613120000_create_subagent_tables
-- +migrate Down
-- +migrate Dialect postgres

DROP TABLE IF EXISTS public.sub_agent_artifacts;
DROP TABLE IF EXISTS public.sub_agent_steps;
DROP TABLE IF EXISTS public.sub_agent_tasks;

-- +migrate Dialect sqlite
SELECT 1; -- Historical SQLite change is included by the first v0.2 dev migration.
