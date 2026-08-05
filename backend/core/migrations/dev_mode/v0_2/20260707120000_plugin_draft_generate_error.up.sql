-- +migrate Dialect postgres
-- Add generate_error column to plugin_drafts.
-- Stores the last error message when generate_status = 'failed'.
ALTER TABLE plugin_drafts
    ADD COLUMN IF NOT EXISTS generate_error TEXT NOT NULL DEFAULT '';

-- +migrate Dialect sqlite
SELECT 1; -- Historical SQLite change is included by the first v0.2 dev migration.
