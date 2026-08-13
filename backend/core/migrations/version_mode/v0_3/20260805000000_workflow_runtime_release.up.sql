-- +migrate Dialect postgres
ALTER TABLE user_ui_preferences
    ADD COLUMN IF NOT EXISTS task_center_enabled BOOLEAN NOT NULL DEFAULT TRUE;
ALTER TABLE user_ui_preferences
    ADD COLUMN IF NOT EXISTS skills_enabled BOOLEAN NOT NULL DEFAULT TRUE;
ALTER TABLE user_ui_preferences
    ADD COLUMN IF NOT EXISTS mcp_enabled BOOLEAN NOT NULL DEFAULT TRUE;
ALTER TABLE user_ui_preferences
    ADD COLUMN IF NOT EXISTS workflows_enabled BOOLEAN NOT NULL DEFAULT TRUE;
UPDATE user_ui_preferences SET workflows_enabled = skills_enabled;
ALTER TABLE user_ui_preferences
    ADD COLUMN IF NOT EXISTS document_parsing_enabled BOOLEAN NOT NULL DEFAULT TRUE;

-- +migrate Dialect sqlite
ALTER TABLE user_ui_preferences ADD COLUMN task_center_enabled BOOLEAN NOT NULL DEFAULT TRUE;
ALTER TABLE user_ui_preferences ADD COLUMN skills_enabled BOOLEAN NOT NULL DEFAULT TRUE;
ALTER TABLE user_ui_preferences ADD COLUMN mcp_enabled BOOLEAN NOT NULL DEFAULT TRUE;
ALTER TABLE user_ui_preferences ADD COLUMN workflows_enabled BOOLEAN NOT NULL DEFAULT TRUE;
UPDATE user_ui_preferences SET workflows_enabled = skills_enabled;
ALTER TABLE user_ui_preferences ADD COLUMN document_parsing_enabled BOOLEAN NOT NULL DEFAULT TRUE;

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
    provider_thread_id VARCHAR(128) NOT NULL,
    managed_by_lazymind BOOLEAN NOT NULL DEFAULT FALSE,
    created_by_user_id VARCHAR(255) NOT NULL,
    created_at TIMESTAMP NOT NULL,
    updated_at TIMESTAMP NOT NULL,
    CONSTRAINT uk_external_agent_binding_conversation UNIQUE (conversation_id),
    CONSTRAINT uk_external_agent_binding_thread UNIQUE (provider, provider_thread_id)
);

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
    created_at TIMESTAMP NOT NULL,
    updated_at TIMESTAMP NOT NULL,
    CONSTRAINT uk_external_agent_run_request UNIQUE (provider, request_id)
);

CREATE INDEX IF NOT EXISTS idx_external_agent_runs_conversation_id ON external_agent_runs (conversation_id);
CREATE INDEX IF NOT EXISTS idx_external_agent_runs_provider_thread_id ON external_agent_runs (provider_thread_id);
CREATE INDEX IF NOT EXISTS idx_external_agent_runs_status ON external_agent_runs (status);

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
    provider_thread_id VARCHAR(128) NOT NULL,
    managed_by_lazymind BOOLEAN NOT NULL DEFAULT FALSE,
    created_by_user_id VARCHAR(255) NOT NULL,
    created_at TIMESTAMP NOT NULL,
    updated_at TIMESTAMP NOT NULL,
    CONSTRAINT uk_external_agent_binding_conversation UNIQUE (conversation_id),
    CONSTRAINT uk_external_agent_binding_thread UNIQUE (provider, provider_thread_id)
);

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
    created_at TIMESTAMP NOT NULL,
    updated_at TIMESTAMP NOT NULL,
    CONSTRAINT uk_external_agent_run_request UNIQUE (provider, request_id)
);

CREATE INDEX IF NOT EXISTS idx_external_agent_runs_conversation_id ON external_agent_runs (conversation_id);
CREATE INDEX IF NOT EXISTS idx_external_agent_runs_provider_thread_id ON external_agent_runs (provider_thread_id);
CREATE INDEX IF NOT EXISTS idx_external_agent_runs_status ON external_agent_runs (status);

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
