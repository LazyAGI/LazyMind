-- +migrate Dialect postgres
ALTER TABLE plugin_sessions DROP COLUMN IF EXISTS dismissed;

-- +migrate Dialect sqlite
SELECT 1; -- Historical SQLite change is included by the first v0.2 dev migration.
