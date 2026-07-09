-- +migrate Down

ALTER TABLE public.resource_update_tasks
    DROP CONSTRAINT IF EXISTS chk_resource_update_tasks_trigger_type;

ALTER TABLE public.resource_update_tasks
    ADD CONSTRAINT chk_resource_update_tasks_trigger_type
    CHECK ((trigger_type)::text IN ('scheduled', 'conversation_idle', 'review_result', 'auto_evo_enabled'));
