-- 20260629100000_add_archived_at_to_taskcenter
-- +migrate Up
-- +migrate Dialect postgres

ALTER TABLE task_center_tasks ADD COLUMN IF NOT EXISTS archived_at timestamp with time zone;

-- +migrate Dialect sqlite
SELECT 1; -- Historical SQLite change is included by the first v0.2 dev migration.
