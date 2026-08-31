-- 20260825031307_add_chat_entry_defaults
-- +migrate Up
-- +migrate Dialect postgres

ALTER TABLE public.user_chat_settings
    ADD COLUMN IF NOT EXISTS quick_question_defaults JSONB NOT NULL
    DEFAULT '{"thinking_depth":"medium","conversation_settings":{"chat_executor":"lazymind","enable_workflow":false,"workflow_mode":"dynamic","enable_subagent":true}}'::jsonb;
ALTER TABLE public.user_chat_settings
    ADD COLUMN IF NOT EXISTS new_task_defaults JSONB NOT NULL
    DEFAULT '{"thinking_depth":"high","conversation_settings":{"chat_executor":"lazymind","enable_workflow":true,"workflow_mode":"dynamic","enable_subagent":true}}'::jsonb;

UPDATE public.user_chat_settings
SET quick_question_defaults = jsonb_build_object(
        'thinking_depth', 'medium',
        'conversation_settings', jsonb_build_object(
            'chat_executor', 'lazymind',
            'enable_workflow', false,
            'workflow_mode', CASE WHEN plugin_mode IN ('auto', 'dynamic') THEN plugin_mode ELSE 'dynamic' END,
            'enable_subagent', enable_subagent
        )
    ),
    new_task_defaults = jsonb_build_object(
        'thinking_depth', 'high',
        'conversation_settings', jsonb_build_object(
            'chat_executor', 'lazymind',
            'enable_workflow', enable_workflow,
            'workflow_mode', CASE WHEN plugin_mode IN ('auto', 'dynamic') THEN plugin_mode ELSE 'dynamic' END,
            'enable_subagent', enable_subagent
        )
    );

CREATE TABLE IF NOT EXISTS public.conversation_policy_snapshot_backups (
    conversation_id VARCHAR(36) PRIMARY KEY,
    enable_plugin_was_null BOOLEAN NOT NULL,
    plugin_mode_was_null BOOLEAN NOT NULL,
    enable_subagent_was_null BOOLEAN NOT NULL
);
INSERT INTO public.conversation_policy_snapshot_backups (
    conversation_id,
    enable_plugin_was_null,
    plugin_mode_was_null,
    enable_subagent_was_null
)
SELECT id, enable_plugin IS NULL, plugin_mode IS NULL, enable_subagent IS NULL
FROM public.conversations
WHERE enable_plugin IS NULL OR plugin_mode IS NULL OR enable_subagent IS NULL
ON CONFLICT (conversation_id) DO NOTHING;

UPDATE public.conversations AS conversation
SET enable_plugin = COALESCE(
        conversation.enable_plugin,
        (SELECT settings.enable_workflow
         FROM public.user_chat_settings AS settings
         WHERE settings.user_id = conversation.create_user_id),
        TRUE
    ),
    plugin_mode = COALESCE(
        conversation.plugin_mode,
        (SELECT CASE
             WHEN settings.plugin_mode IN ('auto', 'dynamic') THEN settings.plugin_mode
             ELSE 'dynamic'
         END
         FROM public.user_chat_settings AS settings
         WHERE settings.user_id = conversation.create_user_id),
        'dynamic'
    ),
    enable_subagent = COALESCE(
        conversation.enable_subagent,
        (SELECT settings.enable_subagent
         FROM public.user_chat_settings AS settings
         WHERE settings.user_id = conversation.create_user_id),
        TRUE
    )
WHERE EXISTS (
    SELECT 1 FROM public.conversation_policy_snapshot_backups AS backup
    WHERE backup.conversation_id = conversation.id
)
  AND (conversation.enable_plugin IS NULL
       OR conversation.plugin_mode IS NULL
       OR conversation.enable_subagent IS NULL);

ALTER TABLE public.conversations
    ADD COLUMN IF NOT EXISTS thinking_depth VARCHAR(16) NOT NULL DEFAULT 'medium';

-- +migrate Dialect sqlite

ALTER TABLE user_chat_settings
    ADD COLUMN quick_question_defaults JSON NOT NULL
    DEFAULT '{"thinking_depth":"medium","conversation_settings":{"chat_executor":"lazymind","enable_workflow":false,"workflow_mode":"dynamic","enable_subagent":true}}';
ALTER TABLE user_chat_settings
    ADD COLUMN new_task_defaults JSON NOT NULL
    DEFAULT '{"thinking_depth":"high","conversation_settings":{"chat_executor":"lazymind","enable_workflow":true,"workflow_mode":"dynamic","enable_subagent":true}}';

UPDATE user_chat_settings
SET quick_question_defaults =
        '{"thinking_depth":"medium","conversation_settings":{"chat_executor":"lazymind","enable_workflow":false,"workflow_mode":"' ||
        CASE WHEN plugin_mode IN ('auto', 'dynamic') THEN plugin_mode ELSE 'dynamic' END ||
        '","enable_subagent":' || CASE WHEN enable_subagent THEN 'true' ELSE 'false' END || '}}',
    new_task_defaults =
        '{"thinking_depth":"high","conversation_settings":{"chat_executor":"lazymind","enable_workflow":' ||
        CASE WHEN enable_workflow THEN 'true' ELSE 'false' END ||
        ',"workflow_mode":"' || CASE WHEN plugin_mode IN ('auto', 'dynamic') THEN plugin_mode ELSE 'dynamic' END ||
        '","enable_subagent":' || CASE WHEN enable_subagent THEN 'true' ELSE 'false' END || '}}';

CREATE TABLE IF NOT EXISTS conversation_policy_snapshot_backups (
    conversation_id VARCHAR(36) PRIMARY KEY,
    enable_plugin_was_null BOOLEAN NOT NULL,
    plugin_mode_was_null BOOLEAN NOT NULL,
    enable_subagent_was_null BOOLEAN NOT NULL
);
INSERT OR IGNORE INTO conversation_policy_snapshot_backups (
    conversation_id,
    enable_plugin_was_null,
    plugin_mode_was_null,
    enable_subagent_was_null
)
SELECT id, enable_plugin IS NULL, plugin_mode IS NULL, enable_subagent IS NULL
FROM conversations
WHERE enable_plugin IS NULL OR plugin_mode IS NULL OR enable_subagent IS NULL;

UPDATE conversations
SET enable_plugin = COALESCE(
        enable_plugin,
        (SELECT settings.enable_workflow
         FROM user_chat_settings AS settings
         WHERE settings.user_id = conversations.create_user_id),
        true
    ),
    plugin_mode = COALESCE(
        plugin_mode,
        (SELECT CASE
             WHEN settings.plugin_mode IN ('auto', 'dynamic') THEN settings.plugin_mode
             ELSE 'dynamic'
         END
         FROM user_chat_settings AS settings
         WHERE settings.user_id = conversations.create_user_id),
        'dynamic'
    ),
    enable_subagent = COALESCE(
        enable_subagent,
        (SELECT settings.enable_subagent
         FROM user_chat_settings AS settings
         WHERE settings.user_id = conversations.create_user_id),
        true
    )
WHERE id IN (SELECT conversation_id FROM conversation_policy_snapshot_backups)
  AND (enable_plugin IS NULL
       OR plugin_mode IS NULL
       OR enable_subagent IS NULL);

ALTER TABLE conversations
    ADD COLUMN thinking_depth VARCHAR(16) NOT NULL DEFAULT 'medium';
