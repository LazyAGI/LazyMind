-- 20260713190000_add_auto_commit_skill_draft_task
-- +migrate Down

DROP INDEX IF EXISTS public.uniq_active_skill_maintenance_admission;

ALTER TABLE public.resource_update_tasks
    DROP CONSTRAINT IF EXISTS chk_resource_update_tasks_task_type;

ALTER TABLE public.resource_update_tasks
    ADD CONSTRAINT chk_resource_update_tasks_task_type
    CHECK ((task_type)::text IN ('generate_review', 'auto_apply_review'));
