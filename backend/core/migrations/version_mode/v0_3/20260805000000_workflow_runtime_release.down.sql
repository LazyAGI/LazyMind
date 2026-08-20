-- +migrate Dialect postgres
DROP INDEX IF EXISTS idx_chat_histories_conversation_seq;
DROP TABLE IF EXISTS agent_invocations;
ALTER TABLE conversations DROP COLUMN IF EXISTS chat_executor;
ALTER TABLE user_ui_preferences
    DROP COLUMN IF EXISTS document_parsing_enabled,
    DROP COLUMN IF EXISTS workflows_enabled,
    DROP COLUMN IF EXISTS mcp_enabled,
    DROP COLUMN IF EXISTS skills_enabled,
    DROP COLUMN IF EXISTS task_center_enabled;
ALTER TABLE sub_agent_tasks DROP COLUMN IF EXISTS sources;
ALTER TABLE plugin_transition_commands DROP COLUMN IF EXISTS retry_origin;
DROP TABLE IF EXISTS external_agent_operations;
DROP TABLE IF EXISTS external_chat_hosts;
DROP TABLE IF EXISTS external_chat_run_events;
DROP TABLE IF EXISTS external_agent_runs;
DROP TABLE IF EXISTS external_agent_bindings;
DROP TABLE IF EXISTS public.episode_memories;
DROP TABLE IF EXISTS public.memory_current_entries;
DROP INDEX IF EXISTS public.idx_chat_histories_algorithm_create_time;
ALTER TABLE public.chat_histories DROP COLUMN IF EXISTS algorithm_id;
DROP INDEX IF EXISTS idx_plugin_drafts_user_trash;
DROP INDEX IF EXISTS idx_plugin_drafts_user_plugin_id;
CREATE UNIQUE INDEX IF NOT EXISTS idx_plugin_drafts_user_plugin_id
    ON plugin_drafts(created_by, plugin_id) WHERE plugin_id != '';
ALTER TABLE task_center_tasks DROP COLUMN IF EXISTS archived_reason;
ALTER TABLE skills DROP COLUMN IF EXISTS trash_expires_at;
ALTER TABLE plugin_drafts DROP COLUMN IF EXISTS published_status_before_trash;
ALTER TABLE plugin_drafts DROP COLUMN IF EXISTS trash_expires_at;
ALTER TABLE plugin_drafts DROP COLUMN IF EXISTS deleted_at;
DROP INDEX IF EXISTS idx_conversations_user_archive_folder;
DROP INDEX IF EXISTS idx_conversations_user_lifecycle;
DROP TABLE IF EXISTS conversation_archive_folders;
ALTER TABLE conversations DROP COLUMN IF EXISTS archive_folder_id;
ALTER TABLE conversations DROP COLUMN IF EXISTS trash_expires_at;
ALTER TABLE conversations DROP COLUMN IF EXISTS archived_at;
ALTER TABLE plugin_drafts DROP COLUMN IF EXISTS driver_content;
ALTER TABLE plugin_attempt_input_bindings
    DROP COLUMN IF EXISTS content_hash,
    DROP COLUMN IF EXISTS source_revision,
    DROP COLUMN IF EXISTS source_id,
    DROP COLUMN IF EXISTS source_type;
DROP TABLE IF EXISTS workflow_input_bindings;
DROP TABLE IF EXISTS workflow_input_resources;
DROP TABLE IF EXISTS workflow_outbox;
DROP INDEX IF EXISTS idx_plugin_session_steps_claim;
ALTER TABLE plugin_session_steps
    DROP COLUMN IF EXISTS result_json,
    DROP COLUMN IF EXISTS terminal_code,
    DROP COLUMN IF EXISTS progress_json,
    DROP COLUMN IF EXISTS heartbeat_at,
    DROP COLUMN IF EXISTS lease_expires_at,
    DROP COLUMN IF EXISTS fencing_generation,
    DROP COLUMN IF EXISTS lease_token,
    DROP COLUMN IF EXISTS lease_owner;
DROP TABLE IF EXISTS workflow_events;
DROP TABLE IF EXISTS workflow_commands;
DROP TABLE IF EXISTS workflow_preparations;
DROP INDEX IF EXISTS idx_plugin_sessions_origin;
ALTER TABLE plugin_sessions
    DROP COLUMN IF EXISTS controller_host,
    DROP COLUMN IF EXISTS origin_ref,
    DROP COLUMN IF EXISTS origin_host;
ALTER TABLE user_plugin_settings DROP COLUMN IF EXISTS call_mode;
DO $$
BEGIN
    IF EXISTS (
        SELECT 1
        FROM information_schema.columns
        WHERE table_schema = 'public'
          AND table_name = 'user_chat_settings'
          AND column_name = 'enable_workflow'
    ) AND NOT EXISTS (
        SELECT 1
        FROM information_schema.columns
        WHERE table_schema = 'public'
          AND table_name = 'user_chat_settings'
          AND column_name = 'enable_plugin'
    ) THEN
        ALTER TABLE public.user_chat_settings RENAME COLUMN enable_workflow TO enable_plugin;
    ELSIF NOT EXISTS (
        SELECT 1
        FROM information_schema.columns
        WHERE table_schema = 'public'
          AND table_name = 'user_chat_settings'
          AND column_name = 'enable_plugin'
    ) THEN
        ALTER TABLE public.user_chat_settings ADD COLUMN enable_plugin BOOLEAN NOT NULL DEFAULT TRUE;
    END IF;
