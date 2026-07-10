-- Add plugin_id column to plugin_drafts for per-user uniqueness enforcement.
-- plugin_id mirrors the `id:` field inside plugin_yaml_content and is kept in sync
-- on every save that touches plugin_yaml_content.
-- Empty string is the default for rows that pre-date this migration; the unique index
-- uses a partial predicate so that multiple empty-string rows are allowed (old drafts).
ALTER TABLE plugin_drafts ADD COLUMN IF NOT EXISTS plugin_id VARCHAR(255) NOT NULL DEFAULT '';

-- Unique index: (created_by, plugin_id) where plugin_id != '' so that un-set legacy rows
-- do not violate the constraint against each other.
CREATE UNIQUE INDEX IF NOT EXISTS idx_plugin_drafts_user_plugin_id
    ON plugin_drafts (created_by, plugin_id)
    WHERE plugin_id != '';
