-- 20260629100000_add_archived_at_to_taskcenter
-- +migrate Up

ALTER TABLE task_center_tasks ADD COLUMN IF NOT EXISTS archived_at timestamp with time zone;
