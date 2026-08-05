-- +migrate Up
-- +migrate Dialect postgres

ALTER TABLE public.resource_update_tasks
    DROP CONSTRAINT IF EXISTS chk_resource_update_tasks_trigger_type;

ALTER TABLE public.resource_update_tasks
    ADD CONSTRAINT chk_resource_update_tasks_trigger_type
    CHECK ((trigger_type)::text IN ('scheduled', 'conversation_idle', 'manual', 'review_result', 'auto_evo_enabled'));

-- +migrate Dialect sqlite
SELECT 1; -- Historical SQLite change is included by the first v0.2 dev migration.
