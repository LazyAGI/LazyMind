-- 20260814120500_add_subagent_sources
-- +migrate Down
-- +migrate Dialect postgres
ALTER TABLE sub_agent_tasks DROP COLUMN IF EXISTS sources;

-- +migrate Dialect sqlite
ALTER TABLE sub_agent_tasks DROP COLUMN sources;
