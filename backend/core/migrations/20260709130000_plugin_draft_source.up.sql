-- Add source_type and source_skill_id to plugin_drafts for origin tracking.
-- source_type: '' | 'ai' | 'skill' | 'blank'
--   ''      — created before this migration (treated as blank)
--   'ai'    — generated from natural language description
--   'skill' — converted from an existing skill
--   'blank' — manually created from scratch
ALTER TABLE plugin_drafts ADD COLUMN IF NOT EXISTS source_type VARCHAR(16) NOT NULL DEFAULT '';
ALTER TABLE plugin_drafts ADD COLUMN IF NOT EXISTS source_skill_id VARCHAR(36) NOT NULL DEFAULT '';
ALTER TABLE plugin_drafts ADD COLUMN IF NOT EXISTS source_skill_name VARCHAR(255) NOT NULL DEFAULT '';
