-- 20260609120000_add_resource_update_schema
-- +migrate Down

DROP INDEX IF EXISTS public.idx_conversations_user_not_deleted;
DROP INDEX IF EXISTS public.idx_chat_histories_conversation_create_time;
DROP INDEX IF EXISTS public.idx_conversation_idle_events_session_waiting;
DROP INDEX IF EXISTS public.idx_conversation_idle_events_due;
DROP INDEX IF EXISTS public.idx_skill_review_scheduler_state_scan;
DROP INDEX IF EXISTS public.idx_resource_update_tasks_user_created;
DROP INDEX IF EXISTS public.idx_resource_update_tasks_running_lock;
DROP INDEX IF EXISTS public.idx_resource_update_tasks_pending;
DROP INDEX IF EXISTS public.uniq_skill_resources_owner_parent_skill_name;
DROP INDEX IF EXISTS public.idx_conversation_idle_events_status;
DROP INDEX IF EXISTS public.idx_conversation_idle_events_due_at;
DROP INDEX IF EXISTS public.idx_conversation_idle_events_user_id;
DROP INDEX IF EXISTS public.idx_conversation_idle_events_session_id;
DROP INDEX IF EXISTS public.uk_conversation_idle_events_event_id;
DROP INDEX IF EXISTS public.uniq_resource_update_active_auto_apply_result;
DROP INDEX IF EXISTS public.uniq_resource_update_task_trigger;
DROP INDEX IF EXISTS public.idx_resource_update_tasks_result_id;
DROP INDEX IF EXISTS public.idx_resource_update_tasks_review_result_id;
DROP INDEX IF EXISTS public.idx_resource_update_tasks_status;
DROP INDEX IF EXISTS public.idx_resource_update_tasks_trigger_id;
DROP INDEX IF EXISTS public.idx_resource_update_tasks_trigger_type;
DROP INDEX IF EXISTS public.idx_resource_update_tasks_resource_id;
DROP INDEX IF EXISTS public.idx_resource_update_tasks_user_id;
DROP INDEX IF EXISTS public.idx_resource_update_tasks_resource_type;
DROP INDEX IF EXISTS public.idx_resource_update_tasks_task_type;

ALTER TABLE public.chat_histories
    DROP CONSTRAINT IF EXISTS chk_chat_histories_tool_call_turns_non_negative;

ALTER TABLE public.chat_histories
    DROP COLUMN IF EXISTS tool_call_turns;

DROP TABLE IF EXISTS public.conversation_idle_events;
DROP TABLE IF EXISTS public.skill_review_scheduler_state;
DROP TABLE IF EXISTS public.resource_update_tasks;
