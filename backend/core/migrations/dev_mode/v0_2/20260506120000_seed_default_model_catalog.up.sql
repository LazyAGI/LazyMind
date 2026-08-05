-- +migrate Up
-- +migrate Dialect postgres

-- Default model catalog is seeded at startup from backend/core/config/model_catalog.yaml
-- via modelprovider.SeedModelCatalog().

-- +migrate Dialect sqlite
PRAGMA defer_foreign_keys = ON;
ALTER TABLE "acl_groups" RENAME TO "__v01_acl_groups";
ALTER TABLE "acl_kbs" RENAME TO "__v01_acl_kbs";
ALTER TABLE "acl_rows" RENAME TO "__v01_acl_rows";
ALTER TABLE "acl_user_groups" RENAME TO "__v01_acl_user_groups";
ALTER TABLE "acl_visibility" RENAME TO "__v01_acl_visibility";
ALTER TABLE "agent_thread_records" RENAME TO "__v01_agent_thread_records";
ALTER TABLE "agent_thread_rounds" RENAME TO "__v01_agent_thread_rounds";
ALTER TABLE "agent_threads" RENAME TO "__v01_agent_threads";
ALTER TABLE "agent_user_active_threads" RENAME TO "__v01_agent_user_active_threads";
ALTER TABLE "chat_histories" RENAME TO "__v01_chat_histories";
ALTER TABLE "conversations" RENAME TO "__v01_conversations";
ALTER TABLE "datasets" RENAME TO "__v01_datasets";
ALTER TABLE "default_datasets" RENAME TO "__v01_default_datasets";
ALTER TABLE "default_model_providers" RENAME TO "__v01_default_model_providers";
ALTER TABLE "default_models" RENAME TO "__v01_default_models";
ALTER TABLE "default_prompts" RENAME TO "__v01_default_prompts";
ALTER TABLE "documents" RENAME TO "__v01_documents";
ALTER TABLE "multi_answers_chat_histories" RENAME TO "__v01_multi_answers_chat_histories";
ALTER TABLE "multi_answers_switches" RENAME TO "__v01_multi_answers_switches";
ALTER TABLE "prompts" RENAME TO "__v01_prompts";
ALTER TABLE "resource_session_snapshots" RENAME TO "__v01_resource_session_snapshots";
ALTER TABLE "resource_suggestions" RENAME TO "__v01_resource_suggestions";
ALTER TABLE "skill_resources" RENAME TO "__v01_skill_resources";
ALTER TABLE "skill_share_items" RENAME TO "__v01_skill_share_items";
ALTER TABLE "skill_share_tasks" RENAME TO "__v01_skill_share_tasks";
ALTER TABLE "system_memories" RENAME TO "__v01_system_memories";
ALTER TABLE "system_user_preferences" RENAME TO "__v01_system_user_preferences";
ALTER TABLE "tasks" RENAME TO "__v01_tasks";
ALTER TABLE "upload_sessions" RENAME TO "__v01_upload_sessions";
ALTER TABLE "uploaded_files" RENAME TO "__v01_uploaded_files";
ALTER TABLE "user_model_provider_group_models" RENAME TO "__v01_user_model_provider_group_models";
ALTER TABLE "user_model_provider_groups" RENAME TO "__v01_user_model_provider_groups";
ALTER TABLE "user_model_providers" RENAME TO "__v01_user_model_providers";
ALTER TABLE "user_personalization_settings" RENAME TO "__v01_user_personalization_settings";
ALTER TABLE "user_selected_models" RENAME TO "__v01_user_selected_models";
ALTER TABLE "word_group_conflicts" RENAME TO "__v01_word_group_conflicts";
ALTER TABLE "words" RENAME TO "__v01_words";
DROP INDEX IF EXISTS "idx_acl_resource";
DROP INDEX IF EXISTS "idx_acl_visibility_resource_id";
DROP INDEX IF EXISTS "idx_agent_thread_records_round_stream_id";
DROP INDEX IF EXISTS "idx_agent_thread_records_task_id";
DROP INDEX IF EXISTS "idx_agent_thread_records_thread_round_id";
DROP INDEX IF EXISTS "idx_agent_thread_records_thread_stream_id";
DROP INDEX IF EXISTS "idx_agent_thread_rounds_task_id";
DROP INDEX IF EXISTS "idx_agent_thread_rounds_thread_id";
DROP INDEX IF EXISTS "idx_agent_thread_rounds_thread_request_hash";
DROP INDEX IF EXISTS "idx_agent_threads_current_task_id";
DROP INDEX IF EXISTS "idx_agent_user_active_threads_status_lease";
DROP INDEX IF EXISTS "idx_agent_user_active_threads_thread_id";
DROP INDEX IF EXISTS "idx_chat_histories_conversation_id";
DROP INDEX IF EXISTS "idx_datasets_kb_id";
DROP INDEX IF EXISTS "idx_documents_dataset_id";
DROP INDEX IF EXISTS "idx_documents_lazyllm_doc_id";
DROP INDEX IF EXISTS "idx_documents_p_id";
DROP INDEX IF EXISTS "idx_multi_answers_chat_histories_conversation_id";
DROP INDEX IF EXISTS "idx_resource_session_snapshots_session_id";
DROP INDEX IF EXISTS "idx_resource_suggestions_list";
DROP INDEX IF EXISTS "idx_resource_suggestions_session_id";
DROP INDEX IF EXISTS "idx_resource_uid";
DROP INDEX IF EXISTS "idx_skill_resources_owner_node_enabled";
DROP INDEX IF EXISTS "idx_skill_share_items_target_user";
DROP INDEX IF EXISTS "idx_skill_share_tasks_source_user";
DROP INDEX IF EXISTS "idx_tasks_algo_id";
DROP INDEX IF EXISTS "idx_tasks_dataset_id";
DROP INDEX IF EXISTS "idx_tasks_doc_id";
DROP INDEX IF EXISTS "idx_tasks_document_p_id";
DROP INDEX IF EXISTS "idx_tasks_kb_id";
DROP INDEX IF EXISTS "idx_tasks_lazyllm_task_id";
DROP INDEX IF EXISTS "idx_tasks_target_dataset_id";
DROP INDEX IF EXISTS "idx_tasks_task_type";
DROP INDEX IF EXISTS "idx_upload_sessions_dataset_id";
DROP INDEX IF EXISTS "idx_upload_sessions_document_id";
DROP INDEX IF EXISTS "idx_upload_sessions_task_id";
DROP INDEX IF EXISTS "idx_upload_sessions_tenant_id";
DROP INDEX IF EXISTS "idx_upload_sessions_upload_id";
DROP INDEX IF EXISTS "idx_upload_sessions_upload_state";
DROP INDEX IF EXISTS "idx_uploaded_files_dataset_id";
DROP INDEX IF EXISTS "idx_uploaded_files_document_id";
DROP INDEX IF EXISTS "idx_uploaded_files_status";
DROP INDEX IF EXISTS "idx_uploaded_files_task_id";
DROP INDEX IF EXISTS "idx_uploaded_files_tenant_id";
DROP INDEX IF EXISTS "idx_uploaded_files_upload_file_id";
DROP INDEX IF EXISTS "idx_user_model_provider_group_models_provider";
DROP INDEX IF EXISTS "idx_user_model_provider_groups_parent";
DROP INDEX IF EXISTS "idx_word_column";
DROP INDEX IF EXISTS "idx_word_create_user_group_id";
DROP INDEX IF EXISTS "idx_word_group_conflict_user_updated";
DROP INDEX IF EXISTS "uk_agent_thread_records_record_key";
DROP INDEX IF EXISTS "uk_default_model_providers_name";
DROP INDEX IF EXISTS "uk_default_models_provider_name";
DROP INDEX IF EXISTS "uk_resource_session_snapshots";
DROP INDEX IF EXISTS "uk_skill_resources_owner_relative_path";
DROP INDEX IF EXISTS "uk_system_memories_user_id";
DROP INDEX IF EXISTS "uk_system_user_preferences_user_id";
DROP INDEX IF EXISTS "uk_user_model_provider_group_models_group_name";
DROP INDEX IF EXISTS "uk_user_personalization_settings_user_id";
DROP INDEX IF EXISTS "uk_user_selected_models_user_type";
DROP INDEX IF EXISTS "ukx_create_user_id_dataset_id";

CREATE TABLE IF NOT EXISTS `acl_groups` (`id` varchar(255),`name` varchar(255) NOT NULL DEFAULT "",PRIMARY KEY (`id`));

CREATE TABLE IF NOT EXISTS `acl_kbs` (`id` varchar(64),`name` varchar(255),`owner_id` varchar(255),`visibility` varchar(32),PRIMARY KEY (`id`));

CREATE TABLE IF NOT EXISTS `acl_rows` (`id` integer PRIMARY KEY AUTOINCREMENT,`resource_type` varchar(32),`resource_id` varchar(255),`grantee_type` varchar(32),`target_id` varchar(255),`permission` varchar(32),`created_by` varchar(255),`created_at` datetime,`expires_at` datetime);

CREATE TABLE IF NOT EXISTS `acl_user_groups` (`user_id` varchar(255),`group_id` varchar(255),PRIMARY KEY (`user_id`,`group_id`));

CREATE TABLE IF NOT EXISTS `acl_visibility` (`id` integer PRIMARY KEY AUTOINCREMENT,`resource_id` varchar(255),`level` varchar(32));

CREATE TABLE IF NOT EXISTS `agent_thread_records` (`id` varchar(32),`thread_id` varchar(128) NOT NULL,`round_id` varchar(32) NOT NULL DEFAULT "",`step_id` varchar(128) NOT NULL DEFAULT "",`task_id` varchar(128) NOT NULL DEFAULT "",`stream_kind` varchar(32) NOT NULL,`record_key` varchar(64) NOT NULL,`event_name` varchar(128) NOT NULL DEFAULT "",`payload_text` text NOT NULL DEFAULT "",`raw_frame` text NOT NULL DEFAULT "",`created_at` datetime NOT NULL,`updated_at` datetime NOT NULL,PRIMARY KEY (`id`));

CREATE TABLE IF NOT EXISTS `agent_thread_rounds` (`round_id` varchar(32),`thread_id` varchar(128) NOT NULL,`request_hash` varchar(64) NOT NULL DEFAULT "",`task_id` varchar(128) NOT NULL DEFAULT "",`status` varchar(32) NOT NULL DEFAULT "created",`user_message` text NOT NULL DEFAULT "",`assistant_message` text NOT NULL DEFAULT "",`request_payload` text NOT NULL DEFAULT "",`created_at` datetime NOT NULL,`updated_at` datetime NOT NULL,PRIMARY KEY (`round_id`));

CREATE TABLE IF NOT EXISTS `agent_thread_steps` (`thread_id` varchar(128),`step_id` varchar(128),`stage` varchar(32) NOT NULL DEFAULT "",`title` varchar(255) NOT NULL DEFAULT "",`status` varchar(32) NOT NULL DEFAULT "running",`active` numeric NOT NULL DEFAULT false,`order_index` integer NOT NULL DEFAULT 0,`event_count` integer NOT NULL DEFAULT 0,`current_task_id` varchar(128) NOT NULL DEFAULT "",`next_step_id` varchar(128) NOT NULL DEFAULT "",`version` integer,`started_at` datetime,`ended_at` datetime,`created_at` datetime NOT NULL,`updated_at` datetime NOT NULL,PRIMARY KEY (`thread_id`,`step_id`));

CREATE TABLE IF NOT EXISTS `agent_threads` (`thread_id` varchar(128),`current_task_id` varchar(128) NOT NULL DEFAULT "",`status` varchar(32) NOT NULL DEFAULT "created",`thread_payload` text NOT NULL DEFAULT "",`last_message_request_hash` varchar(64) NOT NULL DEFAULT "",`create_user_id` varchar(255) NOT NULL DEFAULT "",`create_user_name` varchar(255) NOT NULL DEFAULT "",`created_at` datetime NOT NULL,`updated_at` datetime NOT NULL,PRIMARY KEY (`thread_id`));

CREATE TABLE IF NOT EXISTS `agent_user_active_threads` (`user_id` varchar(255),`thread_id` varchar(128) NOT NULL DEFAULT "",`status` varchar(32) NOT NULL DEFAULT "creating",`create_token` varchar(64) NOT NULL DEFAULT "",`lease_until` datetime NOT NULL,`created_at` datetime NOT NULL,`updated_at` datetime NOT NULL,PRIMARY KEY (`user_id`));

