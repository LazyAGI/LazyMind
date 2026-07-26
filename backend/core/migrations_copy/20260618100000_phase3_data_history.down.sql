-- Rollback Phase 3: Data History & Rich Media Support

DROP TABLE IF EXISTS plugin_slot_order;

ALTER TABLE plugin_slot_revisions
  DROP COLUMN IF EXISTS content_snapshot,
  DROP COLUMN IF EXISTS change_source;

DROP INDEX IF EXISTS idx_saa_task_visible;

ALTER TABLE sub_agent_artifacts
  DROP COLUMN IF EXISTS hidden,
  DROP COLUMN IF EXISTS caption;
