-- +migrate Dialect postgres
ALTER TABLE public.task_center_tasks DROP CONSTRAINT IF EXISTS chk_tct_task_type;
UPDATE public.task_center_tasks SET task_type = 'plugin_run' WHERE task_type = 'workflow_run';
ALTER TABLE public.task_center_tasks
    ADD CONSTRAINT chk_tct_task_type
    CHECK (((task_type)::text = ANY (ARRAY[
        'plugin_run'::text,
        'background_chat'::text,
        'scheduled'::text
    ])));

DROP INDEX IF EXISTS public.idx_skill_revision_distributions_archive;
DROP TABLE IF EXISTS public.skill_revision_distributions;
DROP INDEX IF EXISTS public.idx_skill_distribution_bindings_uid;
DROP TABLE IF EXISTS public.skill_distribution_bindings;
DROP INDEX IF EXISTS public.idx_skill_distribution_entries_blob;
DROP TABLE IF EXISTS public.skill_distribution_entries;
DROP INDEX IF EXISTS public.idx_skill_distribution_artifacts_uid_version;
DROP TABLE IF EXISTS public.skill_distribution_artifacts;
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

DROP TABLE IF EXISTS writer_download_conversions;
DROP INDEX IF EXISTS idx_multi_answers_chat_histories_run_id;
ALTER TABLE multi_answers_chat_histories DROP COLUMN IF EXISTS run_terminal;
ALTER TABLE multi_answers_chat_histories DROP COLUMN IF EXISTS run_status;
ALTER TABLE multi_answers_chat_histories DROP COLUMN IF EXISTS run_id;
DROP INDEX IF EXISTS idx_chat_histories_run_id;
ALTER TABLE chat_histories DROP COLUMN IF EXISTS run_terminal;
ALTER TABLE chat_histories DROP COLUMN IF EXISTS run_status;
ALTER TABLE chat_histories DROP COLUMN IF EXISTS run_id;
DROP INDEX IF EXISTS idx_chat_histories_conversation_seq;
DROP TABLE IF EXISTS agent_invocations;
ALTER TABLE conversations
    DROP COLUMN IF EXISTS thinking_depth,
    DROP COLUMN IF EXISTS chat_executor;
ALTER TABLE user_ui_preferences
    DROP COLUMN IF EXISTS performance_stats_enabled,
    DROP COLUMN IF EXISTS sensitive_word_filter_enabled,
    DROP COLUMN IF EXISTS document_parsing_enabled,
    DROP COLUMN IF EXISTS workflows_enabled,
    DROP COLUMN IF EXISTS mcp_enabled,
    DROP COLUMN IF EXISTS skills_enabled,
    DROP COLUMN IF EXISTS schedules_enabled,
    DROP COLUMN IF EXISTS task_center_enabled;
ALTER TABLE sub_agent_tasks DROP COLUMN IF EXISTS sources;
ALTER TABLE plugin_transition_commands DROP COLUMN IF EXISTS retry_origin;
DROP TABLE IF EXISTS external_agent_operations;
DROP TABLE IF EXISTS external_chat_hosts;
DROP TABLE IF EXISTS external_chat_run_events;
DROP TABLE IF EXISTS external_agent_runs;
DROP TABLE IF EXISTS external_agent_sessions;
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
DROP INDEX IF EXISTS idx_conversations_user_pinned_history;
DROP INDEX IF EXISTS idx_conversations_parent_relation;
DROP INDEX IF EXISTS idx_conversations_ephemeral_expiry;
DROP INDEX IF EXISTS idx_conversations_user_source;
DROP INDEX IF EXISTS idx_conversations_user_ephemeral_history;
DROP TABLE IF EXISTS conversation_archive_folders;
ALTER TABLE conversations DROP CONSTRAINT IF EXISTS chk_conversations_relation_type;
ALTER TABLE conversations DROP COLUMN IF EXISTS source_context;
ALTER TABLE conversations DROP COLUMN IF EXISTS source_selected_text;
ALTER TABLE conversations DROP COLUMN IF EXISTS source_seq;
ALTER TABLE conversations DROP COLUMN IF EXISTS source_history_id;
ALTER TABLE conversations DROP COLUMN IF EXISTS relation_type;
ALTER TABLE conversations DROP COLUMN IF EXISTS parent_conversation_id;
ALTER TABLE conversations DROP COLUMN IF EXISTS chat_model_version;
ALTER TABLE conversations DROP COLUMN IF EXISTS chat_model_snapshot;
ALTER TABLE conversations DROP COLUMN IF EXISTS chat_model_id;
ALTER TABLE conversations DROP COLUMN IF EXISTS chat_model_mode;
ALTER TABLE conversations DROP COLUMN IF EXISTS pinned_at;
ALTER TABLE conversations DROP COLUMN IF EXISTS source_display_name;
ALTER TABLE conversations DROP COLUMN IF EXISTS source_document_id;
ALTER TABLE conversations DROP COLUMN IF EXISTS source_dataset_id;
ALTER TABLE conversations DROP COLUMN IF EXISTS source_type;
ALTER TABLE conversations DROP COLUMN IF EXISTS ephemeral_expires_at;
ALTER TABLE conversations DROP COLUMN IF EXISTS is_ephemeral;
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
ALTER TABLE public.user_chat_settings
    DROP COLUMN IF EXISTS quick_question_defaults,
    DROP COLUMN IF EXISTS new_task_defaults;
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
UPDATE task_center_tasks SET task_type = 'plugin_run' WHERE task_type = 'workflow_run';

