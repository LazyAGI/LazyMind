-- +migrate Dialect postgres
ALTER TABLE plugin_drafts DROP COLUMN IF EXISTS source_type;
ALTER TABLE plugin_drafts DROP COLUMN IF EXISTS source_skill_id;
ALTER TABLE plugin_drafts DROP COLUMN IF EXISTS source_skill_name;

-- +migrate Dialect sqlite
SELECT 1; -- Historical SQLite change is included by the first v0.2 dev migration.
