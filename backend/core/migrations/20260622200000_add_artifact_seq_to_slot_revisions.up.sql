-- Add artifact_seq to plugin_slot_revisions so AI revisions point directly to the
-- sub_agent_artifacts row by (task_id, artifact_key, seq) instead of duplicating
-- the value in content_snapshot.
--
-- Semantics:
--   AI revision:    artifact_seq IS NOT NULL → value lives in sub_agent_artifacts
--   Human revision: artifact_seq IS NULL, content_snapshot carries the value

ALTER TABLE plugin_slot_revisions
    ADD COLUMN IF NOT EXISTS artifact_seq INT;
