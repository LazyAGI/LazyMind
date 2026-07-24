-- 20260629100000_add_archived_at_to_taskcenter
-- +migrate Down

ALTER TABLE task_center_tasks DROP COLUMN IF EXISTS archived_at;
