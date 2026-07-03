-- 仅回滚列重命名，不回滚数据值（开发环境可接受）
ALTER TABLE sub_agent_artifacts RENAME COLUMN slot TO artifact_key;
DROP INDEX IF EXISTS idx_saa_task_slot;
CREATE INDEX idx_saa_task_key ON sub_agent_artifacts(task_id, artifact_key, seq);

ALTER TABLE sub_agent_tasks RENAME COLUMN input_slots  TO input_artifact_keys;
ALTER TABLE sub_agent_tasks RENAME COLUMN output_slots TO output_artifact_keys;

ALTER TABLE plugin_slot_revisions RENAME COLUMN slot TO artifact_key;
DROP INDEX IF EXISTS idx_psr_slot;
CREATE INDEX idx_psr_artifact ON plugin_slot_revisions(artifact_key);

ALTER TABLE plugin_human_artifacts RENAME COLUMN slot TO artifact_key;
DROP INDEX IF EXISTS idx_plugin_human_artifacts_session_slot;
CREATE INDEX idx_plugin_human_artifacts_session_key
    ON plugin_human_artifacts (session_id, artifact_key);
