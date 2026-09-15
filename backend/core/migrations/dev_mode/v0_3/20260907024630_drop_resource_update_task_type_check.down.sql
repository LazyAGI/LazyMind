-- 20260907024630_drop_resource_update_task_type_check
-- +migrate Down
-- +migrate Dialect postgres

ALTER TABLE public.resource_update_tasks
    ADD CONSTRAINT chk_resource_update_tasks_task_type
    CHECK ((task_type)::text IN ('generate_review', 'auto_apply_review', 'auto_commit_skill_draft', 'organize_skill', 'organize_preference'));

-- +migrate Dialect sqlite
SELECT 1; -- SQLite did not have this constraint.