DROP INDEX IF EXISTS `idx_skill_revision_distributions_archive`;
DROP TABLE IF EXISTS `skill_revision_distributions`;
DROP INDEX IF EXISTS `idx_skill_distribution_bindings_uid`;
DROP TABLE IF EXISTS `skill_distribution_bindings`;
DROP INDEX IF EXISTS `idx_skill_distribution_entries_blob`;
DROP TABLE IF EXISTS `skill_distribution_entries`;
DROP INDEX IF EXISTS `idx_skill_distribution_artifacts_uid_version`;
DROP TABLE IF EXISTS `skill_distribution_artifacts`;
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

DROP TABLE IF EXISTS writer_download_conversions;
DROP INDEX IF EXISTS idx_multi_answers_chat_histories_run_id;
ALTER TABLE multi_answers_chat_histories DROP COLUMN run_terminal;
ALTER TABLE multi_answers_chat_histories DROP COLUMN run_status;
ALTER TABLE multi_answers_chat_histories DROP COLUMN run_id;
DROP INDEX IF EXISTS idx_chat_histories_run_id;
ALTER TABLE chat_histories DROP COLUMN run_terminal;
ALTER TABLE chat_histories DROP COLUMN run_status;
ALTER TABLE chat_histories DROP COLUMN run_id;
DROP INDEX IF EXISTS idx_chat_histories_conversation_seq;
DROP TABLE IF EXISTS agent_invocations;
ALTER TABLE conversations DROP COLUMN thinking_depth;
ALTER TABLE conversations DROP COLUMN chat_executor;
ALTER TABLE user_ui_preferences DROP COLUMN performance_stats_enabled;
ALTER TABLE user_ui_preferences DROP COLUMN sensitive_word_filter_enabled;
ALTER TABLE user_ui_preferences DROP COLUMN document_parsing_enabled;
ALTER TABLE user_ui_preferences DROP COLUMN workflows_enabled;
ALTER TABLE user_ui_preferences DROP COLUMN mcp_enabled;
ALTER TABLE user_ui_preferences DROP COLUMN skills_enabled;
ALTER TABLE user_ui_preferences DROP COLUMN schedules_enabled;
ALTER TABLE user_ui_preferences DROP COLUMN task_center_enabled;
ALTER TABLE sub_agent_tasks DROP COLUMN sources;
ALTER TABLE plugin_transition_commands DROP COLUMN retry_origin;
DROP TABLE IF EXISTS external_agent_operations;
DROP TABLE IF EXISTS external_chat_hosts;
DROP TABLE IF EXISTS external_chat_run_events;
DROP TABLE IF EXISTS external_agent_runs;
DROP TABLE IF EXISTS external_agent_sessions;
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
DROP INDEX IF EXISTS idx_conversations_user_pinned_history;
DROP INDEX IF EXISTS idx_conversations_parent_relation;
DROP INDEX IF EXISTS idx_conversations_ephemeral_expiry;
DROP INDEX IF EXISTS idx_conversations_user_source;
DROP INDEX IF EXISTS idx_conversations_user_ephemeral_history;
DROP TABLE IF EXISTS conversation_archive_folders;
ALTER TABLE conversations DROP COLUMN source_context;
ALTER TABLE conversations DROP COLUMN source_selected_text;
ALTER TABLE conversations DROP COLUMN source_seq;
ALTER TABLE conversations DROP COLUMN source_history_id;
ALTER TABLE conversations DROP COLUMN relation_type;
ALTER TABLE conversations DROP COLUMN parent_conversation_id;
ALTER TABLE conversations DROP COLUMN chat_model_version;
ALTER TABLE conversations DROP COLUMN chat_model_snapshot;
ALTER TABLE conversations DROP COLUMN chat_model_id;
ALTER TABLE conversations DROP COLUMN chat_model_mode;
ALTER TABLE conversations DROP COLUMN pinned_at;
ALTER TABLE conversations DROP COLUMN source_display_name;
ALTER TABLE conversations DROP COLUMN source_document_id;
ALTER TABLE conversations DROP COLUMN source_dataset_id;
ALTER TABLE conversations DROP COLUMN source_type;
ALTER TABLE conversations DROP COLUMN ephemeral_expires_at;
ALTER TABLE conversations DROP COLUMN is_ephemeral;
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
ALTER TABLE user_chat_settings DROP COLUMN quick_question_defaults;
ALTER TABLE user_chat_settings DROP COLUMN new_task_defaults;
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

-- +migrate Dialect postgres
DROP INDEX IF EXISTS public.idx_knowledge_market_installs_user;
DROP TABLE IF EXISTS public.knowledge_market_installs;
DROP INDEX IF EXISTS public.idx_knowledge_market_items_category_status;
DROP TABLE IF EXISTS public.knowledge_market_items;

