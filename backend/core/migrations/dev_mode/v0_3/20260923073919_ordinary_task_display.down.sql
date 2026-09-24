-- +migrate Dialect postgres
DROP INDEX IF EXISTS idx_subagent_execution_artifacts;
DROP INDEX IF EXISTS idx_subagent_public_steps;
ALTER TABLE sub_agent_artifacts DROP COLUMN execution_id;
ALTER TABLE sub_agent_steps DROP COLUMN execution_id;
ALTER TABLE sub_agent_tasks DROP COLUMN finished_at;
ALTER TABLE sub_agent_tasks DROP COLUMN started_at;
ALTER TABLE sub_agent_tasks DROP COLUMN display_revision;
ALTER TABLE sub_agent_tasks DROP COLUMN execution_id;

-- +migrate Dialect sqlite
DROP INDEX IF EXISTS idx_subagent_execution_artifacts;
DROP INDEX IF EXISTS idx_subagent_public_steps;
ALTER TABLE sub_agent_artifacts DROP COLUMN execution_id;
ALTER TABLE sub_agent_steps DROP COLUMN execution_id;
ALTER TABLE sub_agent_tasks DROP COLUMN finished_at;
ALTER TABLE sub_agent_tasks DROP COLUMN started_at;
ALTER TABLE sub_agent_tasks DROP COLUMN display_revision;
ALTER TABLE sub_agent_tasks DROP COLUMN execution_id;
