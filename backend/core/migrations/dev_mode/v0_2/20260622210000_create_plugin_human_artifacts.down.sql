-- +migrate Dialect postgres
ALTER TABLE plugin_slot_revisions
    DROP COLUMN IF EXISTS human_artifact_id;

DROP TABLE IF EXISTS plugin_human_artifacts;

-- +migrate Dialect sqlite
SELECT 1; -- Historical SQLite change is included by the first v0.2 dev migration.
