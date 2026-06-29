-- 20260625100000_create_taskcenter_tables
-- +migrate Down

DROP TABLE IF EXISTS public.task_center_tasks;
DROP TABLE IF EXISTS public.user_schedules;
