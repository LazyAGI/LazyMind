DROP INDEX IF EXISTS public.uk_task_run_input_consumption;

CREATE UNIQUE INDEX IF NOT EXISTS uk_task_run_input_snapshot
    ON public.task_run_inputs(downstream_task_id, dependency_id, upstream_task_id);