CREATE TABLE IF NOT EXISTS `async_jobs` (`id` varchar(64),`job_type` varchar(64) NOT NULL,`status` varchar(32) NOT NULL,`resource_type` varchar(64) NOT NULL DEFAULT "",`resource_id` varchar(128) NOT NULL DEFAULT "",`idempotency_key` varchar(128) NOT NULL DEFAULT "",`payload_json` json,`result_json` json,`error_code` varchar(64) NOT NULL DEFAULT "",`error_message` text NOT NULL DEFAULT "",`error_details_json` json,`progress_current` integer NOT NULL DEFAULT 0,`progress_total` integer NOT NULL DEFAULT 0,`attempt_count` integer NOT NULL DEFAULT 0,`max_attempts` integer NOT NULL DEFAULT 1,`next_run_at` datetime NOT NULL,`locked_by` varchar(128) NOT NULL DEFAULT "",`lock_until` datetime,`started_at` datetime,`finished_at` datetime,`heartbeat_at` datetime,`create_user_id` varchar(255) NOT NULL DEFAULT "",`create_user_name` varchar(255) NOT NULL DEFAULT "",`created_at` datetime NOT NULL,`updated_at` datetime NOT NULL,PRIMARY KEY (`id`));

CREATE TABLE IF NOT EXISTS `automation_groups` (`id` varchar(36),`user_id` varchar(255) NOT NULL,`name` varchar(128) NOT NULL,`remark` text NOT NULL DEFAULT "",`timezone` varchar(64) NOT NULL DEFAULT "Asia/Shanghai",`enabled` numeric NOT NULL DEFAULT true,`created_at` datetime NOT NULL,`updated_at` datetime NOT NULL,PRIMARY KEY (`id`));

CREATE TABLE IF NOT EXISTS `chat_histories` (`id` varchar(36),`seq` integer NOT NULL,`conversation_id` varchar(36) NOT NULL,`raw_content` text,`retrieval_result` json,`content` text,`result` text,`feed_back` integer DEFAULT 0,`reason` varchar(255),`expected_answer` text,`ext` json,`version` varchar(128) DEFAULT "2.3",`tool_call_turns` integer NOT NULL DEFAULT 0,`thinking_duration_s` integer NOT NULL DEFAULT 0,`create_time` datetime NOT NULL,`update_time` datetime NOT NULL,PRIMARY KEY (`id`),CONSTRAINT `chk_chat_histories_tool_call_turns_non_negative` CHECK (tool_call_turns >= 0));

CREATE TABLE IF NOT EXISTS `conversation_artifacts` (`id` varchar(36),`conversation_id` varchar(36) NOT NULL,`history_id` varchar(36) NOT NULL,`filename` varchar(255) NOT NULL,`slot` varchar(255) NOT NULL,`content_type` varchar(32) NOT NULL,`value` jsonb NOT NULL,`caption` text,`create_user_id` varchar(255) NOT NULL,`created_at` datetime NOT NULL,PRIMARY KEY (`id`));

CREATE TABLE IF NOT EXISTS `conversation_idle_events` (`id` varchar(36),`event_id` varchar(512) NOT NULL,`session_id` varchar(128) NOT NULL,`user_id` varchar(255) NOT NULL,`last_message_id` varchar(128) NOT NULL,`last_activity_at` datetime NOT NULL,`due_at` datetime NOT NULL,`status` varchar(32) NOT NULL,`skip_reason` varchar(128) NOT NULL DEFAULT "",`error_code` varchar(64) NOT NULL DEFAULT "",`error_message` text NOT NULL DEFAULT "",`memory_task_id` varchar(36) NOT NULL DEFAULT "",`user_preference_task_id` varchar(36) NOT NULL DEFAULT "",`created_at` datetime NOT NULL,`updated_at` datetime NOT NULL,`triggered_at` datetime,PRIMARY KEY (`id`));

CREATE TABLE IF NOT EXISTS `conversations` (`id` varchar(36),`display_name` varchar(255),`channel_id` varchar(36) NOT NULL DEFAULT "default",`search_config` json,`application_id` varchar(64) DEFAULT "",`ext` json,`model` varchar(64) DEFAULT "",`models` json,`chat_times` integer NOT NULL DEFAULT 0,`enable_plugin` numeric,`plugin_mode` varchar(16),`enable_subagent` numeric,`is_task_conv` numeric NOT NULL DEFAULT false,`create_user_id` varchar(255) NOT NULL,`create_user_name` varchar(255) NOT NULL,`created_at` datetime NOT NULL,`updated_at` datetime NOT NULL,`deleted_at` datetime,PRIMARY KEY (`id`));

CREATE TABLE IF NOT EXISTS `datasets` (`id` varchar(255),`kb_id` varchar(255) NOT NULL,`display_name` varchar(255) NOT NULL,`desc` longtext NOT NULL,`cover_image` varchar(255) NOT NULL,`resource_uid` varchar(36) NOT NULL,`bucket_name` varchar(255) NOT NULL,`oss_path` varchar(255) NOT NULL,`dataset_info` json,`dataset_state` integer NOT NULL,`embedding_model` varchar(255) NOT NULL,`embedding_model_provider` varchar(255) NOT NULL,`share_type` integer NOT NULL,`shared_at` datetime,`tenant_id` varchar(36) NOT NULL,`is_demonstrate` numeric NOT NULL DEFAULT false,`type` integer NOT NULL DEFAULT 1,`ext` json,`create_user_id` varchar(255) NOT NULL,`create_user_name` varchar(255) NOT NULL,`created_at` datetime NOT NULL,`updated_at` datetime NOT NULL,`deleted_at` datetime,PRIMARY KEY (`id`));

CREATE TABLE IF NOT EXISTS `default_datasets` (`id` integer PRIMARY KEY AUTOINCREMENT,`dataset_id` varchar(64) NOT NULL,`dataset_name` varchar(255) NOT NULL,`create_user_id` varchar(255) NOT NULL,`create_user_name` varchar(255) NOT NULL,`created_at` datetime NOT NULL,`updated_at` datetime NOT NULL,`deleted_at` datetime);

CREATE TABLE IF NOT EXISTS `default_model_providers` (`id` varchar(64),`name` varchar(255) NOT NULL,`description` text NOT NULL,`description_i18n` json NOT NULL DEFAULT "{}",`base_url` varchar(1024) NOT NULL DEFAULT "",`category` varchar(64) NOT NULL DEFAULT "model",`capabilities` varchar(512) NOT NULL DEFAULT "multi_group,custom_base_url,has_models",`created_at` datetime NOT NULL,`updated_at` datetime NOT NULL,`deleted_at` datetime,PRIMARY KEY (`id`));

CREATE TABLE IF NOT EXISTS `default_models` (`id` varchar(64),`default_model_provider_id` varchar(64) NOT NULL,`provider_name` varchar(255) NOT NULL DEFAULT "",`name` varchar(512) NOT NULL,`model_type` varchar(64) NOT NULL,`max_input_tokens` varchar(16),`created_at` datetime NOT NULL,`updated_at` datetime NOT NULL,`deleted_at` datetime,PRIMARY KEY (`id`));

CREATE TABLE IF NOT EXISTS `documents` (`id` varchar(128),`lazyllm_doc_id` varchar(128) NOT NULL DEFAULT "",`dataset_id` varchar(255) NOT NULL,`display_name` varchar(512) NOT NULL DEFAULT "",`document_type` varchar(64),`p_id` varchar(255) NOT NULL DEFAULT "",`tags` json,`file_id` varchar(128) NOT NULL DEFAULT "",`pdf_convert_result` varchar(64) NOT NULL DEFAULT "",`ext` json,`create_user_id` varchar(255) NOT NULL,`create_user_name` varchar(255) NOT NULL,`created_at` datetime NOT NULL,`updated_at` datetime NOT NULL,`deleted_at` datetime,PRIMARY KEY (`id`));

CREATE TABLE IF NOT EXISTS `eval_set_import_previews` (`token` varchar(64),`status` varchar(32) NOT NULL DEFAULT "ready",`file_name` varchar(512) NOT NULL DEFAULT "",`file_type` varchar(16) NOT NULL,`temp_path` text NOT NULL DEFAULT "",`total_rows` integer NOT NULL DEFAULT 0,`empty_rows` integer NOT NULL DEFAULT 0,`valid_rows` integer NOT NULL DEFAULT 0,`preview_rows_json` json,`error_details_json` json,`create_user_id` varchar(255) NOT NULL,`create_user_name` varchar(255) NOT NULL DEFAULT "",`created_at` datetime NOT NULL,`expires_at` datetime NOT NULL,`consumed_at` datetime,PRIMARY KEY (`token`));

CREATE TABLE IF NOT EXISTS `eval_set_items` (`shard_id` varchar(64) NOT NULL,`id` varchar(64),`eval_set_id` varchar(64) NOT NULL,`case_id` varchar(255) NOT NULL DEFAULT "",`question` text NOT NULL,`ground_truth` text NOT NULL,`question_type` varchar(128) NOT NULL,`generate_reason` text NOT NULL DEFAULT "",`key_points` text NOT NULL DEFAULT "",`reference_chunk_ids` text NOT NULL DEFAULT "",`reference_context` text NOT NULL DEFAULT "",`algorithm_reference_context` text NOT NULL DEFAULT "",`reference_doc` text NOT NULL DEFAULT "",`reference_doc_ids` text NOT NULL DEFAULT "",`is_deleted` numeric NOT NULL DEFAULT false,`estimated_bytes` integer NOT NULL DEFAULT 0,`source` varchar(32) NOT NULL,`source_session_id` varchar(128) NOT NULL DEFAULT "",`source_history_id` varchar(128) NOT NULL DEFAULT "",`create_user_id` varchar(255) NOT NULL,`create_user_name` varchar(255) NOT NULL DEFAULT "",`created_at` datetime NOT NULL,`updated_at` datetime NOT NULL,PRIMARY KEY (`shard_id`,`id`));

CREATE TABLE IF NOT EXISTS `eval_set_shards` (`id` varchar(64),`status` varchar(32) NOT NULL DEFAULT "open",`row_limit` integer NOT NULL DEFAULT 200000,`row_open_threshold` integer NOT NULL DEFAULT 120000,`size_limit_bytes` integer NOT NULL DEFAULT 8589934592,`size_open_threshold_bytes` integer NOT NULL DEFAULT 5368709120,`actual_rows` integer NOT NULL DEFAULT 0,`estimated_bytes` integer NOT NULL DEFAULT 0,`created_at` datetime NOT NULL,`updated_at` datetime NOT NULL,`sealed_at` datetime,PRIMARY KEY (`id`));

CREATE TABLE IF NOT EXISTS `eval_sets` (`id` varchar(64),`name` varchar(255) NOT NULL,`description` text NOT NULL DEFAULT "",`dataset_ids` jsonb NOT NULL DEFAULT '[]',`owner_id` varchar(255) NOT NULL,`group_id` varchar(255) NOT NULL DEFAULT "",`shard_id` varchar(64) NOT NULL,`status` varchar(32) NOT NULL DEFAULT "active",`item_count` integer NOT NULL DEFAULT 0,`create_user_id` varchar(255) NOT NULL,`create_user_name` varchar(255) NOT NULL DEFAULT "",`created_at` datetime NOT NULL,`updated_at` datetime NOT NULL,PRIMARY KEY (`id`));

CREATE TABLE IF NOT EXISTS `external_database_connections` (`id` varchar(64),`display_name` varchar(255) NOT NULL,`description` text NOT NULL DEFAULT "",`db_type` varchar(32) NOT NULL,`host` varchar(255) NOT NULL,`port` integer NOT NULL,`database_name` varchar(255) NOT NULL,`username` varchar(255) NOT NULL,`password_json` json NOT NULL,`options_json` json NOT NULL,`is_verified` numeric NOT NULL DEFAULT false,`last_checked_at` datetime,`last_check_error` text NOT NULL DEFAULT "",`create_user_id` varchar(255) NOT NULL,`create_user_name` varchar(255) NOT NULL,`created_at` datetime NOT NULL,`updated_at` datetime NOT NULL,`deleted_at` datetime,PRIMARY KEY (`id`));

CREATE TABLE IF NOT EXISTS `local_fs_chat_settings` (`id` integer PRIMARY KEY AUTOINCREMENT,`create_user_id` varchar(255) NOT NULL,`create_user_name` varchar(255) NOT NULL DEFAULT "",`enabled` numeric NOT NULL DEFAULT false,`created_at` datetime NOT NULL,`updated_at` datetime NOT NULL);

CREATE TABLE IF NOT EXISTS `mcp_server_tools` (`id` varchar(64),`mcp_server_id` varchar(64) NOT NULL,`tool_name` varchar(255) NOT NULL,`description` text NOT NULL DEFAULT "",`input_schema_json` json NOT NULL,`last_discovered_at` datetime NOT NULL,`created_at` datetime NOT NULL,`updated_at` datetime NOT NULL,`deleted_at` datetime,PRIMARY KEY (`id`));

CREATE TABLE IF NOT EXISTS `mcp_servers` (`id` varchar(64),`name` varchar(255) NOT NULL,`transport` varchar(32) NOT NULL,`url` text NOT NULL DEFAULT "",`headers_json` json NOT NULL,`allowed_tools_json` json NOT NULL,`enabled` boolean NOT NULL DEFAULT false,`is_verified` boolean NOT NULL DEFAULT false,`share` boolean NOT NULL DEFAULT false,`timeout` integer NOT NULL DEFAULT 5,`create_user_id` varchar(255) NOT NULL,`create_user_name` varchar(255) NOT NULL,`created_at` datetime NOT NULL,`updated_at` datetime NOT NULL,`deleted_at` datetime,PRIMARY KEY (`id`));

