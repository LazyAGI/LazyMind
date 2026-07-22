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
