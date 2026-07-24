-- Add generate_warning column to plugin_drafts for non-fatal generation warnings.
ALTER TABLE plugin_drafts ADD COLUMN IF NOT EXISTS generate_warning TEXT NOT NULL DEFAULT '';
