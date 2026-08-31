-- 20260825031307_add_chat_entry_defaults
-- +migrate Down
-- +migrate Dialect postgres

UPDATE public.conversations AS conversation
SET enable_plugin = CASE
        WHEN (SELECT backup.enable_plugin_was_null
              FROM public.conversation_policy_snapshot_backups AS backup
              WHERE backup.conversation_id = conversation.id) THEN NULL
        ELSE conversation.enable_plugin
    END,
    plugin_mode = CASE
        WHEN (SELECT backup.plugin_mode_was_null
              FROM public.conversation_policy_snapshot_backups AS backup
              WHERE backup.conversation_id = conversation.id) THEN NULL
        ELSE conversation.plugin_mode
    END,
    enable_subagent = CASE
        WHEN (SELECT backup.enable_subagent_was_null
              FROM public.conversation_policy_snapshot_backups AS backup
              WHERE backup.conversation_id = conversation.id) THEN NULL
        ELSE conversation.enable_subagent
    END
WHERE EXISTS (
    SELECT 1 FROM public.conversation_policy_snapshot_backups AS backup
    WHERE backup.conversation_id = conversation.id
);
DROP TABLE IF EXISTS public.conversation_policy_snapshot_backups;

ALTER TABLE public.user_chat_settings
    DROP COLUMN IF EXISTS quick_question_defaults,
    DROP COLUMN IF EXISTS new_task_defaults;
ALTER TABLE public.conversations DROP COLUMN IF EXISTS thinking_depth;

-- +migrate Dialect sqlite

UPDATE conversations
SET enable_plugin = CASE
        WHEN (SELECT backup.enable_plugin_was_null
              FROM conversation_policy_snapshot_backups AS backup
              WHERE backup.conversation_id = conversations.id) THEN NULL
        ELSE enable_plugin
    END,
    plugin_mode = CASE
        WHEN (SELECT backup.plugin_mode_was_null
              FROM conversation_policy_snapshot_backups AS backup
              WHERE backup.conversation_id = conversations.id) THEN NULL
        ELSE plugin_mode
    END,
    enable_subagent = CASE
        WHEN (SELECT backup.enable_subagent_was_null
              FROM conversation_policy_snapshot_backups AS backup
              WHERE backup.conversation_id = conversations.id) THEN NULL
        ELSE enable_subagent
    END
WHERE id IN (SELECT conversation_id FROM conversation_policy_snapshot_backups);
DROP TABLE IF EXISTS conversation_policy_snapshot_backups;

ALTER TABLE user_chat_settings DROP COLUMN quick_question_defaults;
ALTER TABLE user_chat_settings DROP COLUMN new_task_defaults;
ALTER TABLE conversations DROP COLUMN thinking_depth;