CREATE TABLE IF NOT EXISTS `memory_review` (`id` text,`user_id` text NOT NULL DEFAULT "",`target` text NOT NULL,`session_id` text NOT NULL,`source_content` text NOT NULL DEFAULT "",`content` text NOT NULL DEFAULT "",`operations` jsonb NOT NULL DEFAULT '[]',`state` text NOT NULL DEFAULT "success",`review_status` text NOT NULL DEFAULT "pending",`time` datetime NOT NULL,PRIMARY KEY (`id`));

CREATE TABLE IF NOT EXISTS `multi_answers_chat_histories` (`id` varchar(36),`seq` integer NOT NULL,`conversation_id` varchar(36) NOT NULL,`raw_content` text,`retrieval_result` json,`content` text,`result` text,`tool_call_turns` integer NOT NULL DEFAULT 0,`thinking_duration_s` integer NOT NULL DEFAULT 0,`feed_back` integer DEFAULT 0,`reason` varchar(255),`ext` json,`endpoint` varchar(512),`create_time` datetime NOT NULL,`update_time` datetime NOT NULL,PRIMARY KEY (`id`),CONSTRAINT `chk_multi_answers_chat_histories_tool_call_turns_non_negative` CHECK (tool_call_turns >= 0));

CREATE TABLE IF NOT EXISTS `multi_answers_switches` (`id` integer PRIMARY KEY AUTOINCREMENT,`status` integer NOT NULL DEFAULT 0,`create_user_id` varchar(255) NOT NULL,`create_user_name` varchar(255) NOT NULL,`created_at` datetime NOT NULL,`updated_at` datetime NOT NULL,`deleted_at` datetime);

CREATE TABLE IF NOT EXISTS `personal_resource_blobs` (`hash` varchar(64),`size` integer NOT NULL,`mime` varchar(128),`file_type` varchar(32) NOT NULL DEFAULT "unknown",`binary` numeric NOT NULL DEFAULT false,`storage_backend` varchar(32) NOT NULL,`storage_key` text,`content` bytea,`created_at` datetime NOT NULL,PRIMARY KEY (`hash`));

CREATE TABLE IF NOT EXISTS `personal_resource_drafts` (`resource_id` varchar(36),`base_revision_id` varchar(36),`path` varchar(1024) NOT NULL,`blob_hash` varchar(64) NOT NULL,`content_hash` varchar(64) NOT NULL,`size` integer NOT NULL DEFAULT 0,`mime` varchar(128),`file_type` varchar(32) NOT NULL DEFAULT "unknown",`binary` numeric NOT NULL DEFAULT false,`draft_status` varchar(32) NOT NULL DEFAULT "",`draft_updated_at` datetime,`task_id` varchar(128) NOT NULL DEFAULT "",`conversation_id` varchar(128),`updated_by` varchar(255),`version` integer NOT NULL DEFAULT 1,`created_at` datetime NOT NULL,`updated_at` datetime NOT NULL,PRIMARY KEY (`resource_id`));

CREATE TABLE IF NOT EXISTS `personal_resource_review_action_batches` (`id` varchar(36),`session_id` varchar(36) NOT NULL,`resource_id` varchar(36) NOT NULL,`before_draft_blob_hash` varchar(64) NOT NULL,`after_draft_blob_hash` varchar(64) NOT NULL,`before_draft_version` integer NOT NULL,`after_draft_version` integer NOT NULL,`review_version` integer NOT NULL,`created_by` varchar(255),`created_at` datetime NOT NULL,PRIMARY KEY (`id`));

CREATE TABLE IF NOT EXISTS `personal_resource_review_action_items` (`id` varchar(36),`batch_id` varchar(36) NOT NULL,`hunk_id` varchar(128) NOT NULL,`decision` varchar(16) NOT NULL,`old_start` integer NOT NULL DEFAULT 0,`old_lines` integer NOT NULL DEFAULT 0,`new_start` integer NOT NULL DEFAULT 0,`new_lines` integer NOT NULL DEFAULT 0,`created_at` datetime NOT NULL,PRIMARY KEY (`id`));

CREATE TABLE IF NOT EXISTS `personal_resource_review_sessions` (`id` varchar(36),`resource_id` varchar(36) NOT NULL,`path` varchar(1024) NOT NULL,`base_revision_id` varchar(36) NOT NULL,`head_revision_id` varchar(36) NOT NULL,`draft_version` integer NOT NULL,`draft_blob_hash` varchar(64) NOT NULL,`review_version` integer NOT NULL DEFAULT 1,`status` varchar(32) NOT NULL DEFAULT "active",`created_by` varchar(255),`created_at` datetime NOT NULL,`updated_at` datetime NOT NULL,PRIMARY KEY (`id`));

CREATE TABLE IF NOT EXISTS `personal_resource_revisions` (`id` varchar(36),`resource_id` varchar(36) NOT NULL,`parent_revision_id` varchar(36),`revision_no` integer NOT NULL,`path` varchar(1024) NOT NULL,`blob_hash` varchar(64) NOT NULL,`content_hash` varchar(64) NOT NULL,`size` integer NOT NULL DEFAULT 0,`mime` varchar(128),`file_type` varchar(32) NOT NULL DEFAULT "unknown",`binary` numeric NOT NULL DEFAULT false,`message` text,`change_source` varchar(32) NOT NULL DEFAULT "draft_commit",`source_ref_type` varchar(64) NOT NULL DEFAULT "",`source_ref_id` varchar(128) NOT NULL DEFAULT "",`created_by` varchar(255),`created_at` datetime NOT NULL,PRIMARY KEY (`id`));

CREATE TABLE IF NOT EXISTS `personal_resources` (`id` varchar(36),`user_id` varchar(255) NOT NULL,`resource_type` varchar(64) NOT NULL,`head_revision_id` varchar(36),`version` integer NOT NULL DEFAULT 1,`auto_evo` numeric NOT NULL DEFAULT true,`auto_evo_apply_status` varchar(32) NOT NULL DEFAULT "idle",`auto_evo_generation` integer NOT NULL DEFAULT 0,`auto_evo_started_at` datetime,`auto_evo_finished_at` datetime,`auto_evo_error` text NOT NULL DEFAULT "",`ext` json,`updated_by` varchar(255) NOT NULL DEFAULT "",`updated_by_name` varchar(255) NOT NULL DEFAULT "",`created_at` datetime NOT NULL,`updated_at` datetime NOT NULL,PRIMARY KEY (`id`));

CREATE TABLE IF NOT EXISTS `plugin_attempt_input_bindings` (`id` varchar(36),`session_id` varchar(36) NOT NULL,`attempt_id` varchar(36) NOT NULL,`material_id` varchar(64) NOT NULL,`material_revision_id` varchar(36) NOT NULL,`bind_as` varchar(64) NOT NULL DEFAULT "",`created_at` datetime NOT NULL,PRIMARY KEY (`id`));

CREATE TABLE IF NOT EXISTS `plugin_blobs` (`hash` varchar(64),`size` integer NOT NULL,`mime` varchar(128),`file_type` varchar(32) NOT NULL DEFAULT "unknown",`is_binary` numeric NOT NULL DEFAULT false,`content` blob NOT NULL,`created_at` datetime NOT NULL,PRIMARY KEY (`hash`));

CREATE TABLE IF NOT EXISTS `plugin_drafts` (`id` varchar(36),`name` varchar(255) NOT NULL DEFAULT "",`content` text NOT NULL DEFAULT "",`created_by` varchar(255) NOT NULL DEFAULT "",`created_at` datetime NOT NULL,`updated_at` datetime NOT NULL,`plugin_yaml_content` text NOT NULL DEFAULT "",`state_yaml_content` text NOT NULL DEFAULT "",`state_layout_content` text NOT NULL DEFAULT "",`scenario_content` text NOT NULL DEFAULT "",`scripts_content` text NOT NULL DEFAULT "{}",`generate_status` varchar(32) NOT NULL DEFAULT "",`generate_error` text NOT NULL DEFAULT "",`generate_warning` text NOT NULL DEFAULT "",`version` integer NOT NULL DEFAULT 1,`source_type` varchar(16) NOT NULL DEFAULT "",`source_skill_id` varchar(36) NOT NULL DEFAULT "",`source_skill_name` varchar(255) NOT NULL DEFAULT "",`source_skill_revision_id` varchar(36) NOT NULL DEFAULT "",`source_skill_revision_no` integer NOT NULL DEFAULT 0,`source_skill_tree_hash` varchar(64) NOT NULL DEFAULT "",`source_analysis_id` varchar(36) NOT NULL DEFAULT "",`design_brief_content` text NOT NULL DEFAULT "",`plugin_id` varchar(255) NOT NULL DEFAULT "",`base_revision_id` varchar(36) NOT NULL DEFAULT "",PRIMARY KEY (`id`));

CREATE TABLE IF NOT EXISTS `plugin_human_artifacts` (`id` varchar(36),`session_id` varchar(36) NOT NULL,`slot` varchar(64) NOT NULL,`content_type` varchar(32) NOT NULL,`value` jsonb NOT NULL,`caption` text,`created_at` datetime NOT NULL,PRIMARY KEY (`id`));

CREATE TABLE IF NOT EXISTS `plugin_revision_entries` (`revision_id` varchar(36),`path` varchar(1024),`entry_type` varchar(16) NOT NULL DEFAULT "file",`blob_hash` varchar(64),`size` integer NOT NULL DEFAULT 0,`mime` varchar(128),`file_type` varchar(32) NOT NULL DEFAULT "unknown",`is_binary` numeric NOT NULL DEFAULT false,`mode` integer NOT NULL DEFAULT 420,PRIMARY KEY (`revision_id`,`path`));

CREATE TABLE IF NOT EXISTS `plugin_revisions` (`id` varchar(36),`plugin_resource_id` varchar(36) NOT NULL,`parent_revision_id` varchar(36),`revision_no` integer NOT NULL,`tree_hash` varchar(64) NOT NULL,`compiled_graph` jsonb,`graph_hash` varchar(64) NOT NULL DEFAULT "",`graph_schema_version` varchar(16) NOT NULL DEFAULT "",`message` text NOT NULL DEFAULT "",`created_by` varchar(255),`created_at` datetime NOT NULL,PRIMARY KEY (`id`));

CREATE TABLE IF NOT EXISTS `plugin_route_decisions` (`id` varchar(36),`session_id` varchar(36) NOT NULL,`from_step_id` varchar(64) NOT NULL,`source_attempt_id` varchar(36) NOT NULL DEFAULT "",`activated_json` jsonb NOT NULL,`pruned_json` jsonb NOT NULL,`bypassed_json` jsonb NOT NULL,`witness_json` jsonb NOT NULL,`validity` varchar(16) NOT NULL DEFAULT "effective",`state_version` integer NOT NULL,`created_at` datetime NOT NULL,PRIMARY KEY (`id`));

CREATE TABLE IF NOT EXISTS `plugin_run_outbox` (`task_id` varchar(36),`payload` jsonb NOT NULL,`status` varchar(16) NOT NULL,`last_error` text NOT NULL DEFAULT "",`created_at` datetime NOT NULL,`updated_at` datetime NOT NULL,PRIMARY KEY (`task_id`));

CREATE TABLE IF NOT EXISTS `plugin_session_steps` (`id` varchar(36),`session_id` varchar(36) NOT NULL,`step_id` varchar(64) NOT NULL,`attempt` integer NOT NULL DEFAULT 1,`task_id` varchar(36) NOT NULL,`status` varchar(16) NOT NULL DEFAULT "pending",`validity` varchar(16) NOT NULL DEFAULT "effective",`created_at` datetime NOT NULL,`updated_at` datetime NOT NULL,PRIMARY KEY (`id`));

CREATE TABLE IF NOT EXISTS `plugin_sessions` (`id` varchar(36),`conversation_id` varchar(36) NOT NULL,`plugin_id` varchar(64) NOT NULL,`plugin_ref` varchar(512) NOT NULL DEFAULT "",`plugin_revision_id` varchar(36) NOT NULL DEFAULT "",`plugin_revision_no` integer NOT NULL DEFAULT 0,`plugin_tree_hash` varchar(64) NOT NULL DEFAULT "",`plugin_remote_root` varchar(1024) NOT NULL DEFAULT "",`state_version` integer NOT NULL DEFAULT 0,`graph_hash` varchar(64) NOT NULL DEFAULT "",`graph_schema_version` varchar(16) NOT NULL DEFAULT "",`trigger_history_id` varchar(36),`status` varchar(16) NOT NULL DEFAULT "active",`current_step_id` varchar(64),`dismissed` boolean NOT NULL DEFAULT false,`intent_context` text NOT NULL DEFAULT "{}",`create_user_id` varchar(255) NOT NULL DEFAULT "",`created_at` datetime NOT NULL,`updated_at` datetime NOT NULL,PRIMARY KEY (`id`));

