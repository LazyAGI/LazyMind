-- +migrate Dialect postgres
CREATE TABLE public.automation_groups (
    id varchar(36) PRIMARY KEY, user_id varchar(255) NOT NULL, name varchar(128) NOT NULL,
    remark text NOT NULL DEFAULT '', timezone varchar(64) NOT NULL DEFAULT 'Asia/Shanghai',
    enabled boolean NOT NULL DEFAULT true, created_at timestamptz NOT NULL, updated_at timestamptz NOT NULL
);
CREATE INDEX idx_automation_groups_user ON public.automation_groups(user_id);

ALTER TABLE public.user_schedules ADD COLUMN group_id varchar(36);
ALTER TABLE public.user_schedules ADD COLUMN group_position integer NOT NULL DEFAULT 0;
ALTER TABLE public.user_schedules ADD COLUMN definition_version integer NOT NULL DEFAULT 1;

CREATE TABLE public.schedule_dependencies (
    id varchar(36) PRIMARY KEY, user_id varchar(255) NOT NULL,
    source_schedule_id varchar(36) NOT NULL, target_schedule_id varchar(36) NOT NULL,
    window_type varchar(32) NOT NULL DEFAULT 'between_target_fires',
    window_config_json text, content_types_json text,
    incomplete_policy varchar(48) NOT NULL DEFAULT 'wait_then_run_with_warning',
    max_wait_seconds integer NOT NULL DEFAULT 7200, enabled boolean NOT NULL DEFAULT true,
    created_at timestamptz NOT NULL, updated_at timestamptz NOT NULL,
    CONSTRAINT chk_schedule_dependency_distinct CHECK (source_schedule_id <> target_schedule_id),
    CONSTRAINT uk_schedule_dependency UNIQUE (source_schedule_id, target_schedule_id)
);
CREATE INDEX idx_schedule_dependencies_target ON public.schedule_dependencies(target_schedule_id);
CREATE INDEX idx_schedule_dependencies_source ON public.schedule_dependencies(source_schedule_id);

ALTER TABLE public.task_center_tasks ADD COLUMN group_id varchar(36);
ALTER TABLE public.task_center_tasks ADD COLUMN scheduled_fire_at timestamptz;
ALTER TABLE public.task_center_tasks ADD COLUMN logical_slot_key varchar(160) NOT NULL DEFAULT '';
ALTER TABLE public.task_center_tasks ADD COLUMN window_start timestamptz;
ALTER TABLE public.task_center_tasks ADD COLUMN window_end timestamptz;
ALTER TABLE public.task_center_tasks ADD COLUMN trigger_type varchar(32) NOT NULL DEFAULT 'manual';
ALTER TABLE public.task_center_tasks ADD COLUMN attempt integer NOT NULL DEFAULT 1;
ALTER TABLE public.task_center_tasks ADD COLUMN definition_version integer NOT NULL DEFAULT 1;
ALTER TABLE public.task_center_tasks ADD COLUMN dependency_status varchar(32) NOT NULL DEFAULT 'none';
ALTER TABLE public.task_center_tasks ADD COLUMN has_late_inputs boolean NOT NULL DEFAULT false;
ALTER TABLE public.task_center_tasks DROP CONSTRAINT IF EXISTS chk_tct_status;
ALTER TABLE public.task_center_tasks ADD CONSTRAINT chk_tct_status CHECK (status IN ('pending','waiting_inputs','running','waiting','succeeded','failed','skipped','canceled'));
CREATE INDEX idx_task_center_schedule_execution
    ON public.task_center_tasks(schedule_id, scheduled_fire_at, created_at);

CREATE TABLE public.task_run_outputs (
    id varchar(36) PRIMARY KEY, task_id varchar(36) NOT NULL UNIQUE, conversation_id varchar(36) NOT NULL,
    final_answer_text text, summary_text text, artifact_manifest_json text,
    output_status varchar(24) NOT NULL, content_hash varchar(64) NOT NULL,
    created_at timestamptz NOT NULL, updated_at timestamptz NOT NULL
);
CREATE INDEX idx_task_run_outputs_conversation ON public.task_run_outputs(conversation_id);

CREATE TABLE public.task_run_inputs (
    id varchar(36) PRIMARY KEY, downstream_task_id varchar(36) NOT NULL, upstream_task_id varchar(36) NOT NULL,
    dependency_id varchar(36) NOT NULL, source_logical_slot_key varchar(160), output_id varchar(36) NOT NULL,
    output_content_hash varchar(64) NOT NULL, position integer NOT NULL, snapshot_json text, created_at timestamptz NOT NULL
);
CREATE INDEX idx_task_run_inputs_downstream ON public.task_run_inputs(downstream_task_id);
CREATE INDEX idx_task_run_inputs_upstream ON public.task_run_inputs(upstream_task_id);
CREATE UNIQUE INDEX uk_task_run_input_snapshot
    ON public.task_run_inputs(downstream_task_id, dependency_id, upstream_task_id);

-- Keep task conversations and derived run counts consistent with soft-deleted
-- task-center executions that may already exist when this migration is applied.
UPDATE public.conversations c
SET deleted_at = COALESCE(c.deleted_at, NOW()),
    updated_at = NOW()
WHERE c.is_task_conv = true
  AND EXISTS (
      SELECT 1
      FROM public.task_center_tasks t
      WHERE t.conversation_id = c.id
        AND t.archived_at IS NOT NULL
  )
  AND NOT EXISTS (
      SELECT 1
      FROM public.task_center_tasks t
      WHERE t.conversation_id = c.id
        AND t.archived_at IS NULL
  );

UPDATE public.user_schedules s
SET run_count = (
    SELECT COUNT(*)
    FROM public.task_center_tasks t
    WHERE t.schedule_id = s.id
      AND t.user_id = s.user_id
      AND t.archived_at IS NULL
);

-- +migrate Dialect sqlite
SELECT 1; -- Historical SQLite change is included by the first v0.2 dev migration.
