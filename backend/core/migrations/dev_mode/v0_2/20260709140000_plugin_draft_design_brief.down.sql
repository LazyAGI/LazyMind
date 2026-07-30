-- +migrate Dialect postgres
ALTER TABLE plugin_drafts DROP COLUMN IF EXISTS design_brief_content;

-- +migrate Dialect sqlite
SELECT 1; -- Historical SQLite change is included by the first v0.2 dev migration.