CREATE TABLE IF NOT EXISTS `plugin_slot_order` (`session_id` varchar(36) NOT NULL,`slot_id` varchar(64) NOT NULL,`order_list` jsonb NOT NULL DEFAULT '[]',`order_version` integer NOT NULL DEFAULT 0,`updated_at` datetime NOT NULL,PRIMARY KEY (`session_id`,`slot_id`));

CREATE TABLE IF NOT EXISTS `plugin_slot_revisions` (`id` varchar(36),`session_id` varchar(36) NOT NULL,`slot_id` varchar(64) NOT NULL,`revision` integer NOT NULL,`list_index` integer,`selected` numeric NOT NULL DEFAULT true,`artifact_seq` integer,`human_artifact_id` varchar(36),`content_snapshot` jsonb,`change_source` varchar(16) NOT NULL DEFAULT "ai",`slot` varchar(255) NOT NULL,`step_id` varchar(64) NOT NULL,`attempt` integer NOT NULL,`validity` varchar(16) NOT NULL DEFAULT "effective",`producer_attempt_id` varchar(36) NOT NULL DEFAULT "",`created_at` datetime NOT NULL,PRIMARY KEY (`id`));

CREATE TABLE IF NOT EXISTS `plugin_step_intents` (`id` varchar(36),`session_id` varchar(36) NOT NULL,`step_id` varchar(64) NOT NULL,`intent_context` text NOT NULL DEFAULT "{}",`updated_at` datetime NOT NULL,PRIMARY KEY (`id`));

CREATE TABLE IF NOT EXISTS `plugin_transition_commands` (`command_id` varchar(36),`session_id` varchar(36) NOT NULL DEFAULT "",`operation` varchar(16) NOT NULL,`target_step_id` varchar(64) NOT NULL DEFAULT "",`status` varchar(16) NOT NULL,`task_id` varchar(36) NOT NULL DEFAULT "",`expected_state_version` integer NOT NULL DEFAULT 0,`resulting_state_version` integer NOT NULL DEFAULT 0,`response_json` jsonb NOT NULL,`created_at` datetime NOT NULL,`updated_at` datetime NOT NULL,PRIMARY KEY (`command_id`));

CREATE TABLE IF NOT EXISTS `plugins` (`id` varchar(36),`plugin_ref` varchar(512) NOT NULL,`plugin_id` varchar(255) NOT NULL,`owner_user_id` varchar(255) NOT NULL,`owner_scope` varchar(128) NOT NULL,`source_type` varchar(16) NOT NULL DEFAULT "user",`relative_root` varchar(1024) NOT NULL,`name` varchar(255) NOT NULL DEFAULT "",`description` text NOT NULL DEFAULT "",`when_to_use` text NOT NULL DEFAULT "",`head_revision_id` varchar(36),`version` integer NOT NULL DEFAULT 0,`status` varchar(16) NOT NULL DEFAULT "active",`contains_scripts` numeric NOT NULL DEFAULT false,`created_at` datetime NOT NULL,`updated_at` datetime NOT NULL,PRIMARY KEY (`id`));

CREATE TABLE IF NOT EXISTS `prompt_categories` (`id` varchar(64),`name` varchar(64) NOT NULL,`create_user_id` varchar(255) NOT NULL,`create_user_name` varchar(255) NOT NULL,`created_at` datetime NOT NULL,`updated_at` datetime NOT NULL,`deleted_at` datetime,PRIMARY KEY (`id`));

CREATE TABLE IF NOT EXISTS `prompt_user_states` (`id` varchar(64),`prompt_id` varchar(64) NOT NULL,`is_favorite` boolean NOT NULL DEFAULT false,`usage_count` bigint NOT NULL DEFAULT 0,`last_used_at` datetime,`create_user_id` varchar(255) NOT NULL,`create_user_name` varchar(255) NOT NULL,`created_at` datetime NOT NULL,`updated_at` datetime NOT NULL,`deleted_at` datetime,PRIMARY KEY (`id`));

CREATE TABLE IF NOT EXISTS `prompts` (`id` varchar(64),`name` varchar(255) NOT NULL,`content` text NOT NULL,`category` varchar(64) NOT NULL DEFAULT "custom",`create_user_id` varchar(255) NOT NULL,`create_user_name` varchar(255) NOT NULL,`created_at` datetime NOT NULL,`updated_at` datetime NOT NULL,`deleted_at` datetime,PRIMARY KEY (`id`));

CREATE TABLE IF NOT EXISTS `resource_session_snapshots` (`id` varchar(36),`session_id` varchar(128) NOT NULL,`user_id` varchar(255) NOT NULL DEFAULT "",`resource_type` varchar(32) NOT NULL,`resource_key` varchar(1024) NOT NULL,`category` varchar(128) NOT NULL DEFAULT "",`parent_skill_name` varchar(255) NOT NULL DEFAULT "",`skill_name` varchar(255) NOT NULL DEFAULT "",`file_ext` varchar(32) NOT NULL DEFAULT "",`relative_path` varchar(1024) NOT NULL DEFAULT "",`snapshot_hash` varchar(64) NOT NULL DEFAULT "",`created_at` datetime NOT NULL,PRIMARY KEY (`id`));

CREATE TABLE IF NOT EXISTS `resource_update_tasks` (`id` varchar(36),`task_type` varchar(32) NOT NULL,`resource_type` varchar(32) NOT NULL,`user_id` varchar(255) NOT NULL DEFAULT "",`resource_id` varchar(128) NOT NULL DEFAULT "",`trigger_type` varchar(32) NOT NULL,`trigger_id` varchar(512) NOT NULL,`status` varchar(32) NOT NULL,`request_json` json,`review_result_id` varchar(128),`result_id` varchar(128),`error_code` varchar(64) NOT NULL DEFAULT "",`error_message` text NOT NULL DEFAULT "",`attempt_count` integer NOT NULL DEFAULT 0,`next_run_at` datetime NOT NULL,`locked_by` varchar(128) NOT NULL DEFAULT "",`locked_until` datetime,`created_at` datetime NOT NULL,`updated_at` datetime NOT NULL,`started_at` datetime,`finished_at` datetime,PRIMARY KEY (`id`));

CREATE TABLE IF NOT EXISTS `schedule_dependencies` (`id` varchar(36),`user_id` varchar(255) NOT NULL,`source_schedule_id` varchar(36) NOT NULL,`target_schedule_id` varchar(36) NOT NULL,`window_type` varchar(32) NOT NULL DEFAULT "between_target_fires",`window_config_json` text,`content_types_json` text,`incomplete_policy` varchar(48) NOT NULL DEFAULT "wait_then_run_with_warning",`max_wait_seconds` integer NOT NULL DEFAULT 7200,`enabled` numeric NOT NULL DEFAULT true,`created_at` datetime NOT NULL,`updated_at` datetime NOT NULL,PRIMARY KEY (`id`));

CREATE TABLE IF NOT EXISTS `skill_blobs` (`hash` varchar(64),`size` integer NOT NULL,`mime` varchar(128),`file_type` varchar(32) NOT NULL DEFAULT "unknown",`binary` numeric NOT NULL DEFAULT false,`storage_backend` varchar(32) NOT NULL,`storage_key` text,`content` bytea,`created_at` datetime NOT NULL,PRIMARY KEY (`hash`));

CREATE TABLE IF NOT EXISTS `skill_draft_entries` (`skill_id` varchar(36),`path` varchar(1024),`op` varchar(16) NOT NULL,`entry_type` varchar(16),`blob_hash` varchar(64),`size` integer,`mime` varchar(128),`file_type` varchar(32),`binary` numeric,`mode` integer,`updated_at` datetime NOT NULL,PRIMARY KEY (`skill_id`,`path`));

CREATE TABLE IF NOT EXISTS `skill_draft_review_action_batches` (`id` varchar(36),`review_session_id` varchar(36) NOT NULL,`sequence` integer NOT NULL,`undo_locked` numeric NOT NULL DEFAULT false,`undone_at` datetime,`undone_by` varchar(255),`created_by` varchar(255),`created_at` datetime NOT NULL,PRIMARY KEY (`id`));

CREATE TABLE IF NOT EXISTS `skill_draft_review_action_items` (`id` varchar(36),`batch_id` varchar(36) NOT NULL,`review_session_id` varchar(36) NOT NULL,`path` varchar(1024) NOT NULL,`hunk_id` varchar(128) NOT NULL,`before_decision` varchar(16) NOT NULL DEFAULT "pending",`after_decision` varchar(16) NOT NULL,`created_at` datetime NOT NULL,PRIMARY KEY (`id`));

CREATE TABLE IF NOT EXISTS `skill_draft_review_sessions` (`id` varchar(36),`skill_id` varchar(36) NOT NULL,`base_revision_id` varchar(36) NOT NULL,`draft_version_at_start` integer NOT NULL,`draft_snapshot_hash` varchar(64) NOT NULL,`status` varchar(32) NOT NULL DEFAULT "active",`version` integer NOT NULL DEFAULT 1,`undo_limit` integer NOT NULL DEFAULT 20,`created_by` varchar(255),`updated_by` varchar(255),`created_at` datetime NOT NULL,`updated_at` datetime NOT NULL,PRIMARY KEY (`id`));

CREATE TABLE IF NOT EXISTS `skill_drafts` (`skill_id` varchar(36),`base_revision_id` varchar(36),`draft_status` varchar(32) NOT NULL DEFAULT "",`draft_updated_at` datetime,`task_id` varchar(128) NOT NULL DEFAULT "",`conversation_id` varchar(128),`updated_by` varchar(255),`version` integer NOT NULL DEFAULT 1,`created_at` datetime NOT NULL,`updated_at` datetime NOT NULL,PRIMARY KEY (`skill_id`));

CREATE TABLE IF NOT EXISTS `skill_market_items` (`id` varchar(36),`source_skill_id` varchar(36) NOT NULL,`status` varchar(32) NOT NULL DEFAULT "draft",`tags` json NOT NULL DEFAULT '[]',`icon` text NOT NULL DEFAULT "",`sort_order` integer NOT NULL DEFAULT 0,`version_note` text NOT NULL DEFAULT "",`created_by` varchar(255),`updated_by` varchar(255),`published_at` datetime,`created_at` datetime NOT NULL,`updated_at` datetime NOT NULL,PRIMARY KEY (`id`));

CREATE TABLE IF NOT EXISTS `skill_review_results` (`id` text,`skill_name` text NOT NULL,`type` text NOT NULL,`review_status` text NOT NULL DEFAULT "pending",`userid` text NOT NULL,`requestid` text NOT NULL,`skill_content` text NOT NULL,`summary` text,`time` datetime NOT NULL,PRIMARY KEY (`id`));

CREATE TABLE IF NOT EXISTS `skill_review_scheduler_state` (`user_id` varchar(255),`last_window_end` datetime NOT NULL,`next_run_at` datetime NOT NULL,`stage_index` integer NOT NULL DEFAULT 0,`stage_success_count` integer NOT NULL DEFAULT 0,`total_success_count` integer NOT NULL DEFAULT 0,`last_accepted_at` datetime,`last_quantity_check_at` datetime,`last_preflight_check_at` datetime,`active_task_id` varchar(36) NOT NULL DEFAULT "",`locked_by` varchar(128) NOT NULL DEFAULT "",`locked_until` datetime,`last_error_code` varchar(64) NOT NULL DEFAULT "",`last_error_message` text NOT NULL DEFAULT "",`created_at` datetime NOT NULL,`updated_at` datetime NOT NULL,PRIMARY KEY (`user_id`));

CREATE TABLE IF NOT EXISTS `skill_review_stats` (`id` text,`requestid` text NOT NULL,`userid` text NOT NULL,`status` text NOT NULL,`started_at` text NOT NULL,`duration_ms` integer NOT NULL DEFAULT 0,`summary` text NOT NULL DEFAULT "{}",PRIMARY KEY (`id`));

CREATE TABLE IF NOT EXISTS `skill_revision_entries` (`revision_id` varchar(36),`path` varchar(1024),`entry_type` varchar(16) NOT NULL,`blob_hash` varchar(64),`size` integer,`mime` varchar(128),`file_type` varchar(32) NOT NULL DEFAULT "unknown",`binary` numeric NOT NULL DEFAULT false,`mode` integer NOT NULL DEFAULT 420,PRIMARY KEY (`revision_id`,`path`));

CREATE TABLE IF NOT EXISTS `skill_revisions` (`id` varchar(36),`skill_id` varchar(36) NOT NULL,`parent_revision_id` varchar(36),`revision_no` integer NOT NULL,`tree_hash` varchar(64) NOT NULL,`message` text,`change_source` varchar(32) NOT NULL DEFAULT "draft_commit",`source_ref_type` varchar(64) NOT NULL DEFAULT "",`source_ref_id` varchar(128) NOT NULL DEFAULT "",`created_by` varchar(255),`created_at` datetime NOT NULL,PRIMARY KEY (`id`));

