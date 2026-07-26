-- 20260613120000_create_subagent_tables
-- +migrate Down

DROP TABLE IF EXISTS public.sub_agent_artifacts;
DROP TABLE IF EXISTS public.sub_agent_steps;
DROP TABLE IF EXISTS public.sub_agent_tasks;
