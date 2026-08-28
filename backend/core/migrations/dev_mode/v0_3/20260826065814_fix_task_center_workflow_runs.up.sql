-- 20260826065814_fix_task_center_workflow_runs
-- +migrate Up
-- +migrate Dialect postgres
ALTER TABLE public.task_center_tasks DROP CONSTRAINT IF EXISTS chk_tct_task_type;
UPDATE public.task_center_tasks SET task_type = 'workflow_run' WHERE task_type = 'plugin_run';
ALTER TABLE public.task_center_tasks
    ADD CONSTRAINT chk_tct_task_type
    CHECK (task_type IN ('workflow_run', 'background_chat', 'scheduled'));

INSERT INTO public.task_center_tasks (
    id, user_id, conversation_id, plugin_session_id, task_type, title,
    status, progress_json, created_at, updated_at, finished_at,
    archived_at, archived_reason
)
SELECT
    'tc_' || substr(replace(ps.id, '-', ''), 1, 32),
    COALESCE(NULLIF(ps.create_user_id, ''), c.create_user_id),
    ps.conversation_id,
    ps.id,
    'workflow_run',
    COALESCE(NULLIF(c.display_name, ''), NULLIF(ps.plugin_id, ''), 'Workflow task'),
    CASE ps.status
        WHEN 'active' THEN 'running'
        WHEN 'waiting' THEN 'waiting'
        WHEN 'completed' THEN 'succeeded'
        WHEN 'failed' THEN 'failed'
        WHEN 'stopped' THEN 'canceled'
        ELSE 'pending'
    END,
    '{}',
    ps.created_at,
    ps.updated_at,
    CASE WHEN ps.status IN ('completed', 'failed', 'stopped') THEN ps.updated_at ELSE NULL END,
    CASE
        WHEN c.id IS NULL THEN ps.updated_at
        ELSE COALESCE(c.deleted_at, c.archived_at)
    END,
    CASE
        WHEN c.id IS NULL THEN 'conversation_purged'
        WHEN c.deleted_at IS NOT NULL THEN 'conversation_trash'
        WHEN c.archived_at IS NOT NULL THEN 'conversation_archive'
        ELSE ''
    END
FROM public.plugin_sessions AS ps
LEFT JOIN public.conversations AS c ON c.id = ps.conversation_id
WHERE COALESCE(NULLIF(ps.create_user_id, ''), c.create_user_id) IS NOT NULL
  AND NOT EXISTS (
      SELECT 1 FROM public.task_center_tasks AS existing
      WHERE existing.plugin_session_id = ps.id
  )
ON CONFLICT (id) DO NOTHING;

-- +migrate Dialect sqlite
UPDATE task_center_tasks SET task_type = 'workflow_run' WHERE task_type = 'plugin_run';

INSERT INTO task_center_tasks (
    id, user_id, conversation_id, plugin_session_id, task_type, title,
    status, progress_json, created_at, updated_at, finished_at,
    archived_at, archived_reason
)
SELECT
    'tc_' || substr(replace(ps.id, '-', ''), 1, 32),
    COALESCE(NULLIF(ps.create_user_id, ''), c.create_user_id),
    ps.conversation_id,
    ps.id,
    'workflow_run',
    COALESCE(NULLIF(c.display_name, ''), NULLIF(ps.plugin_id, ''), 'Workflow task'),
    CASE ps.status
        WHEN 'active' THEN 'running'
        WHEN 'waiting' THEN 'waiting'
        WHEN 'completed' THEN 'succeeded'
        WHEN 'failed' THEN 'failed'
        WHEN 'stopped' THEN 'canceled'
        ELSE 'pending'
    END,
    '{}',
    ps.created_at,
    ps.updated_at,
    CASE WHEN ps.status IN ('completed', 'failed', 'stopped') THEN ps.updated_at ELSE NULL END,
    CASE
        WHEN c.id IS NULL THEN ps.updated_at
        ELSE COALESCE(c.deleted_at, c.archived_at)
    END,
    CASE
        WHEN c.id IS NULL THEN 'conversation_purged'
        WHEN c.deleted_at IS NOT NULL THEN 'conversation_trash'
        WHEN c.archived_at IS NOT NULL THEN 'conversation_archive'
        ELSE ''
    END
FROM plugin_sessions AS ps
LEFT JOIN conversations AS c ON c.id = ps.conversation_id
WHERE COALESCE(NULLIF(ps.create_user_id, ''), c.create_user_id) IS NOT NULL
  AND NOT EXISTS (
      SELECT 1 FROM task_center_tasks AS existing
      WHERE existing.plugin_session_id = ps.id
  )
ON CONFLICT (id) DO NOTHING;