CREATE TABLE IF NOT EXISTS `skill_search_indexes` (`skill_id` varchar(36),`owner_user_id` varchar(255) NOT NULL,`head_revision_id` varchar(36) NOT NULL,`content` text NOT NULL,`updated_at` datetime NOT NULL,PRIMARY KEY (`skill_id`));

CREATE TABLE IF NOT EXISTS `skill_share_items` (`id` varchar(36),`share_task_id` varchar(36) NOT NULL,`source_skill_id` varchar(36) NOT NULL DEFAULT "",`target_user_id` varchar(255) NOT NULL,`target_user_name` varchar(255) NOT NULL DEFAULT "",`status` varchar(32) NOT NULL,`target_relative_root` varchar(1024) NOT NULL DEFAULT "",`accepted_at` datetime,`rejected_at` datetime,`target_root_skill_id` varchar(36) NOT NULL DEFAULT "",`error_message` text,`created_at` datetime NOT NULL,`updated_at` datetime NOT NULL,PRIMARY KEY (`id`));

CREATE TABLE IF NOT EXISTS `skill_share_tasks` (`id` varchar(36),`source_user_id` varchar(255) NOT NULL,`source_user_name` varchar(255) NOT NULL DEFAULT "",`source_skill_id` varchar(36) NOT NULL,`source_category` varchar(128) NOT NULL DEFAULT "",`source_parent_skill_name` varchar(255) NOT NULL DEFAULT "",`source_relative_root` varchar(1024) NOT NULL DEFAULT "",`message` text,`created_at` datetime NOT NULL,`updated_at` datetime NOT NULL,PRIMARY KEY (`id`));

CREATE TABLE IF NOT EXISTS `skills` (`id` varchar(36),`owner_user_id` varchar(255) NOT NULL,`owner_user_name` varchar(255) NOT NULL DEFAULT "",`create_user_id` varchar(255) NOT NULL,`create_user_name` varchar(255) NOT NULL DEFAULT "",`category` varchar(128) NOT NULL,`skill_name` varchar(255) NOT NULL,`origin_builtin_skill_uid` varchar(64) NOT NULL DEFAULT "",`description` text,`tags` json,`relative_root` varchar(1024) NOT NULL,`skill_md_path` varchar(1024) NOT NULL DEFAULT "SKILL.md",`head_revision_id` varchar(36),`version` integer NOT NULL DEFAULT 1,`auto_evo` numeric NOT NULL DEFAULT false,`auto_evo_apply_status` varchar(32) NOT NULL DEFAULT "idle",`auto_evo_generation` integer NOT NULL DEFAULT 0,`auto_evo_started_at` datetime,`auto_evo_finished_at` datetime,`auto_evo_error` text NOT NULL DEFAULT "",`is_enabled` numeric NOT NULL DEFAULT true,`update_status` varchar(32) NOT NULL DEFAULT "up_to_date",`ext` json,`deleted_at` datetime,`deleted_by` varchar(255),`created_at` datetime NOT NULL,`updated_at` datetime NOT NULL,PRIMARY KEY (`id`));

CREATE TABLE IF NOT EXISTS `sub_agent_artifacts` (`id` varchar(36),`task_id` varchar(36) NOT NULL,`slot` varchar(64) NOT NULL,`content_type` varchar(32) NOT NULL,`value` json NOT NULL,`seq` integer NOT NULL DEFAULT 1,`hidden` numeric NOT NULL DEFAULT false,`caption` text,`created_at` datetime NOT NULL,PRIMARY KEY (`id`));

CREATE TABLE IF NOT EXISTS `sub_agent_steps` (`id` varchar(36),`task_id` varchar(36) NOT NULL,`seq` integer NOT NULL,`role` varchar(16) NOT NULL,`content` json NOT NULL,`created_at` datetime NOT NULL,PRIMARY KEY (`id`));

CREATE TABLE IF NOT EXISTS `sub_agent_tasks` (`id` varchar(36),`conversation_id` varchar(36) NOT NULL,`trigger_history_id` varchar(36),`seq_in_conversation` integer NOT NULL,`agent_type` varchar(64) NOT NULL,`title` varchar(255) NOT NULL,`objective` text NOT NULL DEFAULT "",`params` json,`mode` varchar(8) NOT NULL,`status` varchar(16) NOT NULL DEFAULT "pending",`progress_pct` integer NOT NULL DEFAULT 0,`current_phase` text,`estimated_sec` integer,`summary` text NOT NULL DEFAULT "",`last_heartbeat` datetime NOT NULL,`workspace_path` varchar(512) NOT NULL DEFAULT "",`input_slots` json NOT NULL DEFAULT '[]',`output_slots` json NOT NULL DEFAULT '[]',`create_user_id` varchar(255) NOT NULL DEFAULT "",`created_at` datetime NOT NULL,`updated_at` datetime NOT NULL,PRIMARY KEY (`id`));

CREATE TABLE IF NOT EXISTS `task_center_tasks` (`id` varchar(36),`user_id` varchar(255) NOT NULL,`conversation_id` varchar(36) NOT NULL,`plugin_session_id` varchar(36),`task_type` varchar(32) NOT NULL,`title` text,`status` varchar(16) NOT NULL DEFAULT "pending",`schedule_id` varchar(36),`group_id` varchar(36),`scheduled_fire_at` datetime,`logical_slot_key` varchar(160),`window_start` datetime,`window_end` datetime,`trigger_type` varchar(32) NOT NULL DEFAULT "manual",`attempt` integer NOT NULL DEFAULT 1,`definition_version` integer NOT NULL DEFAULT 1,`dependency_status` varchar(32) NOT NULL DEFAULT "none",`has_late_inputs` numeric NOT NULL DEFAULT false,`progress_json` text,`predicted_completion_at` datetime,`created_at` datetime NOT NULL,`updated_at` datetime NOT NULL,`finished_at` datetime,`archived_at` datetime,PRIMARY KEY (`id`));

CREATE TABLE IF NOT EXISTS `task_run_inputs` (`id` varchar(36),`downstream_task_id` varchar(36) NOT NULL,`upstream_task_id` varchar(36) NOT NULL,`dependency_id` varchar(36) NOT NULL,`source_logical_slot_key` varchar(160),`output_id` varchar(36) NOT NULL,`output_content_hash` varchar(64) NOT NULL,`position` integer NOT NULL,`snapshot_json` text,`created_at` datetime NOT NULL,PRIMARY KEY (`id`));

CREATE TABLE IF NOT EXISTS `task_run_outputs` (`id` varchar(36),`task_id` varchar(36) NOT NULL,`conversation_id` varchar(36) NOT NULL,`final_answer_text` text,`summary_text` text,`artifact_manifest_json` text,`output_status` varchar(24) NOT NULL,`content_hash` varchar(64) NOT NULL,`created_at` datetime NOT NULL,`updated_at` datetime NOT NULL,PRIMARY KEY (`id`));

CREATE TABLE IF NOT EXISTS `tasks` (`id` varchar(128),`lazyllm_task_id` varchar(128) NOT NULL DEFAULT "",`doc_id` varchar(128),`kb_id` varchar(255),`algo_id` varchar(255),`dataset_id` varchar(255) NOT NULL,`task_type` varchar(128) NOT NULL DEFAULT "",`document_pid` varchar(255) NOT NULL DEFAULT "",`target_pid` varchar(255) NOT NULL DEFAULT "",`target_dataset_id` varchar(255) NOT NULL DEFAULT "",`display_name` varchar(512) NOT NULL DEFAULT "",`ext` json,`create_user_id` varchar(255) NOT NULL,`create_user_name` varchar(255) NOT NULL,`created_at` datetime NOT NULL,`updated_at` datetime NOT NULL,`deleted_at` datetime,PRIMARY KEY (`id`));

CREATE TABLE IF NOT EXISTS `upload_sessions` (`id` integer PRIMARY KEY AUTOINCREMENT,`upload_id` varchar(128) NOT NULL,`task_id` varchar(128) NOT NULL,`dataset_id` varchar(255) NOT NULL,`tenant_id` varchar(36) NOT NULL,`document_id` varchar(128) NOT NULL,`upload_state` varchar(64) NOT NULL DEFAULT "",`ext` json,`create_user_id` varchar(255) NOT NULL,`create_user_name` varchar(255) NOT NULL,`created_at` datetime NOT NULL,`updated_at` datetime NOT NULL,`deleted_at` datetime);

CREATE TABLE IF NOT EXISTS `uploaded_files` (`id` integer PRIMARY KEY AUTOINCREMENT,`upload_file_id` varchar(128) NOT NULL,`dataset_id` varchar(255) NOT NULL,`tenant_id` varchar(36) NOT NULL,`content_hash` varchar(64) NOT NULL DEFAULT "",`task_id` varchar(128) NOT NULL DEFAULT "",`document_id` varchar(128) NOT NULL DEFAULT "",`status` varchar(64) NOT NULL DEFAULT "",`ext` json,`create_user_id` varchar(255) NOT NULL,`create_user_name` varchar(255) NOT NULL,`created_at` datetime NOT NULL,`updated_at` datetime NOT NULL,`deleted_at` datetime);

CREATE TABLE IF NOT EXISTS `user_chat_settings` (`user_id` varchar(255),`enable_plugin` numeric NOT NULL DEFAULT true,`plugin_mode` varchar(16) NOT NULL DEFAULT "dynamic",`enable_subagent` numeric NOT NULL DEFAULT true,`updated_at` datetime NOT NULL,PRIMARY KEY (`user_id`));

CREATE TABLE IF NOT EXISTS `user_disabled_tools` (`id` integer PRIMARY KEY AUTOINCREMENT,`tool_name` varchar(255) NOT NULL,`create_user_id` varchar(255) NOT NULL,`create_user_name` varchar(255) NOT NULL,`created_at` datetime NOT NULL,`updated_at` datetime NOT NULL,`deleted_at` datetime);

CREATE TABLE IF NOT EXISTS `user_model_provider_group_models` (`id` varchar(64),`user_model_provider_id` varchar(64) NOT NULL,`user_model_provider_group_id` varchar(64) NOT NULL,`provider_name` varchar(255) NOT NULL DEFAULT "",`name` varchar(512) NOT NULL,`model_type` varchar(64) NOT NULL,`max_input_tokens` varchar(16),`is_default` boolean NOT NULL DEFAULT false,`create_user_id` varchar(255) NOT NULL,`create_user_name` varchar(255) NOT NULL,`created_at` datetime NOT NULL,`updated_at` datetime NOT NULL,`deleted_at` datetime,PRIMARY KEY (`id`));

CREATE TABLE IF NOT EXISTS `user_model_provider_groups` (`id` varchar(64),`user_model_provider_id` varchar(64) NOT NULL,`name` varchar(255) NOT NULL,`base_url` varchar(1024) NOT NULL,`api_key` text NOT NULL,`api_key_ciphertext` text NOT NULL DEFAULT "",`credential_version` integer NOT NULL DEFAULT 0,`is_verified` boolean NOT NULL DEFAULT false,`create_user_id` varchar(255) NOT NULL,`create_user_name` varchar(255) NOT NULL,`created_at` datetime NOT NULL,`updated_at` datetime NOT NULL,`deleted_at` datetime,PRIMARY KEY (`id`));

CREATE TABLE IF NOT EXISTS `user_model_providers` (`id` varchar(64),`default_model_provider_id` varchar(64) NOT NULL,`name` varchar(255) NOT NULL,`description` text NOT NULL,`base_url` varchar(1024) NOT NULL DEFAULT "",`category` varchar(64) NOT NULL DEFAULT "model",`capabilities` varchar(512) NOT NULL DEFAULT "multi_group,custom_base_url,has_models",`create_user_id` varchar(255) NOT NULL,`create_user_name` varchar(255) NOT NULL,`created_at` datetime NOT NULL,`updated_at` datetime NOT NULL,`deleted_at` datetime,PRIMARY KEY (`id`));

CREATE TABLE IF NOT EXISTS `user_personalization_settings` (`id` integer PRIMARY KEY AUTOINCREMENT,`user_id` varchar(255) NOT NULL,`enabled` numeric NOT NULL DEFAULT true,`updated_by` varchar(255) NOT NULL DEFAULT "",`updated_by_name` varchar(255) NOT NULL DEFAULT "",`created_at` datetime NOT NULL,`updated_at` datetime NOT NULL);

CREATE TABLE IF NOT EXISTS `user_plugin_settings` (`user_id` varchar(255),`plugin_ref` varchar(512),`enabled` numeric NOT NULL DEFAULT false,`updated_at` datetime NOT NULL,PRIMARY KEY (`user_id`,`plugin_ref`));

CREATE TABLE IF NOT EXISTS `user_schedules` (`id` varchar(36),`user_id` varchar(255) NOT NULL,`name` varchar(128) NOT NULL DEFAULT "",`remark` text NOT NULL DEFAULT "",`cron_expr` varchar(64) NOT NULL,`timezone` varchar(64) NOT NULL DEFAULT "Asia/Shanghai",`prompt_template` text NOT NULL,`kb_ids` text NOT NULL DEFAULT "[]",`file_ids` text NOT NULL DEFAULT "[]",`group_id` varchar(36),`group_position` integer NOT NULL DEFAULT 0,`definition_version` integer NOT NULL DEFAULT 1,`enabled` numeric NOT NULL DEFAULT true,`run_count` integer NOT NULL DEFAULT 0,`last_run_at` datetime,`next_run_at` datetime NOT NULL,`created_at` datetime NOT NULL,PRIMARY KEY (`id`));

