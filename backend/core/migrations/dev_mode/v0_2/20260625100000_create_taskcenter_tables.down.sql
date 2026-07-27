-- 20260625100000_create_taskcenter_tables
-- +migrate Down
-- +migrate Dialect postgres

DROP TABLE IF EXISTS public.task_center_tasks;
DROP TABLE IF EXISTS public.user_schedules;

-- +migrate Dialect sqlite
SELECT 1; -- Historical SQLite change is included by the first v0.2 dev migration.
