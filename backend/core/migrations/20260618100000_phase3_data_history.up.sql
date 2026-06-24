-- Phase 3: Data History & Rich Media Support
-- Adds: hidden/caption to sub_agent_artifacts,
--       plugin_slot_order table,
--       content_snapshot/change_source to plugin_slot_revisions.

-- 2.1 sub_agent_artifacts: hidden + caption
ALTER TABLE sub_agent_artifacts
  ADD COLUMN IF NOT EXISTS hidden  BOOLEAN NOT NULL DEFAULT FALSE,
  ADD COLUMN IF NOT EXISTS caption TEXT;

CREATE INDEX IF NOT EXISTS idx_saa_task_visible
  ON sub_agent_artifacts(task_id, artifact_key, hidden, seq);

-- 2.2 plugin_slot_order: stable sort order per (session, slot)
CREATE TABLE IF NOT EXISTS plugin_slot_order (
  session_id     VARCHAR(36)  NOT NULL,
  slot_id        VARCHAR(64)  NOT NULL,
  order_list     JSONB        NOT NULL DEFAULT '[]',
  order_version  INT          NOT NULL DEFAULT 0,
  updated_at     TIMESTAMPTZ  NOT NULL DEFAULT now(),
  PRIMARY KEY (session_id, slot_id)
);

-- 2.3 plugin_slot_revisions: content_snapshot + change_source
ALTER TABLE plugin_slot_revisions
  ADD COLUMN IF NOT EXISTS content_snapshot JSONB,
  ADD COLUMN IF NOT EXISTS change_source    VARCHAR(16) NOT NULL DEFAULT 'ai';