CREATE TABLE IF NOT EXISTS `user_selected_models` (`id` integer PRIMARY KEY AUTOINCREMENT,`user_id` varchar(255) NOT NULL,`user_name` varchar(255) NOT NULL DEFAULT "",`model_type` varchar(64) NOT NULL,`user_model_provider_group_model_id` varchar(64) NOT NULL,`share` boolean NOT NULL DEFAULT false,`created_at` datetime NOT NULL,`updated_at` datetime NOT NULL);

CREATE TABLE IF NOT EXISTS `user_selected_providers` (`id` integer PRIMARY KEY AUTOINCREMENT,`user_id` varchar(255) NOT NULL,`user_name` varchar(255) NOT NULL DEFAULT "",`category` varchar(64) NOT NULL,`user_model_provider_group_id` varchar(64) NOT NULL,`share` boolean NOT NULL DEFAULT false,`created_at` datetime NOT NULL,`updated_at` datetime NOT NULL);

CREATE TABLE IF NOT EXISTS `user_ui_preferences` (`user_id` varchar(255),`chat_preference_notice_dismissed` numeric NOT NULL DEFAULT false,`developer_mode_active` numeric NOT NULL DEFAULT false,`accepted_user_agreement_version` varchar(64) NOT NULL DEFAULT "",`created_at` datetime NOT NULL,`updated_at` datetime NOT NULL,PRIMARY KEY (`user_id`));

CREATE TABLE IF NOT EXISTS `word_group_conflicts` (`id` varchar(64),`reason` text NOT NULL DEFAULT "",`word` text NOT NULL DEFAULT "",`description` text NOT NULL DEFAULT "",`group_ids` text NOT NULL DEFAULT "[]",`create_user_id` varchar(255) NOT NULL,`message_ids` text NOT NULL DEFAULT "[]",`created_at` datetime NOT NULL,`updated_at` datetime NOT NULL,`deleted_at` datetime,PRIMARY KEY (`id`));

CREATE TABLE IF NOT EXISTS `words` (`id` varchar(64),`word` varchar(512) NOT NULL,`word_kind` varchar(32) NOT NULL DEFAULT "term",`group_id` varchar(64) NOT NULL,`description` varchar(512) NOT NULL DEFAULT "",`source` varchar(32) NOT NULL DEFAULT "user",`reference_info` text NOT NULL DEFAULT "",`locked` boolean NOT NULL DEFAULT false,`create_user_id` varchar(255) NOT NULL,`create_user_name` varchar(255) NOT NULL,`created_at` datetime NOT NULL,`updated_at` datetime NOT NULL,`deleted_at` datetime,PRIMARY KEY (`id`));

CREATE INDEX IF NOT EXISTS `idx_acl_resource` ON `acl_rows`(`resource_type`,`resource_id`);

CREATE INDEX IF NOT EXISTS `idx_acl_visibility_resource_id` ON `acl_visibility`(`resource_id`);

CREATE INDEX IF NOT EXISTS `idx_agent_thread_records_round_stream_id` ON `agent_thread_records`(`round_id`,`stream_kind`);

CREATE INDEX IF NOT EXISTS `idx_agent_thread_records_task_id` ON `agent_thread_records`(`task_id`);

CREATE INDEX IF NOT EXISTS `idx_agent_thread_records_thread_round_id` ON `agent_thread_records`(`thread_id`,`round_id`);

CREATE INDEX IF NOT EXISTS `idx_agent_thread_records_thread_step_stream_id` ON `agent_thread_records`(`thread_id`,`step_id`,`stream_kind`,`id`);

CREATE INDEX IF NOT EXISTS `idx_agent_thread_records_thread_stream_id` ON `agent_thread_records`(`thread_id`,`stream_kind`);

CREATE INDEX IF NOT EXISTS `idx_agent_thread_rounds_task_id` ON `agent_thread_rounds`(`task_id`);

CREATE INDEX IF NOT EXISTS `idx_agent_thread_rounds_thread_id` ON `agent_thread_rounds`(`thread_id`);

CREATE INDEX IF NOT EXISTS `idx_agent_thread_rounds_thread_request_hash` ON `agent_thread_rounds`(`thread_id`,`request_hash`);

CREATE INDEX IF NOT EXISTS `idx_agent_thread_steps_stage` ON `agent_thread_steps`(`stage`);

CREATE INDEX IF NOT EXISTS `idx_agent_thread_steps_status` ON `agent_thread_steps`(`status`);

CREATE INDEX IF NOT EXISTS `idx_agent_thread_steps_thread_active` ON `agent_thread_steps`(`thread_id`,`active`,`updated_at`);

CREATE INDEX IF NOT EXISTS `idx_agent_thread_steps_thread_order` ON `agent_thread_steps`(`thread_id`,`order_index`,`step_id`);

CREATE INDEX IF NOT EXISTS `idx_agent_threads_current_task_id` ON `agent_threads`(`current_task_id`);

CREATE INDEX IF NOT EXISTS `idx_agent_user_active_threads_status_lease` ON `agent_user_active_threads`(`status`,`lease_until`);

CREATE INDEX IF NOT EXISTS `idx_agent_user_active_threads_thread_id` ON `agent_user_active_threads`(`thread_id`);

CREATE INDEX IF NOT EXISTS `idx_async_jobs_idempotency_key` ON `async_jobs`(`idempotency_key`);

CREATE INDEX IF NOT EXISTS `idx_async_jobs_lock_until` ON `async_jobs`(`lock_until`);

CREATE INDEX IF NOT EXISTS `idx_async_jobs_resource` ON `async_jobs`(`resource_type`,`resource_id`);

CREATE INDEX IF NOT EXISTS `idx_async_jobs_status_next` ON `async_jobs`(`status`,`next_run_at`);

CREATE INDEX IF NOT EXISTS `idx_async_jobs_type_status` ON `async_jobs`(`job_type`);

CREATE INDEX IF NOT EXISTS `idx_automation_groups_user_id` ON `automation_groups`(`user_id`);

CREATE INDEX IF NOT EXISTS `idx_chat_histories_conversation_id` ON `chat_histories`(`conversation_id`);

CREATE INDEX IF NOT EXISTS `idx_conversation_artifacts_history_id` ON `conversation_artifacts`(`history_id`);

CREATE INDEX IF NOT EXISTS `idx_conversation_artifacts_owner_conversation_created` ON `conversation_artifacts`(`create_user_id`,`conversation_id`,`created_at`);

CREATE INDEX IF NOT EXISTS `idx_conversation_idle_events_due` ON `conversation_idle_events`(`status`,`due_at`);

CREATE INDEX IF NOT EXISTS `idx_conversation_idle_events_due_at` ON `conversation_idle_events`(`due_at`);

CREATE INDEX IF NOT EXISTS `idx_conversation_idle_events_session_id` ON `conversation_idle_events`(`session_id`);

CREATE INDEX IF NOT EXISTS `idx_conversation_idle_events_session_waiting` ON `conversation_idle_events`(`session_id`,`status`,`due_at` desc);

CREATE INDEX IF NOT EXISTS `idx_conversation_idle_events_status` ON `conversation_idle_events`(`status`);

CREATE INDEX IF NOT EXISTS `idx_conversation_idle_events_user_id` ON `conversation_idle_events`(`user_id`);

CREATE INDEX IF NOT EXISTS `idx_datasets_kb_id` ON `datasets`(`kb_id`);

CREATE INDEX IF NOT EXISTS `idx_documents_dataset_id` ON `documents`(`dataset_id`);

CREATE INDEX IF NOT EXISTS `idx_documents_lazyllm_doc_id` ON `documents`(`lazyllm_doc_id`);

CREATE INDEX IF NOT EXISTS `idx_documents_p_id` ON `documents`(`p_id`);

CREATE INDEX IF NOT EXISTS `idx_eval_set_import_previews_expires_at` ON `eval_set_import_previews`(`expires_at`);

CREATE INDEX IF NOT EXISTS `idx_eval_set_import_previews_status` ON `eval_set_import_previews`(`status`);

CREATE INDEX IF NOT EXISTS `idx_eval_set_items_set_created` ON `eval_set_items`(`shard_id`,`eval_set_id`,`created_at`);

CREATE INDEX IF NOT EXISTS `idx_eval_set_items_set_source` ON `eval_set_items`(`shard_id`,`eval_set_id`,`source`);

CREATE INDEX IF NOT EXISTS `idx_eval_set_items_set_type` ON `eval_set_items`(`shard_id`,`eval_set_id`,`question_type`);

CREATE INDEX IF NOT EXISTS `idx_eval_set_items_set_updated` ON `eval_set_items`(`shard_id`,`eval_set_id`,`updated_at`);

CREATE INDEX IF NOT EXISTS `idx_eval_set_shards_status` ON `eval_set_shards`(`status`);

CREATE INDEX IF NOT EXISTS `idx_eval_sets_group_id` ON `eval_sets`(`group_id`);

CREATE INDEX IF NOT EXISTS `idx_eval_sets_owner_id` ON `eval_sets`(`owner_id`);

CREATE INDEX IF NOT EXISTS `idx_eval_sets_shard_id` ON `eval_sets`(`shard_id`);

CREATE INDEX IF NOT EXISTS `idx_eval_sets_status` ON `eval_sets`(`status`);

CREATE INDEX IF NOT EXISTS `idx_mcp_tools_server` ON `mcp_server_tools`(`mcp_server_id`,`deleted_at`);

CREATE INDEX IF NOT EXISTS `idx_multi_answers_chat_histories_conversation_id` ON `multi_answers_chat_histories`(`conversation_id`);

CREATE INDEX IF NOT EXISTS `idx_personal_resource_drafts_blob` ON `personal_resource_drafts`(`blob_hash`);

CREATE INDEX IF NOT EXISTS `idx_personal_resource_review_batches_session_created` ON `personal_resource_review_action_batches`(`session_id`,`created_at`);

CREATE INDEX IF NOT EXISTS `idx_personal_resource_review_items_batch` ON `personal_resource_review_action_items`(`batch_id`);

CREATE INDEX IF NOT EXISTS `idx_personal_resource_review_sessions_resource_status` ON `personal_resource_review_sessions`(`resource_id`,`status`);

CREATE INDEX IF NOT EXISTS `idx_personal_resource_revisions_blob` ON `personal_resource_revisions`(`blob_hash`);

CREATE INDEX IF NOT EXISTS `idx_personal_resource_revisions_created` ON `personal_resource_revisions`(`resource_id`,`created_at`);

CREATE INDEX IF NOT EXISTS `idx_plugin_attempt_input_bindings_attempt_id` ON `plugin_attempt_input_bindings`(`attempt_id`);

CREATE INDEX IF NOT EXISTS `idx_plugin_attempt_input_bindings_material_revision_id` ON `plugin_attempt_input_bindings`(`material_revision_id`);

CREATE INDEX IF NOT EXISTS `idx_plugin_attempt_input_bindings_session_id` ON `plugin_attempt_input_bindings`(`session_id`);

CREATE INDEX IF NOT EXISTS `idx_plugin_drafts_created_by` ON `plugin_drafts`(`created_by`);

CREATE UNIQUE INDEX IF NOT EXISTS `idx_plugin_drafts_user_plugin_id` ON `plugin_drafts`(`created_by`,`plugin_id`) WHERE plugin_id != '';

CREATE INDEX IF NOT EXISTS `idx_plugin_revisions_resource` ON `plugin_revisions`(`plugin_resource_id`);

CREATE INDEX IF NOT EXISTS `idx_plugin_route_decisions_session_id` ON `plugin_route_decisions`(`session_id`);

CREATE INDEX IF NOT EXISTS `idx_plugin_run_outbox_status` ON `plugin_run_outbox`(`status`);

CREATE INDEX IF NOT EXISTS `idx_plugin_transition_commands_session_id` ON `plugin_transition_commands`(`session_id`);

CREATE INDEX IF NOT EXISTS `idx_plugins_owner` ON `plugins`(`owner_user_id`,`status`);

CREATE UNIQUE INDEX IF NOT EXISTS `idx_plugins_plugin_ref` ON `plugins`(`plugin_ref`);

CREATE UNIQUE INDEX IF NOT EXISTS `idx_plugins_relative_root` ON `plugins`(`relative_root`);

CREATE INDEX IF NOT EXISTS `idx_resource_session_snapshots_session_id` ON `resource_session_snapshots`(`session_id`);

CREATE INDEX IF NOT EXISTS `idx_resource_uid` ON `datasets`(`resource_uid`);

CREATE INDEX IF NOT EXISTS `idx_resource_update_tasks_pending` ON `resource_update_tasks`(`status`,`next_run_at`,`created_at`);

