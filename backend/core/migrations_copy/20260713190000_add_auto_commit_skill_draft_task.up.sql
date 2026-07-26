-- 20260713190000_add_auto_commit_skill_draft_task
-- +migrate Up

ALTER TABLE public.resource_update_tasks
    DROP CONSTRAINT IF EXISTS chk_resource_update_tasks_task_type;

ALTER TABLE public.resource_update_tasks
    ADD CONSTRAINT chk_resource_update_tasks_task_type
    CHECK ((task_type)::text IN ('generate_review', 'auto_apply_review', 'auto_commit_skill_draft', 'organize_skill'));

CREATE UNIQUE INDEX IF NOT EXISTS uniq_active_skill_maintenance_admission
    ON public.resource_update_tasks(user_id)
    WHERE resource_type = 'skill'
      AND task_type IN ('generate_review', 'organize_skill')
      AND status IN ('pending', 'running');
