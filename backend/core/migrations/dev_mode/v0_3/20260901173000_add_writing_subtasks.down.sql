-- 20260901173000_add_writing_subtasks
-- +migrate Down
-- +migrate Dialect postgres
ALTER TABLE sub_agent_tasks DROP COLUMN IF EXISTS writing_subtasks;

-- +migrate Dialect sqlite
ALTER TABLE sub_agent_tasks DROP COLUMN writing_subtasks;
