-- 20260902020829_add_preference_organizer_tasks
-- +migrate Down
-- +migrate Dialect postgres

DROP INDEX IF EXISTS public.uniq_active_preference_organizer;
DROP INDEX IF EXISTS public.uniq_resource_update_running_lane;
DROP INDEX IF EXISTS public.idx_resource_update_tasks_lane_pending;
DELETE FROM public.resource_update_tasks WHERE task_type = 'organize_preference';

ALTER TABLE public.resource_update_tasks
    DROP CONSTRAINT IF EXISTS chk_resource_update_tasks_task_type;
ALTER TABLE public.resource_update_tasks
    ADD CONSTRAINT chk_resource_update_tasks_task_type
    CHECK ((task_type)::text IN ('generate_review', 'auto_apply_review', 'auto_commit_skill_draft', 'organize_skill'));

ALTER TABLE public.resource_update_tasks
    DROP CONSTRAINT IF EXISTS chk_resource_update_tasks_trigger_type;
ALTER TABLE public.resource_update_tasks
    ADD CONSTRAINT chk_resource_update_tasks_trigger_type
    CHECK ((trigger_type)::text IN ('scheduled', 'conversation_idle', 'manual', 'review_result', 'auto_evo_enabled'));

ALTER TABLE public.resource_update_tasks
    DROP COLUMN lane_order_at,
    DROP COLUMN lane_priority,
    DROP COLUMN lane_key,
    DROP COLUMN result_json;

-- +migrate Dialect sqlite

DROP INDEX IF EXISTS uniq_active_preference_organizer;
DROP INDEX IF EXISTS uniq_resource_update_running_lane;
DROP INDEX IF EXISTS idx_resource_update_tasks_lane_pending;
DELETE FROM resource_update_tasks WHERE task_type = 'organize_preference';
ALTER TABLE resource_update_tasks DROP COLUMN lane_order_at;
ALTER TABLE resource_update_tasks DROP COLUMN lane_priority;
ALTER TABLE resource_update_tasks DROP COLUMN lane_key;
ALTER TABLE resource_update_tasks DROP COLUMN result_json;
