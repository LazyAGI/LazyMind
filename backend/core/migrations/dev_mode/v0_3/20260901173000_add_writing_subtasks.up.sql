-- 20260901173000_add_writing_subtasks
-- +migrate Up
-- +migrate Dialect postgres
ALTER TABLE sub_agent_tasks
    ADD COLUMN IF NOT EXISTS writing_subtasks JSONB NOT NULL DEFAULT '[]'::jsonb;

-- +migrate Dialect sqlite
ALTER TABLE sub_agent_tasks ADD COLUMN writing_subtasks JSON NOT NULL DEFAULT '[]';
