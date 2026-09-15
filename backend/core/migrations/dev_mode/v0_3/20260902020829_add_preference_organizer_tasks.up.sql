-- 20260902020829_add_preference_organizer_tasks
-- +migrate Up
-- +migrate Dialect postgres

ALTER TABLE public.resource_update_tasks
    ADD COLUMN result_json json,
    ADD COLUMN lane_key varchar(320) NOT NULL DEFAULT '',
    ADD COLUMN lane_priority integer NOT NULL DEFAULT 0,
    ADD COLUMN lane_order_at timestamp with time zone NOT NULL DEFAULT '1970-01-01 00:00:00+00';

UPDATE public.resource_update_tasks SET lane_order_at = created_at;
UPDATE public.resource_update_tasks AS task
SET lane_key = 'memory-maintenance:' || task.user_id,
    lane_priority = 10
WHERE task.task_type = 'generate_review'
  AND task.resource_type = 'memory'
  AND task.user_id <> ''
  AND (
      task.status = 'pending'
      OR (
          task.status = 'running'
          AND task.id = (
              SELECT MIN(running.id)
              FROM public.resource_update_tasks AS running
              WHERE running.user_id = task.user_id
                AND running.task_type = 'generate_review'
                AND running.resource_type = 'memory'
                AND running.status = 'running'
          )
      )
  );

ALTER TABLE public.resource_update_tasks
    DROP CONSTRAINT IF EXISTS chk_resource_update_tasks_task_type;
ALTER TABLE public.resource_update_tasks
    ADD CONSTRAINT chk_resource_update_tasks_task_type
    CHECK ((task_type)::text IN ('generate_review', 'auto_apply_review', 'auto_commit_skill_draft', 'organize_skill', 'organize_preference'));

ALTER TABLE public.resource_update_tasks
    DROP CONSTRAINT IF EXISTS chk_resource_update_tasks_trigger_type;
ALTER TABLE public.resource_update_tasks
    ADD CONSTRAINT chk_resource_update_tasks_trigger_type
    CHECK ((trigger_type)::text IN ('scheduled', 'conversation_idle', 'manual', 'review_result', 'auto_evo_enabled', 'preference_changed'));

CREATE INDEX idx_resource_update_tasks_lane_pending
    ON public.resource_update_tasks(status, lane_key, lane_priority DESC, lane_order_at, created_at);
CREATE UNIQUE INDEX uniq_resource_update_running_lane
    ON public.resource_update_tasks(lane_key)
    WHERE lane_key <> '' AND status = 'running';
CREATE UNIQUE INDEX uniq_active_preference_organizer
    ON public.resource_update_tasks(user_id)
    WHERE task_type = 'organize_preference' AND status IN ('pending', 'running');

-- +migrate Dialect sqlite

ALTER TABLE resource_update_tasks ADD COLUMN result_json json;
ALTER TABLE resource_update_tasks ADD COLUMN lane_key varchar(320) NOT NULL DEFAULT '';
ALTER TABLE resource_update_tasks ADD COLUMN lane_priority integer NOT NULL DEFAULT 0;
ALTER TABLE resource_update_tasks ADD COLUMN lane_order_at datetime NOT NULL DEFAULT '1970-01-01T00:00:00Z';
UPDATE resource_update_tasks SET lane_order_at = created_at;
UPDATE resource_update_tasks AS task
SET lane_key = 'memory-maintenance:' || task.user_id,
    lane_priority = 10
WHERE task.task_type = 'generate_review'
  AND task.resource_type = 'memory'
  AND task.user_id <> ''
  AND (
      task.status = 'pending'
      OR (
          task.status = 'running'
          AND task.id = (
              SELECT MIN(running.id)
              FROM resource_update_tasks AS running
              WHERE running.user_id = task.user_id
                AND running.task_type = 'generate_review'
                AND running.resource_type = 'memory'
                AND running.status = 'running'
          )
      )
  );

CREATE INDEX idx_resource_update_tasks_lane_pending
    ON resource_update_tasks(status, lane_key, lane_priority DESC, lane_order_at, created_at);
CREATE UNIQUE INDEX uniq_resource_update_running_lane
    ON resource_update_tasks(lane_key)
    WHERE lane_key <> '' AND status = 'running';
CREATE UNIQUE INDEX uniq_active_preference_organizer
    ON resource_update_tasks(user_id)
    WHERE task_type = 'organize_preference' AND status IN ('pending', 'running');
