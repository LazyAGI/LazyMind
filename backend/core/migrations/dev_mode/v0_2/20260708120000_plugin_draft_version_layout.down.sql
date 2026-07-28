-- +migrate Dialect postgres
ALTER TABLE plugin_drafts DROP COLUMN IF EXISTS state_layout_content;
ALTER TABLE plugin_drafts DROP COLUMN IF EXISTS version;

-- +migrate Dialect sqlite
SELECT 1; -- Historical SQLite change is included by the first v0.2 dev migration.
