-- +migrate Dialect postgres
CREATE TABLE IF NOT EXISTS plugin_step_intents (
    id VARCHAR(36) PRIMARY KEY,
    session_id VARCHAR(36) NOT NULL,
    step_id VARCHAR(64) NOT NULL,
    intent_context TEXT NOT NULL DEFAULT '{}',
    updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE UNIQUE INDEX IF NOT EXISTS uk_plugin_step_intent
    ON plugin_step_intents (session_id, step_id);

ALTER TABLE chat_histories ADD COLUMN IF NOT EXISTS run_id VARCHAR(64);
ALTER TABLE chat_histories ADD COLUMN IF NOT EXISTS run_status VARCHAR(32);
ALTER TABLE chat_histories ADD COLUMN IF NOT EXISTS run_terminal JSONB;
CREATE INDEX IF NOT EXISTS idx_chat_histories_run_id ON chat_histories(run_id);
ALTER TABLE multi_answers_chat_histories ADD COLUMN IF NOT EXISTS run_id VARCHAR(64);
ALTER TABLE multi_answers_chat_histories ADD COLUMN IF NOT EXISTS run_status VARCHAR(32);
ALTER TABLE multi_answers_chat_histories ADD COLUMN IF NOT EXISTS run_terminal JSONB;
CREATE INDEX IF NOT EXISTS idx_multi_answers_chat_histories_run_id ON multi_answers_chat_histories(run_id);

-- +migrate Dialect sqlite
ALTER TABLE chat_histories ADD COLUMN run_id TEXT;
ALTER TABLE chat_histories ADD COLUMN run_status TEXT;
ALTER TABLE chat_histories ADD COLUMN run_terminal TEXT;
CREATE INDEX IF NOT EXISTS idx_chat_histories_run_id ON chat_histories(run_id);
ALTER TABLE multi_answers_chat_histories ADD COLUMN run_id TEXT;
ALTER TABLE multi_answers_chat_histories ADD COLUMN run_status TEXT;
ALTER TABLE multi_answers_chat_histories ADD COLUMN run_terminal TEXT;
CREATE INDEX IF NOT EXISTS idx_multi_answers_chat_histories_run_id ON multi_answers_chat_histories(run_id);

-- +migrate Dialect postgres
ALTER TABLE user_ui_preferences
    ADD COLUMN IF NOT EXISTS task_center_enabled BOOLEAN NOT NULL DEFAULT TRUE;
ALTER TABLE user_ui_preferences
    ADD COLUMN IF NOT EXISTS schedules_enabled BOOLEAN NOT NULL DEFAULT TRUE;
UPDATE user_ui_preferences SET schedules_enabled = task_center_enabled;
ALTER TABLE user_ui_preferences
    ADD COLUMN IF NOT EXISTS skills_enabled BOOLEAN NOT NULL DEFAULT TRUE;
ALTER TABLE user_ui_preferences
    ADD COLUMN IF NOT EXISTS mcp_enabled BOOLEAN NOT NULL DEFAULT TRUE;
ALTER TABLE user_ui_preferences
    ADD COLUMN IF NOT EXISTS workflows_enabled BOOLEAN NOT NULL DEFAULT TRUE;
UPDATE user_ui_preferences SET workflows_enabled = skills_enabled;
ALTER TABLE user_ui_preferences
    ADD COLUMN IF NOT EXISTS document_parsing_enabled BOOLEAN NOT NULL DEFAULT TRUE;
ALTER TABLE user_ui_preferences
    ADD COLUMN IF NOT EXISTS performance_stats_enabled BOOLEAN NOT NULL DEFAULT FALSE;
ALTER TABLE sub_agent_tasks
    ADD COLUMN IF NOT EXISTS sources JSONB NOT NULL DEFAULT '[]'::jsonb;

-- +migrate Dialect sqlite
CREATE TABLE IF NOT EXISTS plugin_step_intents (
    id VARCHAR(36) PRIMARY KEY,
    session_id VARCHAR(36) NOT NULL,
    step_id VARCHAR(64) NOT NULL,
    intent_context TEXT NOT NULL DEFAULT '{}',
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP
);
CREATE UNIQUE INDEX IF NOT EXISTS uk_plugin_step_intent
    ON plugin_step_intents (session_id, step_id);

ALTER TABLE user_ui_preferences ADD COLUMN task_center_enabled BOOLEAN NOT NULL DEFAULT TRUE;
ALTER TABLE user_ui_preferences ADD COLUMN schedules_enabled BOOLEAN NOT NULL DEFAULT TRUE;
UPDATE user_ui_preferences SET schedules_enabled = task_center_enabled;
ALTER TABLE user_ui_preferences ADD COLUMN skills_enabled BOOLEAN NOT NULL DEFAULT TRUE;
ALTER TABLE user_ui_preferences ADD COLUMN mcp_enabled BOOLEAN NOT NULL DEFAULT TRUE;
ALTER TABLE user_ui_preferences ADD COLUMN workflows_enabled BOOLEAN NOT NULL DEFAULT TRUE;
UPDATE user_ui_preferences SET workflows_enabled = skills_enabled;
ALTER TABLE user_ui_preferences ADD COLUMN document_parsing_enabled BOOLEAN NOT NULL DEFAULT TRUE;
ALTER TABLE user_ui_preferences ADD COLUMN performance_stats_enabled BOOLEAN NOT NULL DEFAULT FALSE;
ALTER TABLE sub_agent_tasks ADD COLUMN sources JSON NOT NULL DEFAULT '[]';

-- +migrate Dialect postgres
ALTER TABLE user_plugin_settings
    ADD COLUMN IF NOT EXISTS call_mode VARCHAR(16) NOT NULL DEFAULT 'disabled';
UPDATE user_plugin_settings
SET call_mode = CASE WHEN enabled THEN 'auto' ELSE 'disabled' END
WHERE call_mode = 'disabled';

DO $$
BEGIN
    IF EXISTS (
        SELECT 1
        FROM information_schema.columns
        WHERE table_schema = 'public'
          AND table_name = 'user_chat_settings'
          AND column_name = 'enable_plugin'
    ) AND NOT EXISTS (
        SELECT 1
        FROM information_schema.columns
        WHERE table_schema = 'public'
          AND table_name = 'user_chat_settings'
          AND column_name = 'enable_workflow'
    ) THEN
        ALTER TABLE public.user_chat_settings RENAME COLUMN enable_plugin TO enable_workflow;
    ELSIF NOT EXISTS (
        SELECT 1
        FROM information_schema.columns
        WHERE table_schema = 'public'
          AND table_name = 'user_chat_settings'
          AND column_name = 'enable_workflow'
    ) THEN
        ALTER TABLE public.user_chat_settings ADD COLUMN enable_workflow BOOLEAN NOT NULL DEFAULT TRUE;
    END IF;
END $$;

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

ALTER TABLE conversations
    ADD COLUMN IF NOT EXISTS chat_executor VARCHAR(32) NOT NULL DEFAULT 'lazymind';
ALTER TABLE conversations
    ADD COLUMN IF NOT EXISTS thinking_depth VARCHAR(16) NOT NULL DEFAULT 'medium';

ALTER TABLE plugin_sessions ADD COLUMN IF NOT EXISTS origin_host VARCHAR(32) NOT NULL DEFAULT 'lazymind';
ALTER TABLE plugin_sessions ADD COLUMN IF NOT EXISTS origin_ref VARCHAR(255) NOT NULL DEFAULT '';
ALTER TABLE plugin_sessions ADD COLUMN IF NOT EXISTS controller_host VARCHAR(32) NOT NULL DEFAULT 'lazymind';
CREATE INDEX IF NOT EXISTS idx_plugin_sessions_origin ON plugin_sessions(origin_host, origin_ref);

-- +migrate Dialect sqlite
ALTER TABLE user_plugin_settings ADD COLUMN call_mode varchar(16) NOT NULL DEFAULT 'disabled';
UPDATE user_plugin_settings
SET call_mode = CASE WHEN enabled THEN 'auto' ELSE 'disabled' END
WHERE call_mode = 'disabled';

CREATE TABLE IF NOT EXISTS user_chat_settings_next (
    user_id varchar(255),
    enable_workflow numeric NOT NULL DEFAULT true,
    plugin_mode varchar(16) NOT NULL DEFAULT "dynamic",
    enable_subagent numeric NOT NULL DEFAULT true,
    updated_at datetime NOT NULL,
    PRIMARY KEY (user_id)
);
DELETE FROM user_chat_settings_next;
INSERT INTO user_chat_settings_next SELECT * FROM user_chat_settings;
DROP TABLE user_chat_settings;
ALTER TABLE user_chat_settings_next RENAME TO user_chat_settings;

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
    ADD COLUMN chat_executor VARCHAR(32) NOT NULL DEFAULT 'lazymind';
ALTER TABLE conversations
    ADD COLUMN thinking_depth VARCHAR(16) NOT NULL DEFAULT 'medium';

ALTER TABLE plugin_sessions ADD COLUMN origin_host varchar(32) NOT NULL DEFAULT 'lazymind';
ALTER TABLE plugin_sessions ADD COLUMN origin_ref varchar(255) NOT NULL DEFAULT '';
ALTER TABLE plugin_sessions ADD COLUMN controller_host varchar(32) NOT NULL DEFAULT 'lazymind';
CREATE INDEX IF NOT EXISTS idx_plugin_sessions_origin ON plugin_sessions(origin_host, origin_ref);

-- +migrate Dialect postgres
-- Expand-only Workflow v1 facade persistence. Legacy plugin_* Runtime tables
-- remain authoritative and unchanged during the shadow/compatibility window.
CREATE TABLE IF NOT EXISTS workflow_preparations (
    id VARCHAR(36) PRIMARY KEY,
    idempotency_key VARCHAR(255) NOT NULL,
    owner_user_id VARCHAR(255) NOT NULL,
    workflow_id VARCHAR(255) NOT NULL,
    contract_version VARCHAR(32) NOT NULL,
    request_json JSONB NOT NULL,
    response_json JSONB NOT NULL,
    consumed_at TIMESTAMP NULL,
    session_id VARCHAR(36) NOT NULL DEFAULT '',
    created_at TIMESTAMP NOT NULL,
    updated_at TIMESTAMP NOT NULL,
    CONSTRAINT uk_workflow_preparation_owner_key UNIQUE (owner_user_id, idempotency_key)
);
CREATE INDEX IF NOT EXISTS idx_workflow_preparations_owner ON workflow_preparations(owner_user_id);

CREATE TABLE IF NOT EXISTS workflow_commands (
    command_id VARCHAR(255) PRIMARY KEY,
    owner_user_id VARCHAR(255) NOT NULL,
    session_id VARCHAR(36) NOT NULL,
    contract_version VARCHAR(32) NOT NULL,
    request_hash VARCHAR(64) NOT NULL,
    http_status INTEGER NOT NULL,
    response_json JSONB NOT NULL,
    created_at TIMESTAMP NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_workflow_commands_owner ON workflow_commands(owner_user_id);
CREATE INDEX IF NOT EXISTS idx_workflow_commands_session ON workflow_commands(session_id);

CREATE TABLE IF NOT EXISTS workflow_events (
    id BIGSERIAL PRIMARY KEY,
    session_id VARCHAR(36) NOT NULL,
    owner_user_id VARCHAR(255) NOT NULL,
    contract_version VARCHAR(32) NOT NULL,
    event_type VARCHAR(64) NOT NULL,
    entity_id VARCHAR(255) NOT NULL DEFAULT '',
    state_version BIGINT NOT NULL DEFAULT 0,
    command_id VARCHAR(255) NOT NULL DEFAULT '',
    payload_json JSONB NOT NULL,
    created_at TIMESTAMP NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_workflow_events_session_cursor ON workflow_events(session_id, id);
CREATE INDEX IF NOT EXISTS idx_workflow_events_owner ON workflow_events(owner_user_id);
CREATE INDEX IF NOT EXISTS idx_workflow_events_command ON workflow_events(command_id);

-- +migrate Dialect sqlite
CREATE TABLE IF NOT EXISTS workflow_preparations (
    id TEXT PRIMARY KEY, idempotency_key TEXT NOT NULL, owner_user_id TEXT NOT NULL,
    workflow_id TEXT NOT NULL, contract_version TEXT NOT NULL, request_json TEXT NOT NULL,
    response_json TEXT NOT NULL, consumed_at DATETIME NULL, session_id TEXT NOT NULL DEFAULT '',
    created_at DATETIME NOT NULL, updated_at DATETIME NOT NULL,
    UNIQUE(owner_user_id, idempotency_key)
);
CREATE INDEX IF NOT EXISTS idx_workflow_preparations_owner ON workflow_preparations(owner_user_id);
CREATE TABLE IF NOT EXISTS workflow_commands (
    command_id TEXT PRIMARY KEY, owner_user_id TEXT NOT NULL, session_id TEXT NOT NULL,
    contract_version TEXT NOT NULL, request_hash TEXT NOT NULL, http_status INTEGER NOT NULL,
    response_json TEXT NOT NULL, created_at DATETIME NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_workflow_commands_owner ON workflow_commands(owner_user_id);
CREATE INDEX IF NOT EXISTS idx_workflow_commands_session ON workflow_commands(session_id);
CREATE TABLE IF NOT EXISTS workflow_events (
    id INTEGER PRIMARY KEY AUTOINCREMENT, session_id TEXT NOT NULL, owner_user_id TEXT NOT NULL,
    contract_version TEXT NOT NULL, event_type TEXT NOT NULL, entity_id TEXT NOT NULL DEFAULT '',
    state_version INTEGER NOT NULL DEFAULT 0, command_id TEXT NOT NULL DEFAULT '',
    payload_json TEXT NOT NULL, created_at DATETIME NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_workflow_events_session_cursor ON workflow_events(session_id, id);
CREATE INDEX IF NOT EXISTS idx_workflow_events_owner ON workflow_events(owner_user_id);
CREATE INDEX IF NOT EXISTS idx_workflow_events_command ON workflow_events(command_id);

-- +migrate Dialect postgres
ALTER TABLE plugin_session_steps ADD COLUMN IF NOT EXISTS lease_owner VARCHAR(255) NOT NULL DEFAULT '';
ALTER TABLE plugin_session_steps ADD COLUMN IF NOT EXISTS lease_token VARCHAR(255) NOT NULL DEFAULT '';
ALTER TABLE plugin_session_steps ADD COLUMN IF NOT EXISTS fencing_generation BIGINT NOT NULL DEFAULT 0;
ALTER TABLE plugin_session_steps ADD COLUMN IF NOT EXISTS lease_expires_at TIMESTAMP NULL;
ALTER TABLE plugin_session_steps ADD COLUMN IF NOT EXISTS heartbeat_at TIMESTAMP NULL;
ALTER TABLE plugin_session_steps ADD COLUMN IF NOT EXISTS progress_json JSONB NOT NULL DEFAULT '{}';
ALTER TABLE plugin_session_steps ADD COLUMN IF NOT EXISTS terminal_code VARCHAR(64) NOT NULL DEFAULT '';
ALTER TABLE plugin_session_steps ADD COLUMN IF NOT EXISTS result_json JSONB NOT NULL DEFAULT '{}';
CREATE INDEX IF NOT EXISTS idx_plugin_session_steps_claim ON plugin_session_steps(status, lease_expires_at, id);
CREATE TABLE IF NOT EXISTS workflow_outbox (
    id VARCHAR(36) PRIMARY KEY,
    attempt_id VARCHAR(36) NOT NULL UNIQUE,
    session_id VARCHAR(36) NOT NULL,
    payload_json JSONB NOT NULL,
    status VARCHAR(16) NOT NULL DEFAULT 'pending',
    created_at TIMESTAMP NOT NULL,
    updated_at TIMESTAMP NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_workflow_outbox_status ON workflow_outbox(status, created_at);
CREATE INDEX IF NOT EXISTS idx_workflow_outbox_session ON workflow_outbox(session_id);

-- +migrate Dialect sqlite
ALTER TABLE plugin_session_steps ADD COLUMN lease_owner TEXT NOT NULL DEFAULT '';
ALTER TABLE plugin_session_steps ADD COLUMN lease_token TEXT NOT NULL DEFAULT '';
ALTER TABLE plugin_session_steps ADD COLUMN fencing_generation INTEGER NOT NULL DEFAULT 0;
ALTER TABLE plugin_session_steps ADD COLUMN lease_expires_at DATETIME NULL;
ALTER TABLE plugin_session_steps ADD COLUMN heartbeat_at DATETIME NULL;
ALTER TABLE plugin_session_steps ADD COLUMN progress_json TEXT NOT NULL DEFAULT '{}';
ALTER TABLE plugin_session_steps ADD COLUMN terminal_code TEXT NOT NULL DEFAULT '';
ALTER TABLE plugin_session_steps ADD COLUMN result_json TEXT NOT NULL DEFAULT '{}';
CREATE INDEX IF NOT EXISTS idx_plugin_session_steps_claim ON plugin_session_steps(status, lease_expires_at, id);
CREATE TABLE IF NOT EXISTS workflow_outbox (
    id TEXT PRIMARY KEY, attempt_id TEXT NOT NULL UNIQUE, session_id TEXT NOT NULL,
    payload_json TEXT NOT NULL, status TEXT NOT NULL DEFAULT 'pending',
    created_at DATETIME NOT NULL, updated_at DATETIME NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_workflow_outbox_status ON workflow_outbox(status, created_at);
CREATE INDEX IF NOT EXISTS idx_workflow_outbox_session ON workflow_outbox(session_id);

-- +migrate Dialect postgres
CREATE TABLE IF NOT EXISTS workflow_input_resources (
    id varchar(36) PRIMARY KEY,
    owner_user_id varchar(255) NOT NULL,
    name varchar(255) NOT NULL,
    mime_type varchar(255) NOT NULL,
    size bigint NOT NULL,
    content_hash varchar(80) NOT NULL,
    revision bigint NOT NULL DEFAULT 1,
    content bytea NOT NULL,
    created_at timestamp NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_workflow_input_resources_owner_hash
    ON workflow_input_resources(owner_user_id, content_hash);

CREATE TABLE IF NOT EXISTS workflow_input_bindings (
    id varchar(36) PRIMARY KEY,
    workflow_session_id varchar(36) NOT NULL,
    material_id varchar(64) NOT NULL,
    resource_type varchar(32) NOT NULL,
    resource_id varchar(36) NOT NULL,
    resource_revision bigint NOT NULL,
    content_hash varchar(80) NOT NULL,
    validity varchar(16) NOT NULL DEFAULT 'effective',
    created_by_command_id varchar(64) NOT NULL,
    created_at timestamp NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_workflow_input_bindings_session
    ON workflow_input_bindings(workflow_session_id);
CREATE INDEX IF NOT EXISTS idx_workflow_input_bindings_resource
    ON workflow_input_bindings(resource_id);

ALTER TABLE plugin_attempt_input_bindings ADD COLUMN source_type varchar(32) NOT NULL DEFAULT 'artifact';
ALTER TABLE plugin_attempt_input_bindings ADD COLUMN source_id varchar(128) NOT NULL DEFAULT '';
ALTER TABLE plugin_attempt_input_bindings ADD COLUMN source_revision varchar(64) NOT NULL DEFAULT '';
ALTER TABLE plugin_attempt_input_bindings ADD COLUMN content_hash varchar(80) NOT NULL DEFAULT '';

-- +migrate Dialect sqlite
CREATE TABLE IF NOT EXISTS workflow_input_resources (
    id TEXT PRIMARY KEY,
    owner_user_id TEXT NOT NULL,
    name TEXT NOT NULL,
    mime_type TEXT NOT NULL,
    size INTEGER NOT NULL,
    content_hash TEXT NOT NULL,
    revision INTEGER NOT NULL DEFAULT 1,
    content BLOB NOT NULL,
    created_at DATETIME NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_workflow_input_resources_owner_hash
    ON workflow_input_resources(owner_user_id, content_hash);

CREATE TABLE IF NOT EXISTS workflow_input_bindings (
    id TEXT PRIMARY KEY,
    workflow_session_id TEXT NOT NULL,
    material_id TEXT NOT NULL,
    resource_type TEXT NOT NULL,
    resource_id TEXT NOT NULL,
    resource_revision INTEGER NOT NULL,
    content_hash TEXT NOT NULL,
    validity TEXT NOT NULL DEFAULT 'effective',
    created_by_command_id TEXT NOT NULL,
    created_at DATETIME NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_workflow_input_bindings_session
    ON workflow_input_bindings(workflow_session_id);
CREATE INDEX IF NOT EXISTS idx_workflow_input_bindings_resource
    ON workflow_input_bindings(resource_id);

ALTER TABLE plugin_attempt_input_bindings ADD COLUMN source_type TEXT NOT NULL DEFAULT 'artifact';
ALTER TABLE plugin_attempt_input_bindings ADD COLUMN source_id TEXT NOT NULL DEFAULT '';
ALTER TABLE plugin_attempt_input_bindings ADD COLUMN source_revision TEXT NOT NULL DEFAULT '';
ALTER TABLE plugin_attempt_input_bindings ADD COLUMN content_hash TEXT NOT NULL DEFAULT '';

-- +migrate Dialect postgres
ALTER TABLE plugin_drafts ADD COLUMN IF NOT EXISTS driver_content TEXT NOT NULL DEFAULT '';

-- +migrate Dialect sqlite
ALTER TABLE plugin_drafts ADD COLUMN driver_content TEXT NOT NULL DEFAULT '';

-- +migrate Dialect postgres
ALTER TABLE conversations ADD COLUMN IF NOT EXISTS archived_at TIMESTAMP NULL;
ALTER TABLE conversations ADD COLUMN IF NOT EXISTS archive_folder_id VARCHAR(36) NULL;
ALTER TABLE conversations ADD COLUMN IF NOT EXISTS trash_expires_at TIMESTAMP NULL;
ALTER TABLE conversations ADD COLUMN IF NOT EXISTS is_ephemeral BOOLEAN NOT NULL DEFAULT FALSE;
ALTER TABLE conversations ADD COLUMN IF NOT EXISTS ephemeral_expires_at TIMESTAMP NULL;
ALTER TABLE conversations ADD COLUMN IF NOT EXISTS source_type VARCHAR(32) NOT NULL DEFAULT '';
ALTER TABLE conversations ADD COLUMN IF NOT EXISTS source_dataset_id VARCHAR(255) NOT NULL DEFAULT '';
ALTER TABLE conversations ADD COLUMN IF NOT EXISTS source_document_id VARCHAR(255) NOT NULL DEFAULT '';
ALTER TABLE conversations ADD COLUMN IF NOT EXISTS source_display_name VARCHAR(255) NOT NULL DEFAULT '';
ALTER TABLE conversations ADD COLUMN IF NOT EXISTS pinned_at TIMESTAMP NULL;
CREATE TABLE IF NOT EXISTS conversation_archive_folders (
    id VARCHAR(36) PRIMARY KEY,
    user_id VARCHAR(255) NOT NULL,
    name VARCHAR(255) NOT NULL,
    normalized_name VARCHAR(255) NOT NULL,
    created_at TIMESTAMP NOT NULL,
    updated_at TIMESTAMP NOT NULL,
    CONSTRAINT uk_conversation_archive_folders_user_name
        UNIQUE (user_id, normalized_name)
);
CREATE INDEX IF NOT EXISTS idx_conversations_user_lifecycle
    ON conversations(create_user_id, deleted_at, archived_at, is_task_conv, updated_at);
CREATE INDEX IF NOT EXISTS idx_conversations_user_archive_folder
    ON conversations(create_user_id, archive_folder_id, archived_at);
CREATE INDEX IF NOT EXISTS idx_conversations_user_ephemeral_history
    ON conversations(create_user_id, is_ephemeral, deleted_at, archived_at, is_task_conv, updated_at);
CREATE INDEX IF NOT EXISTS idx_conversations_user_pinned_history
    ON conversations(create_user_id, pinned_at DESC, updated_at DESC)
    WHERE deleted_at IS NULL AND archived_at IS NULL;
CREATE INDEX IF NOT EXISTS idx_conversations_user_source
    ON conversations(create_user_id, source_type, source_document_id, is_ephemeral, updated_at);
CREATE INDEX IF NOT EXISTS idx_conversations_ephemeral_expiry
    ON conversations(is_ephemeral, ephemeral_expires_at)
    WHERE is_ephemeral = TRUE;
ALTER TABLE plugin_drafts ADD COLUMN IF NOT EXISTS deleted_at TIMESTAMP NULL;
ALTER TABLE plugin_drafts ADD COLUMN IF NOT EXISTS trash_expires_at TIMESTAMP NULL;
ALTER TABLE plugin_drafts ADD COLUMN IF NOT EXISTS published_status_before_trash VARCHAR(16) NOT NULL DEFAULT '';
CREATE INDEX IF NOT EXISTS idx_plugin_drafts_user_trash
    ON plugin_drafts(created_by, deleted_at, trash_expires_at);
ALTER TABLE skills ADD COLUMN IF NOT EXISTS trash_expires_at TIMESTAMP NULL;
ALTER TABLE task_center_tasks ADD COLUMN IF NOT EXISTS archived_reason VARCHAR(32) NOT NULL DEFAULT '';
UPDATE conversations SET trash_expires_at = CURRENT_TIMESTAMP + INTERVAL '30 days'
    WHERE deleted_at IS NOT NULL AND trash_expires_at IS NULL;
UPDATE skills SET trash_expires_at = CURRENT_TIMESTAMP + INTERVAL '30 days'
    WHERE deleted_at IS NOT NULL AND trash_expires_at IS NULL;
DROP INDEX IF EXISTS idx_plugin_drafts_user_plugin_id;
CREATE UNIQUE INDEX IF NOT EXISTS idx_plugin_drafts_user_plugin_id
    ON plugin_drafts(created_by, plugin_id)
    WHERE plugin_id != '' AND deleted_at IS NULL;

-- +migrate Dialect sqlite
ALTER TABLE conversations ADD COLUMN archived_at DATETIME NULL;
ALTER TABLE conversations ADD COLUMN archive_folder_id VARCHAR(36) NULL;
ALTER TABLE conversations ADD COLUMN trash_expires_at DATETIME NULL;
ALTER TABLE conversations ADD COLUMN is_ephemeral NUMERIC NOT NULL DEFAULT FALSE;
ALTER TABLE conversations ADD COLUMN ephemeral_expires_at DATETIME NULL;
ALTER TABLE conversations ADD COLUMN source_type VARCHAR(32) NOT NULL DEFAULT '';
ALTER TABLE conversations ADD COLUMN source_dataset_id VARCHAR(255) NOT NULL DEFAULT '';
ALTER TABLE conversations ADD COLUMN source_document_id VARCHAR(255) NOT NULL DEFAULT '';
ALTER TABLE conversations ADD COLUMN source_display_name VARCHAR(255) NOT NULL DEFAULT '';
ALTER TABLE conversations ADD COLUMN pinned_at DATETIME NULL;
CREATE TABLE IF NOT EXISTS conversation_archive_folders (
    id VARCHAR(36) PRIMARY KEY,
    user_id VARCHAR(255) NOT NULL,
    name VARCHAR(255) NOT NULL,
    normalized_name VARCHAR(255) NOT NULL,
    created_at DATETIME NOT NULL,
    updated_at DATETIME NOT NULL,
    CONSTRAINT uk_conversation_archive_folders_user_name
        UNIQUE (user_id, normalized_name)
);
CREATE INDEX IF NOT EXISTS idx_conversations_user_lifecycle
    ON conversations(create_user_id, deleted_at, archived_at, is_task_conv, updated_at);
CREATE INDEX IF NOT EXISTS idx_conversations_user_archive_folder
    ON conversations(create_user_id, archive_folder_id, archived_at);
CREATE INDEX IF NOT EXISTS idx_conversations_user_ephemeral_history
    ON conversations(create_user_id, is_ephemeral, deleted_at, archived_at, is_task_conv, updated_at);
CREATE INDEX IF NOT EXISTS idx_conversations_user_pinned_history
    ON conversations(create_user_id, pinned_at DESC, updated_at DESC)
    WHERE deleted_at IS NULL AND archived_at IS NULL;
CREATE INDEX IF NOT EXISTS idx_conversations_user_source
    ON conversations(create_user_id, source_type, source_document_id, is_ephemeral, updated_at);
CREATE INDEX IF NOT EXISTS idx_conversations_ephemeral_expiry
    ON conversations(is_ephemeral, ephemeral_expires_at)
    WHERE is_ephemeral = TRUE;
ALTER TABLE plugin_drafts ADD COLUMN deleted_at DATETIME NULL;
ALTER TABLE plugin_drafts ADD COLUMN trash_expires_at DATETIME NULL;
ALTER TABLE plugin_drafts ADD COLUMN published_status_before_trash VARCHAR(16) NOT NULL DEFAULT '';
CREATE INDEX IF NOT EXISTS idx_plugin_drafts_user_trash
    ON plugin_drafts(created_by, deleted_at, trash_expires_at);
ALTER TABLE skills ADD COLUMN trash_expires_at DATETIME NULL;
ALTER TABLE task_center_tasks ADD COLUMN archived_reason VARCHAR(32) NOT NULL DEFAULT '';
UPDATE conversations SET trash_expires_at = datetime('now', '+30 days')
    WHERE deleted_at IS NOT NULL AND trash_expires_at IS NULL;
UPDATE skills SET trash_expires_at = datetime('now', '+30 days')
    WHERE deleted_at IS NOT NULL AND trash_expires_at IS NULL;
DROP INDEX IF EXISTS idx_plugin_drafts_user_plugin_id;
CREATE UNIQUE INDEX IF NOT EXISTS idx_plugin_drafts_user_plugin_id
    ON plugin_drafts(created_by, plugin_id)
    WHERE plugin_id != '' AND deleted_at IS NULL;

-- +migrate Dialect postgres
ALTER TABLE public.chat_histories
    ADD COLUMN IF NOT EXISTS algorithm_id VARCHAR(64);
CREATE INDEX IF NOT EXISTS idx_chat_histories_algorithm_create_time
    ON public.chat_histories (algorithm_id, create_time);

-- +migrate Dialect sqlite
ALTER TABLE chat_histories ADD COLUMN algorithm_id varchar(64);
CREATE INDEX IF NOT EXISTS idx_chat_histories_algorithm_create_time
    ON chat_histories (algorithm_id, create_time);

-- +migrate Dialect postgres
CREATE TABLE IF NOT EXISTS public.memory_current_entries (
    user_id VARCHAR(255) NOT NULL,
    path VARCHAR(1024) NOT NULL,
    entry_type VARCHAR(16) NOT NULL,
    content BYTEA,
    size BIGINT NOT NULL DEFAULT 0,
    mime VARCHAR(128) NOT NULL DEFAULT '',
    file_type VARCHAR(32) NOT NULL DEFAULT 'unknown',
    "binary" BOOLEAN NOT NULL DEFAULT FALSE,
    created_at TIMESTAMP NOT NULL,
    updated_at TIMESTAMP NOT NULL,
    PRIMARY KEY (user_id, path),
    CONSTRAINT chk_memory_current_entry_type CHECK (entry_type IN ('file', 'dir')),
    CONSTRAINT chk_memory_current_entry_content CHECK (
        (entry_type = 'file' AND content IS NOT NULL)
        OR (entry_type = 'dir' AND content IS NULL)
    )
);

CREATE INDEX IF NOT EXISTS idx_memory_current_entries_user_path
    ON public.memory_current_entries (user_id, path);

CREATE TABLE IF NOT EXISTS public.episode_memories (
    row_id BIGSERIAL PRIMARY KEY,
    id VARCHAR(36) NOT NULL,
    user_id VARCHAR(255) NOT NULL,
    conversation_id VARCHAR(255) NOT NULL,
    source_kind VARCHAR(32) NOT NULL,
    episode_type VARCHAR(16) NOT NULL,
    summary TEXT NOT NULL,
    normalized_summary TEXT NOT NULL,
    search_text TEXT NOT NULL,
    tokenizer_version VARCHAR(64) NOT NULL,
    occurred_at_ms BIGINT NOT NULL,
    recorded_at_ms BIGINT NOT NULL,
    hit_count BIGINT NOT NULL DEFAULT 0,
    search_vector TSVECTOR GENERATED ALWAYS AS (
        setweight(to_tsvector('simple', COALESCE(search_text, '')), 'A')
        || setweight(to_tsvector('simple', COALESCE(summary, '')), 'B')
    ) STORED,
    CONSTRAINT uk_episode_memories_user_id UNIQUE (user_id, id),
    CONSTRAINT uk_episode_memories_identity UNIQUE (user_id, conversation_id, normalized_summary),
    CONSTRAINT chk_episode_memories_source_kind CHECK (source_kind IN ('chat_explicit', 'memory_review')),
    CONSTRAINT chk_episode_memories_episode_type CHECK (episode_type IN ('decision', 'progress', 'result', 'blocker', 'event')),
    CONSTRAINT chk_episode_memories_summary CHECK (length(btrim(summary)) BETWEEN 1 AND 200),
    CONSTRAINT chk_episode_memories_search_text CHECK (length(btrim(search_text)) > 0),
    CONSTRAINT chk_episode_memories_tokenizer_version CHECK (length(btrim(tokenizer_version)) > 0),
    CONSTRAINT chk_episode_memories_timestamps CHECK (occurred_at_ms > 0 AND recorded_at_ms > 0),
    CONSTRAINT chk_episode_memories_hit_count CHECK (hit_count >= 0)
);

CREATE INDEX IF NOT EXISTS idx_episode_memories_user_recorded
    ON public.episode_memories (user_id, recorded_at_ms DESC, id DESC);
CREATE INDEX IF NOT EXISTS idx_episode_memories_user_conversation_recorded
    ON public.episode_memories (user_id, conversation_id, recorded_at_ms ASC, id ASC);
CREATE INDEX IF NOT EXISTS idx_episode_memories_search_vector
    ON public.episode_memories USING GIN (search_vector);

CREATE TABLE IF NOT EXISTS external_agent_bindings (
    id VARCHAR(36) PRIMARY KEY,
    conversation_id VARCHAR(36) NOT NULL,
    provider VARCHAR(32) NOT NULL,
    host_id VARCHAR(128) NOT NULL DEFAULT 'host-legacy',
    provider_thread_id VARCHAR(128) NOT NULL,
    created_by_user_id VARCHAR(255) NOT NULL,
    created_at TIMESTAMP NOT NULL,
    updated_at TIMESTAMP NOT NULL
);
CREATE UNIQUE INDEX IF NOT EXISTS uk_external_agent_binding_conversation
    ON external_agent_bindings(conversation_id);
CREATE UNIQUE INDEX IF NOT EXISTS uk_external_agent_binding_thread
    ON external_agent_bindings(provider, host_id, provider_thread_id);

CREATE TABLE IF NOT EXISTS external_agent_sessions (
    id VARCHAR(36) PRIMARY KEY,
    owner_user_id VARCHAR(255) NOT NULL,
    provider VARCHAR(32) NOT NULL,
    host_id VARCHAR(128) NOT NULL DEFAULT 'host-legacy',
    provider_thread_id VARCHAR(128) NOT NULL,
    project_key VARCHAR(128) NOT NULL DEFAULT '',
    project_name VARCHAR(200) NOT NULL DEFAULT '',
    display_name VARCHAR(255) NOT NULL DEFAULT '',
    turn_count INTEGER NOT NULL DEFAULT 0,
    active BOOLEAN NOT NULL DEFAULT TRUE,
    native_updated_at TIMESTAMP,
    last_seen_at TIMESTAMP NOT NULL,
    created_at TIMESTAMP NOT NULL,
    updated_at TIMESTAMP NOT NULL
);
CREATE UNIQUE INDEX IF NOT EXISTS uk_external_agent_session
    ON external_agent_sessions(owner_user_id, provider, host_id, provider_thread_id);
CREATE INDEX IF NOT EXISTS idx_external_agent_session_catalog
    ON external_agent_sessions(owner_user_id, provider, host_id, active, native_updated_at DESC);
CREATE INDEX IF NOT EXISTS idx_external_agent_session_last_seen ON external_agent_sessions(last_seen_at);

CREATE TABLE IF NOT EXISTS external_agent_runs (
    id VARCHAR(36) PRIMARY KEY,
    request_id VARCHAR(255) NOT NULL,
    conversation_id VARCHAR(36) NOT NULL,
    history_id VARCHAR(36) NOT NULL,
    provider VARCHAR(32) NOT NULL,
    provider_thread_id VARCHAR(128) NOT NULL,
    provider_turn_id VARCHAR(128),
    actor_user_id VARCHAR(255) NOT NULL,
    action VARCHAR(32) NOT NULL DEFAULT 'start',
    status VARCHAR(32) NOT NULL,
    error_message TEXT,
    control_release VARCHAR(32) NOT NULL DEFAULT '',
    control_error TEXT,
	    prompt TEXT NOT NULL DEFAULT '',
	    query TEXT NOT NULL DEFAULT '',
	    sequence INTEGER NOT NULL DEFAULT 0,
	    history_ext JSONB,
	    host_id VARCHAR(128) NOT NULL DEFAULT '',
	    lease_token VARCHAR(64) NOT NULL DEFAULT '',
	    lease_expires_at TIMESTAMP,
	    claimed_at TIMESTAMP,
	    last_heartbeat_at TIMESTAMP,
	    stop_requested BOOLEAN NOT NULL DEFAULT FALSE,
	    claim_count INTEGER NOT NULL DEFAULT 0,
	    next_event_sequence BIGINT NOT NULL DEFAULT 0,
	    completed_at TIMESTAMP,
    created_at TIMESTAMP NOT NULL,
    updated_at TIMESTAMP NOT NULL,
    CONSTRAINT uk_external_agent_run_request UNIQUE (provider, request_id)
);

CREATE INDEX IF NOT EXISTS idx_external_agent_runs_conversation_id ON external_agent_runs (conversation_id);
CREATE INDEX IF NOT EXISTS idx_external_agent_runs_provider_thread_id ON external_agent_runs (provider_thread_id);
CREATE INDEX IF NOT EXISTS idx_external_agent_runs_status ON external_agent_runs (status);
CREATE INDEX IF NOT EXISTS idx_external_agent_runs_history_id ON external_agent_runs (history_id);
CREATE INDEX IF NOT EXISTS idx_external_agent_runs_actor_user_id ON external_agent_runs (actor_user_id);
CREATE INDEX IF NOT EXISTS idx_external_agent_runs_claim
    ON external_agent_runs (actor_user_id, provider, status, lease_expires_at, created_at);

CREATE TABLE IF NOT EXISTS external_chat_run_events (
    id VARCHAR(64) PRIMARY KEY,
    run_id VARCHAR(36) NOT NULL,
    sequence BIGINT NOT NULL,
    type VARCHAR(32) NOT NULL,
    text TEXT,
    provider_thread_id VARCHAR(128) NOT NULL DEFAULT '',
    error_message TEXT,
    created_at TIMESTAMP NOT NULL,
    CONSTRAINT uk_external_chat_run_event_sequence UNIQUE (run_id, sequence)
);
CREATE INDEX IF NOT EXISTS idx_external_chat_run_events_run_id ON external_chat_run_events (run_id);

CREATE TABLE IF NOT EXISTS external_chat_hosts (
    actor_user_id VARCHAR(255) NOT NULL,
    provider VARCHAR(32) NOT NULL,
    host_id VARCHAR(128) NOT NULL,
    installed BOOLEAN NOT NULL,
    ready BOOLEAN NOT NULL,
    unavailable_reason VARCHAR(512) NOT NULL DEFAULT '',
    last_seen TIMESTAMP NOT NULL,
    updated_at TIMESTAMP NOT NULL,
    PRIMARY KEY (actor_user_id, provider, host_id)
);
CREATE INDEX IF NOT EXISTS idx_external_chat_hosts_last_seen ON external_chat_hosts (last_seen);

CREATE TABLE IF NOT EXISTS external_agent_operations (
    id VARCHAR(36) PRIMARY KEY,
    actor_user_id VARCHAR(255) NOT NULL,
    operation_id VARCHAR(255) NOT NULL,
    kind VARCHAR(64) NOT NULL,
    request_hash VARCHAR(64) NOT NULL,
    status VARCHAR(32) NOT NULL,
    result JSONB,
    created_at TIMESTAMP NOT NULL,
    updated_at TIMESTAMP NOT NULL,
    CONSTRAINT uk_external_agent_operation UNIQUE
        (actor_user_id, operation_id, kind)
);

ALTER TABLE plugin_transition_commands
    ADD COLUMN IF NOT EXISTS retry_origin VARCHAR(16) NOT NULL DEFAULT 'automatic';

DELETE FROM sub_agent_artifacts
WHERE task_id IN (
    SELECT task.id FROM sub_agent_tasks AS task
    JOIN plugin_session_steps AS attempt ON attempt.task_id = task.id
    JOIN plugin_sessions AS session ON session.id = attempt.session_id
    WHERE task.agent_type = 'workflow_step' AND session.controller_host = 'external-agent'
);
DELETE FROM sub_agent_steps
WHERE task_id IN (
    SELECT task.id FROM sub_agent_tasks AS task
    JOIN plugin_session_steps AS attempt ON attempt.task_id = task.id
    JOIN plugin_sessions AS session ON session.id = attempt.session_id
    WHERE task.agent_type = 'workflow_step' AND session.controller_host = 'external-agent'
);
DELETE FROM sub_agent_tasks
WHERE id IN (
    SELECT attempt.task_id FROM plugin_session_steps AS attempt
    JOIN plugin_sessions AS session ON session.id = attempt.session_id
    WHERE session.controller_host = 'external-agent'
);

-- +migrate Dialect postgres
CREATE TABLE IF NOT EXISTS writer_download_conversions (
    id VARCHAR(36) PRIMARY KEY,
    user_id VARCHAR(255) NOT NULL,
    source_hash VARCHAR(64) NOT NULL,
    target_format VARCHAR(16) NOT NULL,
    filename VARCHAR(255) NOT NULL,
    mime_type VARCHAR(128) NOT NULL,
    storage_path VARCHAR(1024) NOT NULL,
    size BIGINT NOT NULL,
    content_hash VARCHAR(64) NOT NULL,
    created_at TIMESTAMP NOT NULL,
    updated_at TIMESTAMP NOT NULL,
    CONSTRAINT uk_writer_download_conversion UNIQUE (user_id, source_hash, target_format)
);
CREATE INDEX IF NOT EXISTS idx_writer_download_conversions_user_updated
    ON writer_download_conversions(user_id, updated_at);

-- +migrate Dialect sqlite
CREATE TABLE IF NOT EXISTS writer_download_conversions (
    id TEXT PRIMARY KEY,
    user_id TEXT NOT NULL,
    source_hash TEXT NOT NULL,
    target_format TEXT NOT NULL,
    filename TEXT NOT NULL,
    mime_type TEXT NOT NULL,
    storage_path TEXT NOT NULL,
    size INTEGER NOT NULL,
    content_hash TEXT NOT NULL,
    created_at DATETIME NOT NULL,
    updated_at DATETIME NOT NULL,
    UNIQUE(user_id, source_hash, target_format)
);
CREATE INDEX IF NOT EXISTS idx_writer_download_conversions_user_updated
    ON writer_download_conversions(user_id, updated_at);

-- +migrate Dialect postgres
CREATE TABLE IF NOT EXISTS agent_invocations (
    id VARCHAR(80) PRIMARY KEY,
    owner_user_id VARCHAR(255) NOT NULL,
    client_name VARCHAR(128) NOT NULL DEFAULT '',
    client_version VARCHAR(128) NOT NULL DEFAULT '',
    connector_name VARCHAR(128) NOT NULL DEFAULT '',
    connector_version VARCHAR(64) NOT NULL DEFAULT '',
    connector_instance_id VARCHAR(80) NOT NULL DEFAULT '',
    protocol_version VARCHAR(64) NOT NULL DEFAULT '',
    transport VARCHAR(32) NOT NULL DEFAULT 'stdio',
    tool_name VARCHAR(128) NOT NULL,
    read_only BOOLEAN NOT NULL DEFAULT FALSE,
    status VARCHAR(32) NOT NULL,
    request_hash VARCHAR(64) NOT NULL,
    request_summary JSONB NOT NULL DEFAULT '{}',
    result_summary JSONB NOT NULL DEFAULT '{}',
    error_code VARCHAR(128) NOT NULL DEFAULT '',
    retryable BOOLEAN NOT NULL DEFAULT FALSE,
    workflow_id VARCHAR(255) NOT NULL DEFAULT '',
    session_id VARCHAR(80) NOT NULL DEFAULT '',
    step_id VARCHAR(128) NOT NULL DEFAULT '',
    attempt_id VARCHAR(80) NOT NULL DEFAULT '',
    resource_id VARCHAR(128) NOT NULL DEFAULT '',
    artifact_id VARCHAR(80) NOT NULL DEFAULT '',
    command_id VARCHAR(128) NOT NULL DEFAULT '',
    external_ref VARCHAR(255) NOT NULL DEFAULT '',
    started_at TIMESTAMP NOT NULL,
    finished_at TIMESTAMP,
    created_at TIMESTAMP NOT NULL,
    updated_at TIMESTAMP NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_agent_invocations_owner_started ON agent_invocations(owner_user_id, started_at DESC);
CREATE INDEX IF NOT EXISTS idx_agent_invocations_client_name ON agent_invocations(client_name);
CREATE INDEX IF NOT EXISTS idx_agent_invocations_connector_instance_id ON agent_invocations(connector_instance_id);
CREATE INDEX IF NOT EXISTS idx_agent_invocations_tool_name ON agent_invocations(tool_name);
CREATE INDEX IF NOT EXISTS idx_agent_invocations_status ON agent_invocations(status);
CREATE INDEX IF NOT EXISTS idx_agent_invocations_workflow_id ON agent_invocations(workflow_id);
CREATE INDEX IF NOT EXISTS idx_agent_invocations_session_id ON agent_invocations(session_id);
CREATE INDEX IF NOT EXISTS idx_agent_invocations_attempt_id ON agent_invocations(attempt_id);
CREATE INDEX IF NOT EXISTS idx_chat_histories_conversation_seq
    ON chat_histories (conversation_id, seq DESC);

-- +migrate Dialect sqlite
CREATE TABLE IF NOT EXISTS agent_invocations (
    id VARCHAR(80) PRIMARY KEY,
    owner_user_id VARCHAR(255) NOT NULL,
    client_name VARCHAR(128) NOT NULL DEFAULT '',
    client_version VARCHAR(128) NOT NULL DEFAULT '',
    connector_name VARCHAR(128) NOT NULL DEFAULT '',
    connector_version VARCHAR(64) NOT NULL DEFAULT '',
    connector_instance_id VARCHAR(80) NOT NULL DEFAULT '',
    protocol_version VARCHAR(64) NOT NULL DEFAULT '',
    transport VARCHAR(32) NOT NULL DEFAULT 'stdio',
    tool_name VARCHAR(128) NOT NULL,
    read_only BOOLEAN NOT NULL DEFAULT FALSE,
    status VARCHAR(32) NOT NULL,
    request_hash VARCHAR(64) NOT NULL,
    request_summary TEXT NOT NULL DEFAULT '{}',
    result_summary TEXT NOT NULL DEFAULT '{}',
    error_code VARCHAR(128) NOT NULL DEFAULT '',
    retryable BOOLEAN NOT NULL DEFAULT FALSE,
    workflow_id VARCHAR(255) NOT NULL DEFAULT '',
    session_id VARCHAR(80) NOT NULL DEFAULT '',
    step_id VARCHAR(128) NOT NULL DEFAULT '',
    attempt_id VARCHAR(80) NOT NULL DEFAULT '',
    resource_id VARCHAR(128) NOT NULL DEFAULT '',
    artifact_id VARCHAR(80) NOT NULL DEFAULT '',
    command_id VARCHAR(128) NOT NULL DEFAULT '',
    external_ref VARCHAR(255) NOT NULL DEFAULT '',
    started_at DATETIME NOT NULL,
    finished_at DATETIME,
    created_at DATETIME NOT NULL,
    updated_at DATETIME NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_agent_invocations_owner_started ON agent_invocations(owner_user_id, started_at DESC);
CREATE INDEX IF NOT EXISTS idx_agent_invocations_client_name ON agent_invocations(client_name);
CREATE INDEX IF NOT EXISTS idx_agent_invocations_connector_instance_id ON agent_invocations(connector_instance_id);
CREATE INDEX IF NOT EXISTS idx_agent_invocations_tool_name ON agent_invocations(tool_name);
CREATE INDEX IF NOT EXISTS idx_agent_invocations_status ON agent_invocations(status);
CREATE INDEX IF NOT EXISTS idx_agent_invocations_workflow_id ON agent_invocations(workflow_id);
CREATE INDEX IF NOT EXISTS idx_agent_invocations_session_id ON agent_invocations(session_id);
CREATE INDEX IF NOT EXISTS idx_agent_invocations_attempt_id ON agent_invocations(attempt_id);
CREATE INDEX IF NOT EXISTS idx_chat_histories_conversation_seq
    ON chat_histories (conversation_id, seq DESC);

-- +migrate Dialect sqlite
CREATE TABLE IF NOT EXISTS memory_current_entries (
    user_id varchar(255) NOT NULL,
    path varchar(1024) NOT NULL,
    entry_type varchar(16) NOT NULL,
    content BLOB,
    size integer NOT NULL DEFAULT 0,
    mime varchar(128) NOT NULL DEFAULT '',
    file_type varchar(32) NOT NULL DEFAULT 'unknown',
    "binary" boolean NOT NULL DEFAULT false,
    created_at datetime NOT NULL,
    updated_at datetime NOT NULL,
    PRIMARY KEY (user_id, path),
    CONSTRAINT chk_memory_current_entry_type CHECK (entry_type IN ('file', 'dir')),
    CONSTRAINT chk_memory_current_entry_content CHECK (
        (entry_type = 'file' AND content IS NOT NULL)
        OR (entry_type = 'dir' AND content IS NULL)
    )
);

CREATE INDEX IF NOT EXISTS idx_memory_current_entries_user_path
    ON memory_current_entries (user_id, path);

CREATE TABLE IF NOT EXISTS episode_memories (
    row_id INTEGER PRIMARY KEY AUTOINCREMENT,
    id varchar(36) NOT NULL,
    user_id varchar(255) NOT NULL,
    conversation_id varchar(255) NOT NULL,
    source_kind varchar(32) NOT NULL,
    episode_type varchar(16) NOT NULL,
    summary text NOT NULL,
    normalized_summary text NOT NULL,
    search_text text NOT NULL,
    tokenizer_version varchar(64) NOT NULL,
    occurred_at_ms integer NOT NULL,
    recorded_at_ms integer NOT NULL,
    hit_count integer NOT NULL DEFAULT 0,
    CONSTRAINT uk_episode_memories_user_id UNIQUE (user_id, id),
    CONSTRAINT uk_episode_memories_identity UNIQUE (user_id, conversation_id, normalized_summary),
    CONSTRAINT chk_episode_memories_source_kind CHECK (source_kind IN ('chat_explicit', 'memory_review')),
    CONSTRAINT chk_episode_memories_episode_type CHECK (episode_type IN ('decision', 'progress', 'result', 'blocker', 'event')),
    CONSTRAINT chk_episode_memories_summary CHECK (length(trim(summary)) BETWEEN 1 AND 200),
    CONSTRAINT chk_episode_memories_search_text CHECK (length(trim(search_text)) > 0),
    CONSTRAINT chk_episode_memories_tokenizer_version CHECK (length(trim(tokenizer_version)) > 0),
    CONSTRAINT chk_episode_memories_timestamps CHECK (occurred_at_ms > 0 AND recorded_at_ms > 0),
    CONSTRAINT chk_episode_memories_hit_count CHECK (hit_count >= 0)
);

CREATE INDEX IF NOT EXISTS idx_episode_memories_user_recorded
    ON episode_memories (user_id, recorded_at_ms DESC, id DESC);
CREATE INDEX IF NOT EXISTS idx_episode_memories_user_conversation_recorded
    ON episode_memories (user_id, conversation_id, recorded_at_ms ASC, id ASC);

CREATE TABLE IF NOT EXISTS external_agent_bindings (
    id VARCHAR(36) PRIMARY KEY,
    conversation_id VARCHAR(36) NOT NULL,
    provider VARCHAR(32) NOT NULL,
    host_id VARCHAR(128) NOT NULL DEFAULT 'host-legacy',
    provider_thread_id VARCHAR(128) NOT NULL,
    created_by_user_id VARCHAR(255) NOT NULL,
    created_at TIMESTAMP NOT NULL,
    updated_at TIMESTAMP NOT NULL
);
CREATE UNIQUE INDEX IF NOT EXISTS uk_external_agent_binding_conversation
    ON external_agent_bindings(conversation_id);
CREATE UNIQUE INDEX IF NOT EXISTS uk_external_agent_binding_thread
    ON external_agent_bindings(provider, host_id, provider_thread_id);

CREATE TABLE IF NOT EXISTS external_agent_sessions (
    id VARCHAR(36) PRIMARY KEY,
    owner_user_id VARCHAR(255) NOT NULL,
    provider VARCHAR(32) NOT NULL,
    host_id VARCHAR(128) NOT NULL DEFAULT 'host-legacy',
    provider_thread_id VARCHAR(128) NOT NULL,
    project_key VARCHAR(128) NOT NULL DEFAULT '',
    project_name VARCHAR(200) NOT NULL DEFAULT '',
    display_name VARCHAR(255) NOT NULL DEFAULT '',
    turn_count INTEGER NOT NULL DEFAULT 0,
    active BOOLEAN NOT NULL DEFAULT TRUE,
    native_updated_at DATETIME,
    last_seen_at DATETIME NOT NULL,
    created_at DATETIME NOT NULL,
    updated_at DATETIME NOT NULL
);
CREATE UNIQUE INDEX IF NOT EXISTS uk_external_agent_session
    ON external_agent_sessions(owner_user_id, provider, host_id, provider_thread_id);
CREATE INDEX IF NOT EXISTS idx_external_agent_session_catalog
    ON external_agent_sessions(owner_user_id, provider, host_id, active, native_updated_at DESC);
CREATE INDEX IF NOT EXISTS idx_external_agent_session_last_seen ON external_agent_sessions(last_seen_at);

CREATE TABLE IF NOT EXISTS external_agent_runs (
    id VARCHAR(36) PRIMARY KEY,
    request_id VARCHAR(255) NOT NULL,
    conversation_id VARCHAR(36) NOT NULL,
    history_id VARCHAR(36) NOT NULL,
    provider VARCHAR(32) NOT NULL,
    provider_thread_id VARCHAR(128) NOT NULL,
    provider_turn_id VARCHAR(128),
    actor_user_id VARCHAR(255) NOT NULL,
    action VARCHAR(32) NOT NULL DEFAULT 'start',
    status VARCHAR(32) NOT NULL,
    error_message TEXT,
    control_release VARCHAR(32) NOT NULL DEFAULT '',
    control_error TEXT,
	    prompt TEXT NOT NULL DEFAULT '',
	    query TEXT NOT NULL DEFAULT '',
	    sequence INTEGER NOT NULL DEFAULT 0,
	    history_ext TEXT,
	    host_id VARCHAR(128) NOT NULL DEFAULT '',
	    lease_token VARCHAR(64) NOT NULL DEFAULT '',
	    lease_expires_at DATETIME,
	    claimed_at DATETIME,
	    last_heartbeat_at DATETIME,
	    stop_requested BOOLEAN NOT NULL DEFAULT FALSE,
	    claim_count INTEGER NOT NULL DEFAULT 0,
	    next_event_sequence INTEGER NOT NULL DEFAULT 0,
	    completed_at DATETIME,
    created_at TIMESTAMP NOT NULL,
    updated_at TIMESTAMP NOT NULL,
    CONSTRAINT uk_external_agent_run_request UNIQUE (provider, request_id)
);

CREATE INDEX IF NOT EXISTS idx_external_agent_runs_conversation_id ON external_agent_runs (conversation_id);
CREATE INDEX IF NOT EXISTS idx_external_agent_runs_provider_thread_id ON external_agent_runs (provider_thread_id);
CREATE INDEX IF NOT EXISTS idx_external_agent_runs_status ON external_agent_runs (status);
CREATE INDEX IF NOT EXISTS idx_external_agent_runs_history_id ON external_agent_runs (history_id);
CREATE INDEX IF NOT EXISTS idx_external_agent_runs_actor_user_id ON external_agent_runs (actor_user_id);
CREATE INDEX IF NOT EXISTS idx_external_agent_runs_claim
    ON external_agent_runs (actor_user_id, provider, status, lease_expires_at, created_at);

CREATE TABLE IF NOT EXISTS external_chat_run_events (
    id VARCHAR(64) PRIMARY KEY,
    run_id VARCHAR(36) NOT NULL,
    sequence INTEGER NOT NULL,
    type VARCHAR(32) NOT NULL,
    text TEXT,
    provider_thread_id VARCHAR(128) NOT NULL DEFAULT '',
    error_message TEXT,
    created_at DATETIME NOT NULL,
    CONSTRAINT uk_external_chat_run_event_sequence UNIQUE (run_id, sequence)
);
CREATE INDEX IF NOT EXISTS idx_external_chat_run_events_run_id ON external_chat_run_events (run_id);

CREATE TABLE IF NOT EXISTS external_chat_hosts (
    actor_user_id VARCHAR(255) NOT NULL,
    provider VARCHAR(32) NOT NULL,
    host_id VARCHAR(128) NOT NULL,
    installed BOOLEAN NOT NULL,
    ready BOOLEAN NOT NULL,
    unavailable_reason VARCHAR(512) NOT NULL DEFAULT '',
    last_seen DATETIME NOT NULL,
    updated_at DATETIME NOT NULL,
    PRIMARY KEY (actor_user_id, provider, host_id)
);
CREATE INDEX IF NOT EXISTS idx_external_chat_hosts_last_seen ON external_chat_hosts (last_seen);

CREATE TABLE IF NOT EXISTS external_agent_operations (
    id VARCHAR(36) PRIMARY KEY,
    actor_user_id VARCHAR(255) NOT NULL,
    operation_id VARCHAR(255) NOT NULL,
    kind VARCHAR(64) NOT NULL,
    request_hash VARCHAR(64) NOT NULL,
    status VARCHAR(32) NOT NULL,
    result TEXT,
    created_at TIMESTAMP NOT NULL,
    updated_at TIMESTAMP NOT NULL,
    CONSTRAINT uk_external_agent_operation UNIQUE
        (actor_user_id, operation_id, kind)
);

ALTER TABLE plugin_transition_commands
    ADD COLUMN retry_origin VARCHAR(16) NOT NULL DEFAULT 'automatic';

DELETE FROM sub_agent_artifacts
WHERE task_id IN (
    SELECT task.id FROM sub_agent_tasks AS task
    JOIN plugin_session_steps AS attempt ON attempt.task_id = task.id
    JOIN plugin_sessions AS session ON session.id = attempt.session_id
    WHERE task.agent_type = 'workflow_step' AND session.controller_host = 'external-agent'
);
DELETE FROM sub_agent_steps
WHERE task_id IN (
    SELECT task.id FROM sub_agent_tasks AS task
    JOIN plugin_session_steps AS attempt ON attempt.task_id = task.id
    JOIN plugin_sessions AS session ON session.id = attempt.session_id
    WHERE task.agent_type = 'workflow_step' AND session.controller_host = 'external-agent'
);
DELETE FROM sub_agent_tasks
WHERE id IN (
    SELECT attempt.task_id FROM plugin_session_steps AS attempt
    JOIN plugin_sessions AS session ON session.id = attempt.session_id
    WHERE session.controller_host = 'external-agent'
);

-- +migrate Dialect postgres
CREATE TABLE IF NOT EXISTS public.knowledge_market_items (
    id VARCHAR(64) PRIMARY KEY,
    category VARCHAR(32) NOT NULL,
    name VARCHAR(255) NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    icon TEXT NOT NULL DEFAULT '',
    domain VARCHAR(64) NOT NULL DEFAULT '',
    tags JSONB NOT NULL DEFAULT '[]'::jsonb,
    version VARCHAR(32) NOT NULL DEFAULT '',
    version_date VARCHAR(10) NOT NULL DEFAULT '',
    version_note TEXT NOT NULL DEFAULT '',
    package_url TEXT NOT NULL DEFAULT '',
    package_revision VARCHAR(64) NOT NULL DEFAULT '',
    online_access_url VARCHAR(1024) NOT NULL DEFAULT '',
    data_source TEXT NOT NULL DEFAULT '',
    source_adapter VARCHAR(64) NOT NULL DEFAULT '',
    source_options JSONB NOT NULL DEFAULT '{}'::jsonb,
    sample_questions JSONB NOT NULL DEFAULT '[]'::jsonb,
    status VARCHAR(32) NOT NULL DEFAULT 'published',
    sort_order INTEGER NOT NULL DEFAULT 0,
    created_at TIMESTAMP WITHOUT TIME ZONE NOT NULL,
    updated_at TIMESTAMP WITHOUT TIME ZONE NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_knowledge_market_items_category_status
    ON public.knowledge_market_items(status, category, sort_order);

CREATE TABLE IF NOT EXISTS public.knowledge_market_installs (
    market_item_id VARCHAR(64) NOT NULL,
    user_id VARCHAR(255) NOT NULL,
    installed_version VARCHAR(32) NOT NULL DEFAULT '',
    dataset_id VARCHAR(64) NOT NULL DEFAULT '',
    install_state VARCHAR(32) NOT NULL DEFAULT 'pending',
    installed_at TIMESTAMP WITHOUT TIME ZONE NULL,
    config JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at TIMESTAMP WITHOUT TIME ZONE NOT NULL,
    updated_at TIMESTAMP WITHOUT TIME ZONE NOT NULL,
    PRIMARY KEY (market_item_id, user_id)
);

CREATE INDEX IF NOT EXISTS idx_knowledge_market_installs_user
    ON public.knowledge_market_installs(user_id, market_item_id);

-- +migrate Dialect sqlite
CREATE TABLE IF NOT EXISTS `knowledge_market_items` (
    `id` varchar(64) NOT NULL,
    `category` varchar(32) NOT NULL,
    `name` varchar(255) NOT NULL,
    `description` text NOT NULL DEFAULT "",
    `icon` text NOT NULL DEFAULT "",
    `domain` varchar(64) NOT NULL DEFAULT "",
    `tags` json NOT NULL DEFAULT '[]',
    `version` varchar(32) NOT NULL DEFAULT "",
    `version_date` varchar(10) NOT NULL DEFAULT "",
    `version_note` text NOT NULL DEFAULT "",
    `package_url` text NOT NULL DEFAULT "",
    `package_revision` varchar(64) NOT NULL DEFAULT "",
    `online_access_url` varchar(1024) NOT NULL DEFAULT "",
    `data_source` text NOT NULL DEFAULT "",
    `source_adapter` varchar(64) NOT NULL DEFAULT "",
    `source_options` json NOT NULL DEFAULT '{}',
    `sample_questions` json NOT NULL DEFAULT '[]',
    `status` varchar(32) NOT NULL DEFAULT "published",
    `sort_order` integer NOT NULL DEFAULT 0,
    `created_at` datetime NOT NULL,
    `updated_at` datetime NOT NULL,
    PRIMARY KEY (`id`)
);

CREATE INDEX IF NOT EXISTS `idx_knowledge_market_items_category_status`
    ON `knowledge_market_items`(`status`, `category`, `sort_order`);

CREATE TABLE IF NOT EXISTS `knowledge_market_installs` (
    `market_item_id` varchar(64) NOT NULL,
    `user_id` varchar(255) NOT NULL,
    `installed_version` varchar(32) NOT NULL DEFAULT "",
    `dataset_id` varchar(64) NOT NULL DEFAULT "",
    `install_state` varchar(32) NOT NULL DEFAULT "pending",
    `installed_at` datetime NULL,
    `config` json NOT NULL DEFAULT '{}',
    `created_at` datetime NOT NULL,
    `updated_at` datetime NOT NULL,
    PRIMARY KEY (`market_item_id`, `user_id`)
);

CREATE INDEX IF NOT EXISTS `idx_knowledge_market_installs_user`
    ON `knowledge_market_installs`(`user_id`, `market_item_id`);

-- +migrate Dialect postgres
CREATE TABLE IF NOT EXISTS public.skill_distribution_artifacts (archive_sha256 VARCHAR(64) PRIMARY KEY,builtin_skill_uid VARCHAR(64) NOT NULL,version VARCHAR(64) NOT NULL,tree_sha256 VARCHAR(64) NOT NULL,created_at TIMESTAMP WITHOUT TIME ZONE NOT NULL);
CREATE INDEX IF NOT EXISTS idx_skill_distribution_artifacts_uid_version ON public.skill_distribution_artifacts(builtin_skill_uid, version);
CREATE TABLE IF NOT EXISTS public.skill_distribution_entries (archive_sha256 VARCHAR(64) NOT NULL,path VARCHAR(1024) NOT NULL,entry_type VARCHAR(16) NOT NULL,blob_hash VARCHAR(64),size BIGINT NOT NULL DEFAULT 0,mime VARCHAR(128) NOT NULL DEFAULT '',file_type VARCHAR(32) NOT NULL DEFAULT 'unknown',"binary" BOOLEAN NOT NULL DEFAULT FALSE,mode INTEGER NOT NULL DEFAULT 420,PRIMARY KEY (archive_sha256, path));
CREATE INDEX IF NOT EXISTS idx_skill_distribution_entries_blob ON public.skill_distribution_entries(blob_hash);
CREATE TABLE IF NOT EXISTS public.skill_distribution_bindings (skill_id VARCHAR(36) PRIMARY KEY,builtin_skill_uid VARCHAR(64) NOT NULL,current_archive_sha256 VARCHAR(64) NOT NULL,pending_archive_sha256 VARCHAR(64) NOT NULL DEFAULT '',conflicts JSONB NOT NULL DEFAULT '[]'::jsonb,created_at TIMESTAMP WITHOUT TIME ZONE NOT NULL,updated_at TIMESTAMP WITHOUT TIME ZONE NOT NULL);
CREATE INDEX IF NOT EXISTS idx_skill_distribution_bindings_uid ON public.skill_distribution_bindings(builtin_skill_uid);
CREATE TABLE IF NOT EXISTS public.skill_revision_distributions (revision_id VARCHAR(36) PRIMARY KEY,archive_sha256 VARCHAR(64) NOT NULL,created_at TIMESTAMP WITHOUT TIME ZONE NOT NULL);
CREATE INDEX IF NOT EXISTS idx_skill_revision_distributions_archive ON public.skill_revision_distributions(archive_sha256);

-- +migrate Dialect sqlite
CREATE TABLE IF NOT EXISTS `skill_distribution_artifacts` (`archive_sha256` varchar(64),`builtin_skill_uid` varchar(64) NOT NULL,`version` varchar(64) NOT NULL,`tree_sha256` varchar(64) NOT NULL,`created_at` datetime NOT NULL,PRIMARY KEY (`archive_sha256`));
CREATE INDEX IF NOT EXISTS `idx_skill_distribution_artifacts_uid_version` ON `skill_distribution_artifacts`(`builtin_skill_uid`,`version`);
CREATE TABLE IF NOT EXISTS `skill_distribution_entries` (`archive_sha256` varchar(64) NOT NULL,`path` varchar(1024) NOT NULL,`entry_type` varchar(16) NOT NULL,`blob_hash` varchar(64),`size` integer NOT NULL DEFAULT 0,`mime` varchar(128) NOT NULL DEFAULT "",`file_type` varchar(32) NOT NULL DEFAULT "unknown",`binary` numeric NOT NULL DEFAULT false,`mode` integer NOT NULL DEFAULT 420,PRIMARY KEY (`archive_sha256`,`path`));
CREATE INDEX IF NOT EXISTS `idx_skill_distribution_entries_blob` ON `skill_distribution_entries`(`blob_hash`);
CREATE TABLE IF NOT EXISTS `skill_distribution_bindings` (`skill_id` varchar(36),`builtin_skill_uid` varchar(64) NOT NULL,`current_archive_sha256` varchar(64) NOT NULL,`pending_archive_sha256` varchar(64) NOT NULL DEFAULT "",`conflicts` json NOT NULL DEFAULT '[]',`created_at` datetime NOT NULL,`updated_at` datetime NOT NULL,PRIMARY KEY (`skill_id`));
CREATE INDEX IF NOT EXISTS `idx_skill_distribution_bindings_uid` ON `skill_distribution_bindings`(`builtin_skill_uid`);
CREATE TABLE IF NOT EXISTS `skill_revision_distributions` (`revision_id` varchar(36),`archive_sha256` varchar(64) NOT NULL,`created_at` datetime NOT NULL,PRIMARY KEY (`revision_id`));
CREATE INDEX IF NOT EXISTS `idx_skill_revision_distributions_archive` ON `skill_revision_distributions`(`archive_sha256`);

-- +migrate Dialect postgres
CREATE TABLE IF NOT EXISTS public.dataset_user_states (
    id VARCHAR(64) PRIMARY KEY,
    dataset_id VARCHAR(255) NOT NULL,
    usage_count BIGINT NOT NULL DEFAULT 0,
    last_used_at TIMESTAMP WITH TIME ZONE,
    create_user_id VARCHAR(255) NOT NULL,
    create_user_name VARCHAR(255) NOT NULL DEFAULT '',
    created_at TIMESTAMP WITH TIME ZONE NOT NULL,
    updated_at TIMESTAMP WITH TIME ZONE NOT NULL,
    deleted_at TIMESTAMP WITH TIME ZONE
);

CREATE UNIQUE INDEX IF NOT EXISTS uk_dataset_user_states_user_dataset
    ON public.dataset_user_states (create_user_id, dataset_id);

-- +migrate Dialect sqlite
CREATE TABLE IF NOT EXISTS dataset_user_states (
    id varchar(64) PRIMARY KEY,
    dataset_id varchar(255) NOT NULL,
    usage_count bigint NOT NULL DEFAULT 0,
    last_used_at datetime,
    create_user_id varchar(255) NOT NULL,
    create_user_name varchar(255) NOT NULL DEFAULT '',
    created_at datetime NOT NULL,
    updated_at datetime NOT NULL,
    deleted_at datetime
);

CREATE UNIQUE INDEX IF NOT EXISTS uk_dataset_user_states_user_dataset
    ON dataset_user_states (create_user_id, dataset_id);

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

-- +migrate Dialect postgres
ALTER TABLE public.default_models
    ADD COLUMN IF NOT EXISTS free_auto_select_priority INTEGER NOT NULL DEFAULT 0;
ALTER TABLE public.default_models
    ADD COLUMN IF NOT EXISTS free_auto_select_base_urls TEXT NOT NULL DEFAULT '';
ALTER TABLE public.user_model_provider_group_models
    ADD COLUMN IF NOT EXISTS free_auto_select_priority INTEGER NOT NULL DEFAULT 0;
ALTER TABLE public.user_model_provider_group_models
    ADD COLUMN IF NOT EXISTS free_auto_select_base_urls TEXT NOT NULL DEFAULT '';

-- +migrate Dialect sqlite
ALTER TABLE default_models
    ADD COLUMN free_auto_select_priority INTEGER NOT NULL DEFAULT 0;
ALTER TABLE default_models
    ADD COLUMN free_auto_select_base_urls TEXT NOT NULL DEFAULT '';
ALTER TABLE user_model_provider_group_models
    ADD COLUMN free_auto_select_priority INTEGER NOT NULL DEFAULT 0;
ALTER TABLE user_model_provider_group_models
    ADD COLUMN free_auto_select_base_urls TEXT NOT NULL DEFAULT '';

-- +migrate Dialect postgres
DROP INDEX IF EXISTS public.uk_skills_owner_identity;
CREATE UNIQUE INDEX uk_skills_owner_identity
    ON public.skills(owner_user_id, category, skill_name)
    WHERE deleted_at IS NULL;
DROP INDEX IF EXISTS public.uk_skills_owner_relative_root;
CREATE UNIQUE INDEX uk_skills_owner_relative_root
    ON public.skills(owner_user_id, relative_root)
    WHERE deleted_at IS NULL;

-- +migrate Dialect sqlite
DROP INDEX IF EXISTS uk_skills_owner_identity;
CREATE UNIQUE INDEX uk_skills_owner_identity
    ON skills(owner_user_id, category, skill_name)
    WHERE deleted_at IS NULL;
DROP INDEX IF EXISTS uk_skills_owner_relative_root;
CREATE UNIQUE INDEX uk_skills_owner_relative_root
    ON skills(owner_user_id, relative_root)
    WHERE deleted_at IS NULL;
