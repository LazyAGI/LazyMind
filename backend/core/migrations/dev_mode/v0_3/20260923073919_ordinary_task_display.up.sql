-- +migrate Dialect postgres
ALTER TABLE sub_agent_tasks ADD COLUMN execution_id VARCHAR(64) NOT NULL DEFAULT '';
ALTER TABLE sub_agent_tasks ADD COLUMN display_revision BIGINT NOT NULL DEFAULT 0;
ALTER TABLE sub_agent_tasks ADD COLUMN started_at TIMESTAMPTZ;
ALTER TABLE sub_agent_tasks ADD COLUMN finished_at TIMESTAMPTZ;
ALTER TABLE sub_agent_steps ADD COLUMN execution_id VARCHAR(64) NOT NULL DEFAULT '';
ALTER TABLE sub_agent_artifacts ADD COLUMN execution_id VARCHAR(64) NOT NULL DEFAULT '';
CREATE INDEX idx_subagent_public_steps ON sub_agent_steps(task_id, execution_id, role, seq);
CREATE INDEX idx_subagent_execution_artifacts ON sub_agent_artifacts(task_id, execution_id);

-- +migrate Dialect sqlite
ALTER TABLE sub_agent_tasks ADD COLUMN execution_id VARCHAR(64) NOT NULL DEFAULT '';
ALTER TABLE sub_agent_tasks ADD COLUMN display_revision BIGINT NOT NULL DEFAULT 0;
ALTER TABLE sub_agent_tasks ADD COLUMN started_at DATETIME;
ALTER TABLE sub_agent_tasks ADD COLUMN finished_at DATETIME;
ALTER TABLE sub_agent_steps ADD COLUMN execution_id VARCHAR(64) NOT NULL DEFAULT '';
ALTER TABLE sub_agent_artifacts ADD COLUMN execution_id VARCHAR(64) NOT NULL DEFAULT '';
CREATE INDEX idx_subagent_public_steps ON sub_agent_steps(task_id, execution_id, role, seq);
CREATE INDEX idx_subagent_execution_artifacts ON sub_agent_artifacts(task_id, execution_id);
