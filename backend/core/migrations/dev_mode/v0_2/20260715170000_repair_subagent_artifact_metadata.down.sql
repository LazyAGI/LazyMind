-- +migrate Dialect postgres
DROP INDEX IF EXISTS idx_saa_task_visible;

-- Keep the repaired columns: migration 20260618100000 owns their lifecycle.

-- +migrate Dialect sqlite
SELECT 1; -- Historical SQLite change is included by the first v0.2 dev migration.
