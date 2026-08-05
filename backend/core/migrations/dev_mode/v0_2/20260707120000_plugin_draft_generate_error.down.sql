-- +migrate Dialect postgres
-- Revert: remove generate_error column from plugin_drafts.
ALTER TABLE plugin_drafts
    DROP COLUMN IF EXISTS generate_error;

-- +migrate Dialect sqlite
SELECT 1; -- Historical SQLite change is included by the first v0.2 dev migration.
