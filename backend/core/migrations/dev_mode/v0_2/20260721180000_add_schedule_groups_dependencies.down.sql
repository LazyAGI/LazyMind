-- +migrate Dialect postgres
DROP TABLE IF EXISTS public.task_run_inputs;
DROP TABLE IF EXISTS public.task_run_outputs;
DROP INDEX IF EXISTS public.idx_task_center_schedule_execution;
ALTER TABLE public.task_center_tasks DROP COLUMN IF EXISTS has_late_inputs;
ALTER TABLE public.task_center_tasks DROP COLUMN IF EXISTS dependency_status;
ALTER TABLE public.task_center_tasks DROP COLUMN IF EXISTS definition_version;
ALTER TABLE public.task_center_tasks DROP COLUMN IF EXISTS attempt;
ALTER TABLE public.task_center_tasks DROP COLUMN IF EXISTS trigger_type;
ALTER TABLE public.task_center_tasks DROP COLUMN IF EXISTS window_end;
ALTER TABLE public.task_center_tasks DROP COLUMN IF EXISTS window_start;
ALTER TABLE public.task_center_tasks DROP COLUMN IF EXISTS logical_slot_key;
ALTER TABLE public.task_center_tasks DROP COLUMN IF EXISTS scheduled_fire_at;
ALTER TABLE public.task_center_tasks DROP COLUMN IF EXISTS group_id;
DROP TABLE IF EXISTS public.schedule_dependencies;
ALTER TABLE public.user_schedules DROP COLUMN IF EXISTS definition_version;
ALTER TABLE public.user_schedules DROP COLUMN IF EXISTS group_position;
ALTER TABLE public.user_schedules DROP COLUMN IF EXISTS group_id;
DROP TABLE IF EXISTS public.automation_groups;

-- Soft-deleted task conversations are not restored because users may also have
-- deleted them independently. user_schedules.run_count remains derivable data.

-- +migrate Dialect sqlite
SELECT 1; -- Historical SQLite change is included by the first v0.2 dev migration.
