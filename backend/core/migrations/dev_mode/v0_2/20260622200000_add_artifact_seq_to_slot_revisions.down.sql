-- +migrate Dialect postgres
ALTER TABLE plugin_slot_revisions
    DROP COLUMN IF EXISTS artifact_seq;

-- +migrate Dialect sqlite
SELECT 1; -- Historical SQLite change is included by the first v0.2 dev migration.
