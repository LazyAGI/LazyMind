-- +migrate Dialect postgres
DROP INDEX IF EXISTS idx_plugin_drafts_user_plugin_id;
ALTER TABLE plugin_drafts DROP COLUMN IF EXISTS plugin_id;

-- +migrate Dialect sqlite
SELECT 1; -- Historical SQLite change is included by the first v0.2 dev migration.
