-- 20260629100000_add_archived_at_to_taskcenter
-- +migrate Down
-- +migrate Dialect postgres

ALTER TABLE task_center_tasks DROP COLUMN IF EXISTS archived_at;

-- +migrate Dialect sqlite
SELECT 1; -- Historical SQLite change is included by the first v0.2 dev migration.