-- +migrate Dialect sqlite
DROP INDEX IF EXISTS `idx_knowledge_market_installs_user`;
DROP TABLE IF EXISTS `knowledge_market_installs`;
DROP INDEX IF EXISTS `idx_knowledge_market_items_category_status`;
DROP TABLE IF EXISTS `knowledge_market_items`;

-- +migrate Dialect postgres
DROP INDEX IF EXISTS public.uk_dataset_user_states_user_dataset;
DROP TABLE IF EXISTS public.dataset_user_states;

-- +migrate Dialect sqlite
DROP INDEX IF EXISTS uk_dataset_user_states_user_dataset;
DROP TABLE IF EXISTS dataset_user_states;

-- +migrate Dialect postgres
ALTER TABLE public.user_model_provider_group_models
    DROP COLUMN IF EXISTS free_auto_select_base_urls;
ALTER TABLE public.user_model_provider_group_models
    DROP COLUMN IF EXISTS free_auto_select_priority;
ALTER TABLE public.default_models
    DROP COLUMN IF EXISTS free_auto_select_base_urls;
ALTER TABLE public.default_models
    DROP COLUMN IF EXISTS free_auto_select_priority;

-- +migrate Dialect sqlite
ALTER TABLE user_model_provider_group_models
    DROP COLUMN free_auto_select_base_urls;
ALTER TABLE user_model_provider_group_models
    DROP COLUMN free_auto_select_priority;
ALTER TABLE default_models
    DROP COLUMN free_auto_select_base_urls;
ALTER TABLE default_models
    DROP COLUMN free_auto_select_priority;

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
-- Partial unique indexes allowed a live skill to reuse a trashed name or
-- relative_root. Restore the historical full unique indexes by removing those
-- colliding trashed rows first.
DROP INDEX IF EXISTS uk_skills_owner_identity;
DROP INDEX IF EXISTS uk_skills_owner_relative_root;
DELETE FROM skills
WHERE id IN (
    SELECT id FROM (
        SELECT trashed.id
        FROM skills AS trashed
        INNER JOIN skills AS live
            ON live.deleted_at IS NULL
            AND live.owner_user_id = trashed.owner_user_id
            AND live.category = trashed.category
            AND live.skill_name = trashed.skill_name
        WHERE trashed.deleted_at IS NOT NULL
    ) AS identity_conflicts
);
DELETE FROM skills
WHERE id IN (
    SELECT id FROM (
        SELECT trashed.id
        FROM skills AS trashed
        INNER JOIN skills AS live
            ON live.deleted_at IS NULL
            AND live.owner_user_id = trashed.owner_user_id
            AND live.relative_root = trashed.relative_root
        WHERE trashed.deleted_at IS NOT NULL
    ) AS relative_root_conflicts
);
CREATE UNIQUE INDEX uk_skills_owner_identity
    ON skills(owner_user_id, category, skill_name);
CREATE UNIQUE INDEX uk_skills_owner_relative_root
    ON skills(owner_user_id, relative_root);

-- +migrate Dialect postgres
DROP INDEX IF EXISTS public.uniq_active_preference_organizer;
DROP INDEX IF EXISTS public.uniq_resource_update_running_lane;
DROP INDEX IF EXISTS public.idx_resource_update_tasks_lane_pending;
DELETE FROM public.resource_update_tasks WHERE task_type = 'organize_preference';
ALTER TABLE public.resource_update_tasks DROP CONSTRAINT IF EXISTS chk_resource_update_tasks_task_type;
ALTER TABLE public.resource_update_tasks DROP CONSTRAINT IF EXISTS chk_resource_update_tasks_trigger_type;
ALTER TABLE public.resource_update_tasks ADD CONSTRAINT chk_resource_update_tasks_trigger_type
    CHECK ((trigger_type)::text IN ('scheduled', 'conversation_idle', 'manual', 'review_result', 'auto_evo_enabled'));
ALTER TABLE public.resource_update_tasks
    DROP COLUMN run_id, DROP COLUMN lane_order_at, DROP COLUMN lane_priority, DROP COLUMN lane_key, DROP COLUMN result_json;

-- +migrate Dialect sqlite
DROP INDEX IF EXISTS uniq_active_preference_organizer;
DROP INDEX IF EXISTS uniq_resource_update_running_lane;
DROP INDEX IF EXISTS idx_resource_update_tasks_lane_pending;
DELETE FROM resource_update_tasks WHERE task_type = 'organize_preference';
ALTER TABLE resource_update_tasks DROP COLUMN lane_order_at;
ALTER TABLE resource_update_tasks DROP COLUMN lane_priority;
ALTER TABLE resource_update_tasks DROP COLUMN run_id;
ALTER TABLE resource_update_tasks DROP COLUMN lane_key;
ALTER TABLE resource_update_tasks DROP COLUMN result_json;

-- +migrate Dialect postgres
DROP TABLE IF EXISTS chat_run_performance;

-- +migrate Dialect sqlite
DROP TABLE IF EXISTS chat_run_performance;
