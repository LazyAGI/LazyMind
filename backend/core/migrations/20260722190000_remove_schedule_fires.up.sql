DROP TABLE IF EXISTS public.schedule_fires;

CREATE INDEX IF NOT EXISTS idx_task_center_schedule_execution
    ON public.task_center_tasks(schedule_id, scheduled_fire_at, created_at);

CREATE UNIQUE INDEX IF NOT EXISTS uk_task_run_input_snapshot
    ON public.task_run_inputs(downstream_task_id, dependency_id, upstream_task_id);
