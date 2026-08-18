-- 20260814120000_add_subagent_sources
-- +migrate Up
-- +migrate Dialect postgres
ALTER TABLE sub_agent_tasks
    ADD COLUMN IF NOT EXISTS sources JSONB NOT NULL DEFAULT '[]'::jsonb;

-- +migrate Dialect sqlite
ALTER TABLE sub_agent_tasks ADD COLUMN sources JSON NOT NULL DEFAULT '[]';
