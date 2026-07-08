-- Add state_layout_content column: stores only x-layout JSON (node positions), no version check needed.
ALTER TABLE plugin_drafts ADD COLUMN IF NOT EXISTS state_layout_content TEXT NOT NULL DEFAULT '';

-- Add version column for optimistic locking on plugin_yaml_content / state_yaml_content.
-- Existing rows start at version 1.
ALTER TABLE plugin_drafts ADD COLUMN IF NOT EXISTS version INT NOT NULL DEFAULT 1;
