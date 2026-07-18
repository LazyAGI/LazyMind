-- Repair environments where migration version 20260618100000 was already
-- consumed by a different historical migration and phase3_data_history was
-- therefore skipped.  The current ORM and plugin queries require both fields.
ALTER TABLE sub_agent_artifacts
  ADD COLUMN IF NOT EXISTS hidden  BOOLEAN NOT NULL DEFAULT FALSE,
  ADD COLUMN IF NOT EXISTS caption TEXT;

CREATE INDEX IF NOT EXISTS idx_saa_task_visible
  ON sub_agent_artifacts(task_id, slot, hidden, seq);
