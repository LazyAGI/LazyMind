-- 20260826065814_fix_task_center_workflow_runs
-- +migrate Down
-- +migrate Dialect postgres
ALTER TABLE public.task_center_tasks DROP CONSTRAINT IF EXISTS chk_tct_task_type;
UPDATE public.task_center_tasks SET task_type = 'plugin_run' WHERE task_type = 'workflow_run';
ALTER TABLE public.task_center_tasks
    ADD CONSTRAINT chk_tct_task_type
    CHECK (((task_type)::text = ANY (ARRAY[
        'plugin_run'::text,
        'background_chat'::text,
        'scheduled'::text
    ])));

-- +migrate Dialect sqlite
UPDATE task_center_tasks SET task_type = 'plugin_run' WHERE task_type = 'workflow_run';
