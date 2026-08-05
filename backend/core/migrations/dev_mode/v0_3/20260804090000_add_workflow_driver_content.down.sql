-- +migrate Dialect postgres
ALTER TABLE plugin_drafts DROP COLUMN IF EXISTS driver_content;

-- +migrate Dialect sqlite
ALTER TABLE plugin_drafts DROP COLUMN driver_content;
