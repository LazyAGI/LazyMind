CREATE TABLE public.schedule_fires (
    id varchar(36) PRIMARY KEY, schedule_id varchar(36) NOT NULL, scheduled_fire_at timestamptz NOT NULL,
    logical_slot_key varchar(160) NOT NULL, status varchar(24) NOT NULL, task_id varchar(36),
    attempt integer NOT NULL DEFAULT 1, created_at timestamptz NOT NULL, updated_at timestamptz NOT NULL,
    CONSTRAINT uk_schedule_fire UNIQUE (schedule_id, scheduled_fire_at, attempt)
);

DROP INDEX IF EXISTS public.uk_task_run_input_snapshot;
DROP INDEX IF EXISTS public.idx_task_center_schedule_execution;