CREATE INDEX IF NOT EXISTS `idx_resource_update_tasks_resource_id` ON `resource_update_tasks`(`resource_id`);

CREATE INDEX IF NOT EXISTS `idx_resource_update_tasks_resource_type` ON `resource_update_tasks`(`resource_type`);

CREATE INDEX IF NOT EXISTS `idx_resource_update_tasks_result_id` ON `resource_update_tasks`(`result_id`);

CREATE INDEX IF NOT EXISTS `idx_resource_update_tasks_review_result_id` ON `resource_update_tasks`(`review_result_id`);

CREATE INDEX IF NOT EXISTS `idx_resource_update_tasks_running_lock` ON `resource_update_tasks`(`status`,`locked_until`);

CREATE INDEX IF NOT EXISTS `idx_resource_update_tasks_status` ON `resource_update_tasks`(`status`);

CREATE INDEX IF NOT EXISTS `idx_resource_update_tasks_task_type` ON `resource_update_tasks`(`task_type`);

CREATE INDEX IF NOT EXISTS `idx_resource_update_tasks_trigger_id` ON `resource_update_tasks`(`trigger_id`);

CREATE INDEX IF NOT EXISTS `idx_resource_update_tasks_trigger_type` ON `resource_update_tasks`(`trigger_type`);

CREATE INDEX IF NOT EXISTS `idx_resource_update_tasks_user_created` ON `resource_update_tasks`(`user_id`,`created_at` desc);

CREATE INDEX IF NOT EXISTS `idx_resource_update_tasks_user_id` ON `resource_update_tasks`(`user_id`);

CREATE INDEX IF NOT EXISTS `idx_schedule_dependencies_source_schedule_id` ON `schedule_dependencies`(`source_schedule_id`);

CREATE INDEX IF NOT EXISTS `idx_schedule_dependencies_target_schedule_id` ON `schedule_dependencies`(`target_schedule_id`);

CREATE INDEX IF NOT EXISTS `idx_schedule_dependencies_user_id` ON `schedule_dependencies`(`user_id`);

CREATE INDEX IF NOT EXISTS `idx_skill_draft_review_batches_session_created` ON `skill_draft_review_action_batches`(`review_session_id`,`created_at`);

CREATE INDEX IF NOT EXISTS `idx_skill_draft_review_items_batch` ON `skill_draft_review_action_items`(`batch_id`);

CREATE INDEX IF NOT EXISTS `idx_skill_draft_review_items_session_hunk` ON `skill_draft_review_action_items`(`review_session_id`,`path`,`hunk_id`);

CREATE INDEX IF NOT EXISTS `idx_skill_draft_review_sessions_skill_status` ON `skill_draft_review_sessions`(`skill_id`,`status`,`updated_at`);

CREATE INDEX IF NOT EXISTS `idx_skill_review_scheduler_state_scan` ON `skill_review_scheduler_state`(`locked_until`,`next_run_at`,`last_quantity_check_at`);

CREATE INDEX IF NOT EXISTS `idx_skill_review_stats_user_request_status` ON `skill_review_stats`(`userid`,`requestid`,`status`);

CREATE INDEX IF NOT EXISTS `idx_skill_review_stats_user_status_started` ON `skill_review_stats`(`userid`,`status`,`started_at`);

CREATE INDEX IF NOT EXISTS `idx_skill_search_owner` ON `skill_search_indexes`(`owner_user_id`);

CREATE INDEX IF NOT EXISTS `idx_skill_share_items_source_skill` ON `skill_share_items`(`source_skill_id`);

CREATE INDEX IF NOT EXISTS `idx_skill_share_items_target_user` ON `skill_share_items`(`share_task_id`,`target_user_id`,`status`);

CREATE INDEX IF NOT EXISTS `idx_skill_share_tasks_source_user` ON `skill_share_tasks`(`source_user_id`);

CREATE INDEX IF NOT EXISTS `idx_task_center_tasks_group_id` ON `task_center_tasks`(`group_id`);

CREATE INDEX IF NOT EXISTS `idx_task_center_tasks_scheduled_fire_at` ON `task_center_tasks`(`scheduled_fire_at`);

CREATE INDEX IF NOT EXISTS `idx_task_run_inputs_downstream_task_id` ON `task_run_inputs`(`downstream_task_id`);

CREATE INDEX IF NOT EXISTS `idx_task_run_inputs_upstream_task_id` ON `task_run_inputs`(`upstream_task_id`);

CREATE INDEX IF NOT EXISTS `idx_task_run_outputs_conversation_id` ON `task_run_outputs`(`conversation_id`);

CREATE UNIQUE INDEX IF NOT EXISTS `idx_task_run_outputs_task_id` ON `task_run_outputs`(`task_id`);

CREATE INDEX IF NOT EXISTS `idx_tasks_algo_id` ON `tasks`(`algo_id`);

CREATE INDEX IF NOT EXISTS `idx_tasks_dataset_id` ON `tasks`(`dataset_id`);

CREATE INDEX IF NOT EXISTS `idx_tasks_doc_id` ON `tasks`(`doc_id`);

CREATE INDEX IF NOT EXISTS `idx_tasks_document_p_id` ON `tasks`(`document_pid`);

CREATE INDEX IF NOT EXISTS `idx_tasks_kb_id` ON `tasks`(`kb_id`);

CREATE INDEX IF NOT EXISTS `idx_tasks_lazyllm_task_id` ON `tasks`(`lazyllm_task_id`);

CREATE INDEX IF NOT EXISTS `idx_tasks_target_dataset_id` ON `tasks`(`target_dataset_id`);

CREATE INDEX IF NOT EXISTS `idx_tasks_task_type` ON `tasks`(`task_type`);

CREATE INDEX IF NOT EXISTS `idx_tct_user_status` ON `task_center_tasks`(`user_id`,`status`);

CREATE INDEX IF NOT EXISTS `idx_upload_sessions_dataset_id` ON `upload_sessions`(`dataset_id`);

CREATE INDEX IF NOT EXISTS `idx_upload_sessions_document_id` ON `upload_sessions`(`document_id`);

CREATE INDEX IF NOT EXISTS `idx_upload_sessions_task_id` ON `upload_sessions`(`task_id`);

CREATE INDEX IF NOT EXISTS `idx_upload_sessions_tenant_id` ON `upload_sessions`(`tenant_id`);

CREATE UNIQUE INDEX IF NOT EXISTS `idx_upload_sessions_upload_id` ON `upload_sessions`(`upload_id`);

CREATE INDEX IF NOT EXISTS `idx_upload_sessions_upload_state` ON `upload_sessions`(`upload_state`);

CREATE INDEX IF NOT EXISTS `idx_uploaded_files_dataset_id` ON `uploaded_files`(`dataset_id`);

CREATE INDEX IF NOT EXISTS `idx_uploaded_files_document_id` ON `uploaded_files`(`document_id`);

CREATE INDEX IF NOT EXISTS `idx_uploaded_files_status` ON `uploaded_files`(`status`);

CREATE INDEX IF NOT EXISTS `idx_uploaded_files_task_id` ON `uploaded_files`(`task_id`);

CREATE INDEX IF NOT EXISTS `idx_uploaded_files_tenant_id` ON `uploaded_files`(`tenant_id`);

CREATE UNIQUE INDEX IF NOT EXISTS `idx_uploaded_files_upload_file_id` ON `uploaded_files`(`upload_file_id`);

CREATE INDEX IF NOT EXISTS `idx_user_model_provider_group_models_provider` ON `user_model_provider_group_models`(`user_model_provider_id`);

CREATE INDEX IF NOT EXISTS `idx_user_model_provider_groups_parent` ON `user_model_provider_groups`(`user_model_provider_id`);

CREATE INDEX IF NOT EXISTS `idx_user_schedules_group_id` ON `user_schedules`(`group_id`);

CREATE INDEX IF NOT EXISTS `idx_word_column` ON `words`(`create_user_id`,`word`);

CREATE INDEX IF NOT EXISTS `idx_word_create_user_group_id` ON `words`(`create_user_id`,`group_id`);

CREATE INDEX IF NOT EXISTS `idx_word_group_conflict_user_updated` ON `word_group_conflicts`(`create_user_id`,`updated_at` desc);

CREATE UNIQUE INDEX IF NOT EXISTS `uk_agent_thread_records_record_key` ON `agent_thread_records`(`thread_id`,`round_id`,`stream_kind`,`record_key`);

CREATE UNIQUE INDEX IF NOT EXISTS `uk_conversation_idle_events_event_id` ON `conversation_idle_events`(`event_id`);

CREATE UNIQUE INDEX IF NOT EXISTS `uk_default_model_providers_name` ON `default_model_providers`(`name`);

CREATE UNIQUE INDEX IF NOT EXISTS `uk_default_models_provider_name` ON `default_models`(`default_model_provider_id`,`name`);

CREATE UNIQUE INDEX IF NOT EXISTS `uk_local_fs_chat_settings_user` ON `local_fs_chat_settings`(`create_user_id`);

CREATE UNIQUE INDEX IF NOT EXISTS `uk_personal_resource_revisions_no` ON `personal_resource_revisions`(`resource_id`,`revision_no`);

CREATE UNIQUE INDEX IF NOT EXISTS `uk_personal_resources_user_type` ON `personal_resources`(`user_id`,`resource_type`);

CREATE UNIQUE INDEX IF NOT EXISTS `uk_plugin_revisions_resource_no` ON `plugin_revisions`(`plugin_resource_id`,`revision_no`);

CREATE UNIQUE INDEX IF NOT EXISTS `uk_plugin_step_intent` ON `plugin_step_intents`(`session_id`,`step_id`);

CREATE UNIQUE INDEX IF NOT EXISTS `uk_prompt_user_states_user_prompt` ON `prompt_user_states`(`create_user_id`,`prompt_id`);

CREATE UNIQUE INDEX IF NOT EXISTS `uk_resource_session_snapshots` ON `resource_session_snapshots`(`session_id`,`resource_type`,`resource_key`);

CREATE UNIQUE INDEX IF NOT EXISTS `uk_skill_draft_review_batch_sequence` ON `skill_draft_review_action_batches`(`review_session_id`,`sequence`);

CREATE UNIQUE INDEX IF NOT EXISTS `uk_skill_revisions_skill_no` ON `skill_revisions`(`skill_id`,`revision_no`);

CREATE UNIQUE INDEX IF NOT EXISTS `uk_skills_owner_identity` ON `skills`(`owner_user_id`,`category`,`skill_name`);

CREATE UNIQUE INDEX IF NOT EXISTS `uk_skills_owner_relative_root` ON `skills`(`owner_user_id`,`relative_root`);

CREATE UNIQUE INDEX IF NOT EXISTS `uk_task_run_input_snapshot` ON `task_run_inputs`(`downstream_task_id`,`dependency_id`,`upstream_task_id`);

CREATE UNIQUE INDEX IF NOT EXISTS `uk_user_disabled_tools_user_tool` ON `user_disabled_tools`(`create_user_id`,`tool_name`);

CREATE UNIQUE INDEX IF NOT EXISTS `uk_user_model_provider_group_models_group_name` ON `user_model_provider_group_models`(`user_model_provider_group_id`,`name`);

CREATE UNIQUE INDEX IF NOT EXISTS `uk_user_personalization_settings_user_id` ON `user_personalization_settings`(`user_id`);

CREATE UNIQUE INDEX IF NOT EXISTS `uk_user_selected_models_user_type` ON `user_selected_models`(`user_id`,`model_type`);

CREATE UNIQUE INDEX IF NOT EXISTS `uk_user_selected_providers_user_category` ON `user_selected_providers`(`user_id`,`category`);

CREATE UNIQUE INDEX IF NOT EXISTS `ukx_create_user_id_dataset_id` ON `default_datasets`(`dataset_id`);

CREATE UNIQUE INDEX IF NOT EXISTS `uniq_resource_update_active_auto_apply_result` ON `resource_update_tasks`(`resource_type`,`review_result_id`) WHERE task_type = 'auto_apply_review' AND (status = 'pending' OR status = 'running');

CREATE UNIQUE INDEX IF NOT EXISTS `uniq_resource_update_task_trigger` ON `resource_update_tasks`(`task_type`,`resource_type`,`trigger_type`,`trigger_id`);

