-- +migrate Dialect postgres
ALTER TABLE plugin_drafts ADD COLUMN IF NOT EXISTS driver_content TEXT NOT NULL DEFAULT '';

-- +migrate Dialect sqlite
ALTER TABLE plugin_drafts ADD COLUMN driver_content TEXT NOT NULL DEFAULT '';
