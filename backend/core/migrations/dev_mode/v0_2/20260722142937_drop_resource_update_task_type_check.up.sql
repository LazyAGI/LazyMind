-- 20260722142937_drop_resource_update_task_type_check
-- +migrate Up
-- +migrate Dialect postgres

-- Task types are application-defined and may grow without a schema change.
ALTER TABLE public.resource_update_tasks
    DROP CONSTRAINT IF EXISTS chk_resource_update_tasks_task_type;

-- +migrate Dialect sqlite
SELECT 1; -- Historical SQLite change is included by the first v0.2 dev migration.