INSERT INTO "acl_groups" (id,name) SELECT id,name FROM "__v01_acl_groups";
INSERT INTO "acl_kbs" (id,name,owner_id,visibility) SELECT id,name,owner_id,visibility FROM "__v01_acl_kbs";
INSERT INTO "acl_rows" (id,resource_type,resource_id,grantee_type,target_id,permission,created_by,created_at,expires_at) SELECT id,resource_type,resource_id,grantee_type,target_id,permission,created_by,created_at,expires_at FROM "__v01_acl_rows";
INSERT INTO "acl_user_groups" (user_id,group_id) SELECT user_id,group_id FROM "__v01_acl_user_groups";
INSERT INTO "acl_visibility" (id,resource_id,level) SELECT id,resource_id,level FROM "__v01_acl_visibility";
INSERT INTO "agent_thread_records" (id,thread_id,round_id,task_id,stream_kind,record_key,event_name,payload_text,raw_frame,created_at,updated_at) SELECT id,thread_id,round_id,task_id,stream_kind,record_key,event_name,payload_text,raw_frame,created_at,updated_at FROM "__v01_agent_thread_records";
INSERT INTO "agent_thread_rounds" (round_id,thread_id,request_hash,task_id,status,user_message,assistant_message,request_payload,created_at,updated_at) SELECT round_id,thread_id,request_hash,task_id,status,user_message,assistant_message,request_payload,created_at,updated_at FROM "__v01_agent_thread_rounds";
INSERT INTO "agent_threads" (thread_id,current_task_id,status,thread_payload,last_message_request_hash,create_user_id,create_user_name,created_at,updated_at) SELECT thread_id,current_task_id,status,thread_payload,last_message_request_hash,create_user_id,create_user_name,created_at,updated_at FROM "__v01_agent_threads";
INSERT INTO "agent_user_active_threads" (user_id,thread_id,status,create_token,lease_until,created_at,updated_at) SELECT user_id,thread_id,status,create_token,lease_until,created_at,updated_at FROM "__v01_agent_user_active_threads";
INSERT INTO "chat_histories" (id,seq,conversation_id,raw_content,retrieval_result,content,result,feed_back,reason,expected_answer,ext,version,create_time,update_time) SELECT id,seq,conversation_id,raw_content,retrieval_result,content,result,feed_back,reason,expected_answer,ext,version,create_time,update_time FROM "__v01_chat_histories";
INSERT INTO "conversations" (id,display_name,channel_id,search_config,application_id,ext,model,models,chat_times,create_user_id,create_user_name,created_at,updated_at,deleted_at) SELECT id,display_name,channel_id,search_config,application_id,ext,model,models,chat_times,create_user_id,create_user_name,created_at,updated_at,deleted_at FROM "__v01_conversations";
INSERT INTO "datasets" (id,kb_id,display_name,desc,cover_image,resource_uid,bucket_name,oss_path,dataset_info,dataset_state,embedding_model,embedding_model_provider,share_type,shared_at,tenant_id,is_demonstrate,type,ext,create_user_id,create_user_name,created_at,updated_at,deleted_at) SELECT id,kb_id,display_name,desc,cover_image,resource_uid,bucket_name,oss_path,dataset_info,dataset_state,embedding_model,embedding_model_provider,share_type,shared_at,tenant_id,is_demonstrate,type,ext,create_user_id,create_user_name,created_at,updated_at,deleted_at FROM "__v01_datasets";
INSERT INTO "default_datasets" (id,dataset_id,dataset_name,create_user_id,create_user_name,created_at,updated_at,deleted_at) SELECT id,dataset_id,dataset_name,create_user_id,create_user_name,created_at,updated_at,deleted_at FROM "__v01_default_datasets";
INSERT INTO "default_model_providers" (id,name,description,base_url,created_at,updated_at,deleted_at) SELECT id,name,description,base_url,created_at,updated_at,deleted_at FROM "__v01_default_model_providers";
INSERT INTO "default_models" (id,default_model_provider_id,provider_name,name,model_type,created_at,updated_at,deleted_at) SELECT id,default_model_provider_id,provider_name,name,model_type,created_at,updated_at,deleted_at FROM "__v01_default_models";
INSERT INTO "documents" (id,lazyllm_doc_id,dataset_id,display_name,p_id,tags,file_id,pdf_convert_result,ext,create_user_id,create_user_name,created_at,updated_at,deleted_at) SELECT id,lazyllm_doc_id,dataset_id,display_name,p_id,tags,file_id,pdf_convert_result,ext,create_user_id,create_user_name,created_at,updated_at,deleted_at FROM "__v01_documents";
INSERT INTO "multi_answers_chat_histories" (id,seq,conversation_id,raw_content,retrieval_result,content,result,feed_back,reason,ext,endpoint,create_time,update_time) SELECT id,seq,conversation_id,raw_content,retrieval_result,content,result,feed_back,reason,ext,endpoint,create_time,update_time FROM "__v01_multi_answers_chat_histories";
INSERT INTO "multi_answers_switches" (id,status,create_user_id,create_user_name,created_at,updated_at,deleted_at) SELECT id,status,create_user_id,create_user_name,created_at,updated_at,deleted_at FROM "__v01_multi_answers_switches";
INSERT INTO "prompts" (id,name,content,create_user_id,create_user_name,created_at,updated_at,deleted_at) SELECT id,name,content,create_user_id,create_user_name,created_at,updated_at,deleted_at FROM "__v01_prompts";
INSERT INTO "resource_session_snapshots" (id,session_id,user_id,resource_type,resource_key,category,parent_skill_name,skill_name,file_ext,relative_path,snapshot_hash,created_at) SELECT id,session_id,user_id,resource_type,resource_key,category,parent_skill_name,skill_name,file_ext,relative_path,snapshot_hash,created_at FROM "__v01_resource_session_snapshots";
INSERT INTO "skill_share_items" (id,share_task_id,target_user_id,target_user_name,status,target_relative_root,accepted_at,rejected_at,target_root_skill_id,error_message,created_at,updated_at) SELECT id,share_task_id,target_user_id,target_user_name,status,target_relative_root,accepted_at,rejected_at,target_root_skill_id,error_message,created_at,updated_at FROM "__v01_skill_share_items";
INSERT INTO "skill_share_tasks" (id,source_user_id,source_user_name,source_skill_id,source_category,source_parent_skill_name,source_relative_root,message,created_at,updated_at) SELECT id,source_user_id,source_user_name,source_skill_id,source_category,source_parent_skill_name,source_relative_root,message,created_at,updated_at FROM "__v01_skill_share_tasks";
INSERT INTO "tasks" (id,lazyllm_task_id,doc_id,kb_id,algo_id,dataset_id,task_type,document_pid,target_pid,target_dataset_id,display_name,ext,create_user_id,create_user_name,created_at,updated_at,deleted_at) SELECT id,lazyllm_task_id,doc_id,kb_id,algo_id,dataset_id,task_type,document_pid,target_pid,target_dataset_id,display_name,ext,create_user_id,create_user_name,created_at,updated_at,deleted_at FROM "__v01_tasks";
INSERT INTO "upload_sessions" (id,upload_id,task_id,dataset_id,tenant_id,document_id,upload_state,ext,create_user_id,create_user_name,created_at,updated_at,deleted_at) SELECT id,upload_id,task_id,dataset_id,tenant_id,document_id,upload_state,ext,create_user_id,create_user_name,created_at,updated_at,deleted_at FROM "__v01_upload_sessions";
INSERT INTO "uploaded_files" (id,upload_file_id,dataset_id,tenant_id,task_id,document_id,status,ext,create_user_id,create_user_name,created_at,updated_at,deleted_at) SELECT id,upload_file_id,dataset_id,tenant_id,task_id,document_id,status,ext,create_user_id,create_user_name,created_at,updated_at,deleted_at FROM "__v01_uploaded_files";
INSERT INTO "user_model_provider_group_models" (id,user_model_provider_id,user_model_provider_group_id,provider_name,name,model_type,is_default,create_user_id,create_user_name,created_at,updated_at,deleted_at) SELECT id,user_model_provider_id,user_model_provider_group_id,provider_name,name,model_type,is_default,create_user_id,create_user_name,created_at,updated_at,deleted_at FROM "__v01_user_model_provider_group_models";
INSERT INTO "user_model_provider_groups" (id,user_model_provider_id,name,base_url,api_key,is_verified,create_user_id,create_user_name,created_at,updated_at,deleted_at) SELECT id,user_model_provider_id,name,base_url,api_key,is_verified,create_user_id,create_user_name,created_at,updated_at,deleted_at FROM "__v01_user_model_provider_groups";
INSERT INTO "user_model_providers" (id,default_model_provider_id,name,description,base_url,create_user_id,create_user_name,created_at,updated_at,deleted_at) SELECT id,default_model_provider_id,name,description,base_url,create_user_id,create_user_name,created_at,updated_at,deleted_at FROM "__v01_user_model_providers";
INSERT INTO "user_personalization_settings" (id,user_id,enabled,updated_by,updated_by_name,created_at,updated_at) SELECT id,user_id,enabled,updated_by,updated_by_name,created_at,updated_at FROM "__v01_user_personalization_settings";
INSERT INTO "user_selected_models" (id,user_id,user_name,model_type,user_model_provider_group_model_id,share,created_at,updated_at) SELECT id,user_id,user_name,model_type,user_model_provider_group_model_id,share,created_at,updated_at FROM "__v01_user_selected_models";
INSERT INTO "word_group_conflicts" (id,reason,word,description,group_ids,create_user_id,message_ids,created_at,updated_at,deleted_at) SELECT id,reason,word,description,group_ids,create_user_id,message_ids,created_at,updated_at,deleted_at FROM "__v01_word_group_conflicts";
INSERT INTO "words" (id,word,word_kind,group_id,description,source,reference_info,locked,create_user_id,create_user_name,created_at,updated_at,deleted_at) SELECT id,word,word_kind,group_id,description,source,reference_info,locked,create_user_id,create_user_name,created_at,updated_at,deleted_at FROM "__v01_words";
DROP TABLE "__v01_acl_groups";
DROP TABLE "__v01_acl_kbs";
DROP TABLE "__v01_acl_rows";
DROP TABLE "__v01_acl_user_groups";
DROP TABLE "__v01_acl_visibility";
DROP TABLE "__v01_agent_thread_records";
DROP TABLE "__v01_agent_thread_rounds";
DROP TABLE "__v01_agent_threads";
DROP TABLE "__v01_agent_user_active_threads";
DROP TABLE "__v01_chat_histories";
DROP TABLE "__v01_conversations";
DROP TABLE "__v01_datasets";
DROP TABLE "__v01_default_datasets";
DROP TABLE "__v01_default_model_providers";
DROP TABLE "__v01_default_models";
DROP TABLE "__v01_default_prompts";
DROP TABLE "__v01_documents";
DROP TABLE "__v01_multi_answers_chat_histories";
DROP TABLE "__v01_multi_answers_switches";
DROP TABLE "__v01_prompts";
DROP TABLE "__v01_resource_session_snapshots";
DROP TABLE "__v01_resource_suggestions";
DROP TABLE "__v01_skill_resources";
DROP TABLE "__v01_skill_share_items";
DROP TABLE "__v01_skill_share_tasks";
DROP TABLE "__v01_system_memories";
DROP TABLE "__v01_system_user_preferences";
DROP TABLE "__v01_tasks";
DROP TABLE "__v01_upload_sessions";
DROP TABLE "__v01_uploaded_files";
DROP TABLE "__v01_user_model_provider_group_models";
DROP TABLE "__v01_user_model_provider_groups";
DROP TABLE "__v01_user_model_providers";
DROP TABLE "__v01_user_personalization_settings";
DROP TABLE "__v01_user_selected_models";
DROP TABLE "__v01_word_group_conflicts";
DROP TABLE "__v01_words";

UPDATE default_models SET model_type = CASE model_type
  WHEN 'VLM' THEN 'vlm' WHEN 'embedding' THEN 'embed' WHEN 'embed_main' THEN 'embed'
  WHEN 'multimodal_embedding' THEN 'cross_modal_embed' WHEN 'embed_image' THEN 'cross_modal_embed'
  WHEN 'reranker' THEN 'rerank' ELSE model_type END
WHERE model_type IN ('VLM','embedding','embed_main','multimodal_embedding','embed_image','reranker');
UPDATE user_model_provider_group_models SET model_type = CASE model_type
  WHEN 'VLM' THEN 'vlm' WHEN 'embedding' THEN 'embed' WHEN 'embed_main' THEN 'embed'
  WHEN 'multimodal_embedding' THEN 'cross_modal_embed' WHEN 'embed_image' THEN 'cross_modal_embed'
  WHEN 'reranker' THEN 'rerank' ELSE model_type END
WHERE model_type IN ('VLM','embedding','embed_main','multimodal_embedding','embed_image','reranker');
UPDATE user_selected_models SET model_type = CASE model_type
  WHEN 'llm-chat' THEN 'llm' WHEN 'llm-evo' THEN 'evo_llm' WHEN 'llm2' THEN 'evo_llm'
  WHEN 'VLM' THEN 'vlm' WHEN 'embedding' THEN 'embed_main'
  WHEN 'multimodal_embedding' THEN 'embed_image' WHEN 'rerank' THEN 'reranker'
  ELSE model_type END
WHERE model_type IN ('llm-chat','llm-evo','llm2','VLM','embedding','multimodal_embedding','rerank');
INSERT OR IGNORE INTO eval_set_shards (
  id,status,row_limit,row_open_threshold,size_limit_bytes,size_open_threshold_bytes,
  actual_rows,estimated_bytes,created_at,updated_at
) VALUES ('eval_shard_0001','open',200000,120000,8589934592,5368709120,0,0,CURRENT_TIMESTAMP,CURRENT_TIMESTAMP);