END $$;

-- +migrate Dialect sqlite
DROP INDEX IF EXISTS idx_chat_histories_conversation_seq;
DROP TABLE IF EXISTS agent_invocations;
ALTER TABLE conversations DROP COLUMN chat_executor;
ALTER TABLE user_ui_preferences DROP COLUMN document_parsing_enabled;
ALTER TABLE user_ui_preferences DROP COLUMN workflows_enabled;
ALTER TABLE user_ui_preferences DROP COLUMN mcp_enabled;
ALTER TABLE user_ui_preferences DROP COLUMN skills_enabled;
ALTER TABLE user_ui_preferences DROP COLUMN task_center_enabled;
ALTER TABLE sub_agent_tasks DROP COLUMN sources;
ALTER TABLE plugin_transition_commands DROP COLUMN retry_origin;
DROP TABLE IF EXISTS external_agent_operations;
DROP TABLE IF EXISTS external_chat_hosts;
DROP TABLE IF EXISTS external_chat_run_events;
DROP TABLE IF EXISTS external_agent_runs;
DROP TABLE IF EXISTS external_agent_bindings;
DROP TABLE IF EXISTS episode_memories;
DROP TABLE IF EXISTS memory_current_entries;
DROP INDEX IF EXISTS idx_chat_histories_algorithm_create_time;
ALTER TABLE chat_histories DROP COLUMN algorithm_id;
DROP INDEX IF EXISTS idx_plugin_drafts_user_trash;
DROP INDEX IF EXISTS idx_plugin_drafts_user_plugin_id;
CREATE UNIQUE INDEX IF NOT EXISTS idx_plugin_drafts_user_plugin_id
    ON plugin_drafts(created_by, plugin_id) WHERE plugin_id != '';
ALTER TABLE task_center_tasks DROP COLUMN archived_reason;
ALTER TABLE skills DROP COLUMN trash_expires_at;
ALTER TABLE plugin_drafts DROP COLUMN published_status_before_trash;
ALTER TABLE plugin_drafts DROP COLUMN trash_expires_at;
ALTER TABLE plugin_drafts DROP COLUMN deleted_at;
DROP INDEX IF EXISTS idx_conversations_user_archive_folder;
DROP INDEX IF EXISTS idx_conversations_user_lifecycle;
DROP TABLE IF EXISTS conversation_archive_folders;
ALTER TABLE conversations DROP COLUMN archive_folder_id;
ALTER TABLE conversations DROP COLUMN trash_expires_at;
ALTER TABLE conversations DROP COLUMN archived_at;
ALTER TABLE plugin_drafts DROP COLUMN driver_content;
ALTER TABLE plugin_attempt_input_bindings DROP COLUMN content_hash;
ALTER TABLE plugin_attempt_input_bindings DROP COLUMN source_revision;
ALTER TABLE plugin_attempt_input_bindings DROP COLUMN source_id;
ALTER TABLE plugin_attempt_input_bindings DROP COLUMN source_type;
DROP TABLE IF EXISTS workflow_input_bindings;
DROP TABLE IF EXISTS workflow_input_resources;
DROP TABLE IF EXISTS workflow_outbox;
DROP INDEX IF EXISTS idx_plugin_session_steps_claim;
ALTER TABLE plugin_session_steps DROP COLUMN result_json;
ALTER TABLE plugin_session_steps DROP COLUMN terminal_code;
ALTER TABLE plugin_session_steps DROP COLUMN progress_json;
ALTER TABLE plugin_session_steps DROP COLUMN heartbeat_at;
ALTER TABLE plugin_session_steps DROP COLUMN lease_expires_at;
ALTER TABLE plugin_session_steps DROP COLUMN fencing_generation;
ALTER TABLE plugin_session_steps DROP COLUMN lease_token;
ALTER TABLE plugin_session_steps DROP COLUMN lease_owner;
DROP TABLE IF EXISTS workflow_events;
DROP TABLE IF EXISTS workflow_commands;
DROP TABLE IF EXISTS workflow_preparations;
DROP INDEX IF EXISTS idx_plugin_sessions_origin;
ALTER TABLE plugin_sessions DROP COLUMN controller_host;
ALTER TABLE plugin_sessions DROP COLUMN origin_ref;
ALTER TABLE plugin_sessions DROP COLUMN origin_host;
ALTER TABLE user_plugin_settings DROP COLUMN call_mode;
CREATE TABLE IF NOT EXISTS user_chat_settings_next (
    user_id varchar(255),
    enable_plugin numeric NOT NULL DEFAULT true,
    plugin_mode varchar(16) NOT NULL DEFAULT "dynamic",
    enable_subagent numeric NOT NULL DEFAULT true,
    updated_at datetime NOT NULL,
    PRIMARY KEY (user_id)
);
DELETE FROM user_chat_settings_next;
INSERT INTO user_chat_settings_next SELECT * FROM user_chat_settings;
DROP TABLE user_chat_settings;
ALTER TABLE user_chat_settings_next RENAME TO user_chat_settings;
