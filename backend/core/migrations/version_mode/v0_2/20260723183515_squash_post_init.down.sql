-- 20260723183515_squash_post_init
-- +migrate Down
-- +migrate Dialect postgres

-- Reverse the flattened net migration back to the unchanged init schema.
-- Data from tables intentionally dropped by the historical migrations cannot be restored.

-- Reverse data transformations on tables shared with init.
UPDATE public.default_models
SET model_type = CASE model_type
    WHEN 'vlm' THEN 'VLM'
    WHEN 'embed' THEN 'embedding'
    WHEN 'cross_modal_embed' THEN 'multimodal_embedding'
    WHEN 'reranker' THEN 'rerank'
    ELSE model_type
END
WHERE model_type IN ('vlm', 'embed', 'cross_modal_embed', 'reranker');

UPDATE public.user_model_provider_group_models
SET model_type = CASE model_type
    WHEN 'vlm' THEN 'VLM'
    WHEN 'embed' THEN 'embedding'
    WHEN 'cross_modal_embed' THEN 'multimodal_embedding'
    WHEN 'reranker' THEN 'rerank'
    ELSE model_type
END
WHERE model_type IN ('vlm', 'embed', 'cross_modal_embed', 'reranker');

UPDATE public.user_selected_models
SET model_type = CASE model_type
    WHEN 'llm' THEN 'llm-chat'
    WHEN 'evo_llm' THEN 'llm-evo'
    WHEN 'vlm' THEN 'VLM'
    WHEN 'embed_main' THEN 'embedding'
    WHEN 'embed_image' THEN 'multimodal_embedding'
    WHEN 'reranker' THEN 'rerank'
    ELSE model_type
END
WHERE model_type IN ('llm', 'evo_llm', 'vlm', 'embed_main', 'embed_image', 'reranker');

-- Remove every object introduced after init.
DROP TABLE IF EXISTS public.agent_thread_steps CASCADE;
DROP TABLE IF EXISTS public.async_jobs CASCADE;
DROP TABLE IF EXISTS public.automation_groups CASCADE;
DROP TABLE IF EXISTS public.conversation_artifacts CASCADE;
DROP TABLE IF EXISTS public.conversation_idle_events CASCADE;
DROP TABLE IF EXISTS public.eval_set_import_previews CASCADE;
DROP TABLE IF EXISTS public.eval_set_items CASCADE;
DROP TABLE IF EXISTS public.eval_set_items_p_eval_shard_0001 CASCADE;
DROP TABLE IF EXISTS public.eval_set_shards CASCADE;
DROP TABLE IF EXISTS public.eval_sets CASCADE;
DROP TABLE IF EXISTS public.external_database_connections CASCADE;
DROP TABLE IF EXISTS public.local_fs_chat_settings CASCADE;
DROP TABLE IF EXISTS public.mcp_server_tools CASCADE;
DROP TABLE IF EXISTS public.mcp_servers CASCADE;
DROP TABLE IF EXISTS public.memory_review CASCADE;
DROP TABLE IF EXISTS public.personal_resource_blobs CASCADE;
DROP TABLE IF EXISTS public.personal_resource_drafts CASCADE;
DROP TABLE IF EXISTS public.personal_resource_review_action_batches CASCADE;
DROP TABLE IF EXISTS public.personal_resource_review_action_items CASCADE;
DROP TABLE IF EXISTS public.personal_resource_review_sessions CASCADE;
DROP TABLE IF EXISTS public.personal_resource_revisions CASCADE;
DROP TABLE IF EXISTS public.personal_resources CASCADE;
DROP TABLE IF EXISTS public.plugin_attempt_input_bindings CASCADE;
DROP TABLE IF EXISTS public.plugin_blobs CASCADE;
DROP TABLE IF EXISTS public.plugin_drafts CASCADE;
DROP TABLE IF EXISTS public.plugin_generation_analyses CASCADE;
DROP TABLE IF EXISTS public.plugin_human_artifacts CASCADE;
DROP TABLE IF EXISTS public.plugin_repair_runs CASCADE;
DROP TABLE IF EXISTS public.plugin_revision_entries CASCADE;
DROP TABLE IF EXISTS public.plugin_revisions CASCADE;
DROP TABLE IF EXISTS public.plugin_route_decisions CASCADE;
DROP TABLE IF EXISTS public.plugin_run_outbox CASCADE;
DROP TABLE IF EXISTS public.plugin_session_steps CASCADE;
DROP TABLE IF EXISTS public.plugin_sessions CASCADE;
DROP TABLE IF EXISTS public.plugin_slot_order CASCADE;
DROP TABLE IF EXISTS public.plugin_slot_revisions CASCADE;
DROP TABLE IF EXISTS public.plugin_step_intents CASCADE;
DROP TABLE IF EXISTS public.plugin_transition_commands CASCADE;
DROP TABLE IF EXISTS public.plugins CASCADE;
DROP TABLE IF EXISTS public.prompt_categories CASCADE;
DROP TABLE IF EXISTS public.prompt_user_states CASCADE;
DROP TABLE IF EXISTS public.resource_update_tasks CASCADE;
DROP TABLE IF EXISTS public.schedule_dependencies CASCADE;
DROP TABLE IF EXISTS public.skill_blobs CASCADE;
DROP TABLE IF EXISTS public.skill_draft_entries CASCADE;
DROP TABLE IF EXISTS public.skill_draft_review_action_batches CASCADE;
DROP TABLE IF EXISTS public.skill_draft_review_action_items CASCADE;
DROP TABLE IF EXISTS public.skill_draft_review_sessions CASCADE;
DROP TABLE IF EXISTS public.skill_drafts CASCADE;
DROP TABLE IF EXISTS public.skill_market_installs CASCADE;
DROP TABLE IF EXISTS public.skill_market_items CASCADE;
DROP TABLE IF EXISTS public.skill_review_results CASCADE;
DROP TABLE IF EXISTS public.skill_review_run_stats CASCADE;
DROP TABLE IF EXISTS public.skill_review_scheduler_state CASCADE;
DROP TABLE IF EXISTS public.skill_review_stats CASCADE;
DROP TABLE IF EXISTS public.skill_revision_entries CASCADE;
DROP TABLE IF EXISTS public.skill_revisions CASCADE;
DROP TABLE IF EXISTS public.skill_search_indexes CASCADE;
DROP TABLE IF EXISTS public.skills CASCADE;
DROP TABLE IF EXISTS public.sub_agent_artifacts CASCADE;
DROP TABLE IF EXISTS public.sub_agent_steps CASCADE;
DROP TABLE IF EXISTS public.sub_agent_tasks CASCADE;
DROP TABLE IF EXISTS public.task_center_tasks CASCADE;
DROP TABLE IF EXISTS public.task_run_inputs CASCADE;
DROP TABLE IF EXISTS public.task_run_outputs CASCADE;
DROP TABLE IF EXISTS public.user_chat_settings CASCADE;
DROP TABLE IF EXISTS public.user_disabled_tools CASCADE;
DROP TABLE IF EXISTS public.user_plugin_settings CASCADE;
DROP TABLE IF EXISTS public.user_schedules CASCADE;
DROP TABLE IF EXISTS public.user_selected_providers CASCADE;
DROP TABLE IF EXISTS public.user_ui_preferences CASCADE;
DROP SEQUENCE IF EXISTS public.local_fs_chat_settings_id_seq;
DROP SEQUENCE IF EXISTS public.user_disabled_tools_id_seq;
DROP SEQUENCE IF EXISTS public.user_selected_providers_id_seq;

-- Reverse net changes to tables that already exist in the init migration.

-- Drop changed indexes and constraints before changing their columns.
DROP INDEX "public"."idx_agent_thread_records_thread_step_stream_id";
DROP INDEX "public"."idx_chat_histories_conversation_create_time";
ALTER TABLE "public"."chat_histories" DROP CONSTRAINT "chk_chat_histories_tool_call_turns_non_negative";
DROP INDEX "public"."idx_conversations_is_task_conv";
DROP INDEX "public"."idx_conversations_user_not_deleted";
ALTER TABLE "public"."multi_answers_chat_histories" DROP CONSTRAINT "chk_multi_answers_chat_histories_tool_call_turns_non_negative";
DROP INDEX "public"."idx_skill_share_items_source_skill";
DROP INDEX "public"."idx_uploaded_files_reusable_hash";
DROP INDEX "public"."uk_user_selected_models_shared_model";

-- Apply each column's net change once.
ALTER TABLE "public"."agent_thread_records" DROP COLUMN "step_id";
ALTER TABLE "public"."chat_histories" DROP COLUMN "tool_call_turns";
ALTER TABLE "public"."chat_histories" DROP COLUMN "thinking_duration_s";
ALTER TABLE "public"."conversations" DROP COLUMN "enable_plugin";
ALTER TABLE "public"."conversations" DROP COLUMN "plugin_mode";
ALTER TABLE "public"."conversations" DROP COLUMN "enable_subagent";
ALTER TABLE "public"."conversations" DROP COLUMN "is_task_conv";
ALTER TABLE "public"."default_model_providers" DROP COLUMN "category";
ALTER TABLE "public"."default_model_providers" DROP COLUMN "capabilities";
ALTER TABLE "public"."default_model_providers" DROP COLUMN "description_i18n";
ALTER TABLE "public"."default_models" DROP COLUMN "max_input_tokens";
ALTER TABLE "public"."default_models" ADD COLUMN "base_url" character varying(1024) DEFAULT ''::character varying NOT NULL;
ALTER TABLE "public"."documents" DROP COLUMN "document_type";
ALTER TABLE "public"."multi_answers_chat_histories" DROP COLUMN "tool_call_turns";
ALTER TABLE "public"."multi_answers_chat_histories" DROP COLUMN "thinking_duration_s";
ALTER TABLE "public"."prompts" DROP COLUMN "category";
ALTER TABLE "public"."skill_share_items" DROP COLUMN "source_skill_id";
ALTER TABLE "public"."uploaded_files" DROP COLUMN "content_hash";
ALTER TABLE "public"."user_model_provider_group_models" DROP COLUMN "max_input_tokens";
ALTER TABLE "public"."user_model_provider_group_models" ADD COLUMN "base_url" character varying(1024) DEFAULT ''::character varying NOT NULL;
ALTER TABLE "public"."user_model_provider_groups" DROP COLUMN "credential_version";
ALTER TABLE "public"."user_model_provider_groups" DROP COLUMN "api_key_ciphertext";
ALTER TABLE "public"."user_model_providers" DROP COLUMN "category";
ALTER TABLE "public"."user_model_providers" DROP COLUMN "capabilities";
ALTER TABLE "public"."user_selected_models" DROP COLUMN "share";

-- Add final constraints and indexes after all columns are ready.

-- Restore objects that exist in init but were removed by later migrations.

--
-- Name: default_prompts; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.default_prompts (
    id bigint NOT NULL,
    prompt_id character varying(64) NOT NULL,
    prompt_name character varying(255) NOT NULL,
    create_user_id character varying(255) NOT NULL,
    create_user_name character varying(255) NOT NULL,
    created_at timestamp with time zone NOT NULL,
    updated_at timestamp with time zone NOT NULL,
    deleted_at timestamp with time zone
);


--
-- Name: default_prompts_id_seq; Type: SEQUENCE; Schema: public; Owner: -
--

CREATE SEQUENCE public.default_prompts_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;


--
-- Name: default_prompts_id_seq; Type: SEQUENCE OWNED BY; Schema: public; Owner: -
--

ALTER SEQUENCE public.default_prompts_id_seq OWNED BY public.default_prompts.id;


--
-- Name: resource_suggestions; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.resource_suggestions (
    id character varying(36) NOT NULL,
    user_id character varying(255) DEFAULT ''::character varying NOT NULL,
    resource_type character varying(32) NOT NULL,
    resource_key character varying(1024) DEFAULT ''::character varying NOT NULL,
    category character varying(128) DEFAULT ''::character varying NOT NULL,
    parent_skill_name character varying(255) DEFAULT ''::character varying NOT NULL,
    skill_name character varying(255) DEFAULT ''::character varying NOT NULL,
    file_ext character varying(32) DEFAULT ''::character varying NOT NULL,
    relative_path character varying(1024) DEFAULT ''::character varying NOT NULL,
    action character varying(32) NOT NULL,
    session_id character varying(128) NOT NULL,
    snapshot_hash character varying(64) DEFAULT ''::character varying NOT NULL,
    title character varying(255) DEFAULT ''::character varying NOT NULL,
    content text,
    reason text,
    full_content text,
    status character varying(32) NOT NULL,
    invalid_reason text,
    reviewer_id character varying(255) DEFAULT ''::character varying NOT NULL,
    reviewer_name character varying(255) DEFAULT ''::character varying NOT NULL,
    reviewed_at timestamp with time zone,
    ext json,
    created_at timestamp with time zone NOT NULL,
    updated_at timestamp with time zone NOT NULL
);


--
-- Name: skill_resources; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.skill_resources (
    id character varying(36) NOT NULL,
    owner_user_id character varying(255) NOT NULL,
    owner_user_name character varying(255) DEFAULT ''::character varying NOT NULL,
    category character varying(128) NOT NULL,
    parent_skill_name character varying(255) DEFAULT ''::character varying NOT NULL,
    skill_name character varying(255) DEFAULT ''::character varying NOT NULL,
    node_type character varying(32) NOT NULL,
    description text,
    tags json,
    file_ext character varying(32) DEFAULT 'md'::character varying NOT NULL,
    relative_path character varying(1024) NOT NULL,
    storage_path text DEFAULT ''::text NOT NULL,
    content_hash character varying(64) DEFAULT ''::character varying NOT NULL,
    version bigint DEFAULT 1 NOT NULL,
    draft_source_version bigint DEFAULT 0 NOT NULL,
    draft_status character varying(32) DEFAULT ''::character varying NOT NULL,
    draft_updated_at timestamp with time zone,
    auto_evo boolean DEFAULT false NOT NULL,
    is_enabled boolean DEFAULT true NOT NULL,
    update_status character varying(32) DEFAULT 'up_to_date'::character varying NOT NULL,
    ext json,
    create_user_id character varying(255) NOT NULL,
    create_user_name character varying(255) DEFAULT ''::character varying NOT NULL,
    created_at timestamp with time zone NOT NULL,
    updated_at timestamp with time zone NOT NULL,
    content text DEFAULT ''::text NOT NULL,
    content_size bigint DEFAULT 0 NOT NULL,
    mime_type character varying(128) DEFAULT 'text/plain; charset=utf-8'::character varying NOT NULL,
    draft_content text DEFAULT ''::text NOT NULL,
    auto_evo_apply_status character varying(32) DEFAULT 'idle'::character varying NOT NULL,
    auto_evo_generation integer DEFAULT 0 NOT NULL,
    auto_evo_started_at timestamp with time zone,
    auto_evo_finished_at timestamp with time zone,
    auto_evo_error text DEFAULT ''::text NOT NULL
);


--
-- Name: system_memories; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.system_memories (
    id character varying(36) NOT NULL,
    content text DEFAULT ''::text NOT NULL,
    content_hash character varying(64) DEFAULT ''::character varying NOT NULL,
    version bigint DEFAULT 1 NOT NULL,
    draft_content text,
    draft_source_version bigint DEFAULT 0 NOT NULL,
    draft_status character varying(32) DEFAULT ''::character varying NOT NULL,
    draft_updated_at timestamp with time zone,
    ext json,
    updated_by character varying(255) DEFAULT ''::character varying NOT NULL,
    updated_by_name character varying(255) DEFAULT ''::character varying NOT NULL,
    created_at timestamp with time zone NOT NULL,
    updated_at timestamp with time zone NOT NULL,
    user_id character varying(255) DEFAULT ''::character varying NOT NULL,
    auto_evo boolean DEFAULT true NOT NULL,
    auto_evo_apply_status character varying(32) DEFAULT 'idle'::character varying NOT NULL,
    auto_evo_generation integer DEFAULT 0 NOT NULL,
    auto_evo_started_at timestamp with time zone,
    auto_evo_finished_at timestamp with time zone,
    auto_evo_error text DEFAULT ''::text NOT NULL
);


--
-- Name: system_user_preferences; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.system_user_preferences (
    id character varying(36) NOT NULL,
    content text DEFAULT ''::text NOT NULL,
    agent_persona text DEFAULT ''::text NOT NULL,
    user_address text DEFAULT ''::text NOT NULL,
    response_style text DEFAULT ''::text NOT NULL,
    content_hash character varying(64) DEFAULT ''::character varying NOT NULL,
    version bigint DEFAULT 1 NOT NULL,
    draft_content text,
    draft_source_version bigint DEFAULT 0 NOT NULL,
    draft_status character varying(32) DEFAULT ''::character varying NOT NULL,
    draft_updated_at timestamp with time zone,
    ext json,
    updated_by character varying(255) DEFAULT ''::character varying NOT NULL,
    updated_by_name character varying(255) DEFAULT ''::character varying NOT NULL,
    created_at timestamp with time zone NOT NULL,
    updated_at timestamp with time zone NOT NULL,
    user_id character varying(255) DEFAULT ''::character varying NOT NULL,
    auto_evo boolean DEFAULT true NOT NULL,
    auto_evo_apply_status character varying(32) DEFAULT 'idle'::character varying NOT NULL,
    auto_evo_generation integer DEFAULT 0 NOT NULL,
    auto_evo_started_at timestamp with time zone,
    auto_evo_finished_at timestamp with time zone,
    auto_evo_error text DEFAULT ''::text NOT NULL
);


--
-- Name: default_prompts id; Type: DEFAULT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.default_prompts ALTER COLUMN id SET DEFAULT nextval('public.default_prompts_id_seq'::regclass);


--
-- Name: default_prompts default_prompts_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.default_prompts
    ADD CONSTRAINT default_prompts_pkey PRIMARY KEY (id);


--
-- Name: resource_suggestions resource_suggestions_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.resource_suggestions
    ADD CONSTRAINT resource_suggestions_pkey PRIMARY KEY (id);


--
-- Name: skill_resources skill_resources_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.skill_resources
    ADD CONSTRAINT skill_resources_pkey PRIMARY KEY (id);


--
-- Name: system_memories system_memories_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.system_memories
    ADD CONSTRAINT system_memories_pkey PRIMARY KEY (id);


--
-- Name: system_user_preferences system_user_preferences_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.system_user_preferences
    ADD CONSTRAINT system_user_preferences_pkey PRIMARY KEY (id);


--
-- Name: idx_resource_suggestions_list; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_resource_suggestions_list ON public.resource_suggestions USING btree (user_id, resource_type, status);


--
-- Name: idx_resource_suggestions_session_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_resource_suggestions_session_id ON public.resource_suggestions USING btree (session_id);


--
-- Name: idx_skill_resources_owner_node_enabled; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_skill_resources_owner_node_enabled ON public.skill_resources USING btree (owner_user_id, node_type, is_enabled, category);


--
-- Name: uk_skill_resources_owner_relative_path; Type: INDEX; Schema: public; Owner: -
--

CREATE UNIQUE INDEX uk_skill_resources_owner_relative_path ON public.skill_resources USING btree (owner_user_id, relative_path);


--
-- Name: uk_system_memories_user_id; Type: INDEX; Schema: public; Owner: -
--

CREATE UNIQUE INDEX uk_system_memories_user_id ON public.system_memories USING btree (user_id);


--
-- Name: uk_system_user_preferences_user_id; Type: INDEX; Schema: public; Owner: -
--

CREATE UNIQUE INDEX uk_system_user_preferences_user_id ON public.system_user_preferences USING btree (user_id);

-- +migrate Dialect sqlite
PRAGMA defer_foreign_keys = ON;
DROP TABLE IF EXISTS plugin_repair_runs;
DROP TABLE IF EXISTS plugin_generation_analyses;
DROP TABLE IF EXISTS skill_market_installs;

ALTER TABLE "acl_groups" RENAME TO "__v02_acl_groups";
ALTER TABLE "acl_kbs" RENAME TO "__v02_acl_kbs";
ALTER TABLE "acl_rows" RENAME TO "__v02_acl_rows";
ALTER TABLE "acl_user_groups" RENAME TO "__v02_acl_user_groups";
ALTER TABLE "acl_visibility" RENAME TO "__v02_acl_visibility";
ALTER TABLE "agent_thread_records" RENAME TO "__v02_agent_thread_records";
ALTER TABLE "agent_thread_rounds" RENAME TO "__v02_agent_thread_rounds";
ALTER TABLE "agent_thread_steps" RENAME TO "__v02_agent_thread_steps";
ALTER TABLE "agent_threads" RENAME TO "__v02_agent_threads";
ALTER TABLE "agent_user_active_threads" RENAME TO "__v02_agent_user_active_threads";
ALTER TABLE "async_jobs" RENAME TO "__v02_async_jobs";
ALTER TABLE "automation_groups" RENAME TO "__v02_automation_groups";
ALTER TABLE "chat_histories" RENAME TO "__v02_chat_histories";
ALTER TABLE "conversation_artifacts" RENAME TO "__v02_conversation_artifacts";
ALTER TABLE "conversation_idle_events" RENAME TO "__v02_conversation_idle_events";
ALTER TABLE "conversations" RENAME TO "__v02_conversations";
ALTER TABLE "datasets" RENAME TO "__v02_datasets";
ALTER TABLE "default_datasets" RENAME TO "__v02_default_datasets";
ALTER TABLE "default_model_providers" RENAME TO "__v02_default_model_providers";
ALTER TABLE "default_models" RENAME TO "__v02_default_models";
ALTER TABLE "documents" RENAME TO "__v02_documents";
ALTER TABLE "eval_set_import_previews" RENAME TO "__v02_eval_set_import_previews";
ALTER TABLE "eval_set_items" RENAME TO "__v02_eval_set_items";
ALTER TABLE "eval_set_shards" RENAME TO "__v02_eval_set_shards";
ALTER TABLE "eval_sets" RENAME TO "__v02_eval_sets";
ALTER TABLE "external_database_connections" RENAME TO "__v02_external_database_connections";
ALTER TABLE "local_fs_chat_settings" RENAME TO "__v02_local_fs_chat_settings";
ALTER TABLE "mcp_server_tools" RENAME TO "__v02_mcp_server_tools";
ALTER TABLE "mcp_servers" RENAME TO "__v02_mcp_servers";
ALTER TABLE "memory_review" RENAME TO "__v02_memory_review";
ALTER TABLE "multi_answers_chat_histories" RENAME TO "__v02_multi_answers_chat_histories";
ALTER TABLE "multi_answers_switches" RENAME TO "__v02_multi_answers_switches";
ALTER TABLE "personal_resource_blobs" RENAME TO "__v02_personal_resource_blobs";
ALTER TABLE "personal_resource_drafts" RENAME TO "__v02_personal_resource_drafts";
ALTER TABLE "personal_resource_review_action_batches" RENAME TO "__v02_personal_resource_review_action_batches";
ALTER TABLE "personal_resource_review_action_items" RENAME TO "__v02_personal_resource_review_action_items";
ALTER TABLE "personal_resource_review_sessions" RENAME TO "__v02_personal_resource_review_sessions";
ALTER TABLE "personal_resource_revisions" RENAME TO "__v02_personal_resource_revisions";
ALTER TABLE "personal_resources" RENAME TO "__v02_personal_resources";
ALTER TABLE "plugin_attempt_input_bindings" RENAME TO "__v02_plugin_attempt_input_bindings";
ALTER TABLE "plugin_blobs" RENAME TO "__v02_plugin_blobs";
ALTER TABLE "plugin_drafts" RENAME TO "__v02_plugin_drafts";
ALTER TABLE "plugin_human_artifacts" RENAME TO "__v02_plugin_human_artifacts";
ALTER TABLE "plugin_revision_entries" RENAME TO "__v02_plugin_revision_entries";
ALTER TABLE "plugin_revisions" RENAME TO "__v02_plugin_revisions";
ALTER TABLE "plugin_route_decisions" RENAME TO "__v02_plugin_route_decisions";
ALTER TABLE "plugin_run_outbox" RENAME TO "__v02_plugin_run_outbox";
ALTER TABLE "plugin_session_steps" RENAME TO "__v02_plugin_session_steps";
ALTER TABLE "plugin_sessions" RENAME TO "__v02_plugin_sessions";
ALTER TABLE "plugin_slot_order" RENAME TO "__v02_plugin_slot_order";
ALTER TABLE "plugin_slot_revisions" RENAME TO "__v02_plugin_slot_revisions";
ALTER TABLE "plugin_step_intents" RENAME TO "__v02_plugin_step_intents";
ALTER TABLE "plugin_transition_commands" RENAME TO "__v02_plugin_transition_commands";
ALTER TABLE "plugins" RENAME TO "__v02_plugins";
ALTER TABLE "prompt_categories" RENAME TO "__v02_prompt_categories";
ALTER TABLE "prompt_user_states" RENAME TO "__v02_prompt_user_states";
ALTER TABLE "prompts" RENAME TO "__v02_prompts";
ALTER TABLE "resource_session_snapshots" RENAME TO "__v02_resource_session_snapshots";
ALTER TABLE "resource_update_tasks" RENAME TO "__v02_resource_update_tasks";
ALTER TABLE "schedule_dependencies" RENAME TO "__v02_schedule_dependencies";
ALTER TABLE "skill_blobs" RENAME TO "__v02_skill_blobs";
ALTER TABLE "skill_draft_entries" RENAME TO "__v02_skill_draft_entries";
ALTER TABLE "skill_draft_review_action_batches" RENAME TO "__v02_skill_draft_review_action_batches";
ALTER TABLE "skill_draft_review_action_items" RENAME TO "__v02_skill_draft_review_action_items";
ALTER TABLE "skill_draft_review_sessions" RENAME TO "__v02_skill_draft_review_sessions";
ALTER TABLE "skill_drafts" RENAME TO "__v02_skill_drafts";
ALTER TABLE "skill_market_items" RENAME TO "__v02_skill_market_items";
ALTER TABLE "skill_review_results" RENAME TO "__v02_skill_review_results";
ALTER TABLE "skill_review_scheduler_state" RENAME TO "__v02_skill_review_scheduler_state";
ALTER TABLE "skill_review_stats" RENAME TO "__v02_skill_review_stats";
ALTER TABLE "skill_revision_entries" RENAME TO "__v02_skill_revision_entries";
ALTER TABLE "skill_revisions" RENAME TO "__v02_skill_revisions";
ALTER TABLE "skill_search_indexes" RENAME TO "__v02_skill_search_indexes";
ALTER TABLE "skill_share_items" RENAME TO "__v02_skill_share_items";
ALTER TABLE "skill_share_tasks" RENAME TO "__v02_skill_share_tasks";
ALTER TABLE "skills" RENAME TO "__v02_skills";
ALTER TABLE "sub_agent_artifacts" RENAME TO "__v02_sub_agent_artifacts";
ALTER TABLE "sub_agent_steps" RENAME TO "__v02_sub_agent_steps";
ALTER TABLE "sub_agent_tasks" RENAME TO "__v02_sub_agent_tasks";
ALTER TABLE "task_center_tasks" RENAME TO "__v02_task_center_tasks";
ALTER TABLE "task_run_inputs" RENAME TO "__v02_task_run_inputs";
ALTER TABLE "task_run_outputs" RENAME TO "__v02_task_run_outputs";
ALTER TABLE "tasks" RENAME TO "__v02_tasks";
ALTER TABLE "upload_sessions" RENAME TO "__v02_upload_sessions";
ALTER TABLE "uploaded_files" RENAME TO "__v02_uploaded_files";
ALTER TABLE "user_chat_settings" RENAME TO "__v02_user_chat_settings";
ALTER TABLE "user_disabled_tools" RENAME TO "__v02_user_disabled_tools";
ALTER TABLE "user_model_provider_group_models" RENAME TO "__v02_user_model_provider_group_models";
ALTER TABLE "user_model_provider_groups" RENAME TO "__v02_user_model_provider_groups";
ALTER TABLE "user_model_providers" RENAME TO "__v02_user_model_providers";
ALTER TABLE "user_personalization_settings" RENAME TO "__v02_user_personalization_settings";
ALTER TABLE "user_plugin_settings" RENAME TO "__v02_user_plugin_settings";
ALTER TABLE "user_schedules" RENAME TO "__v02_user_schedules";
ALTER TABLE "user_selected_models" RENAME TO "__v02_user_selected_models";
ALTER TABLE "user_selected_providers" RENAME TO "__v02_user_selected_providers";
ALTER TABLE "user_ui_preferences" RENAME TO "__v02_user_ui_preferences";
ALTER TABLE "word_group_conflicts" RENAME TO "__v02_word_group_conflicts";
ALTER TABLE "words" RENAME TO "__v02_words";
DROP INDEX IF EXISTS "idx_acl_resource";
DROP INDEX IF EXISTS "idx_acl_visibility_resource_id";
DROP INDEX IF EXISTS "idx_agent_thread_records_round_stream_id";
DROP INDEX IF EXISTS "idx_agent_thread_records_task_id";
DROP INDEX IF EXISTS "idx_agent_thread_records_thread_round_id";
DROP INDEX IF EXISTS "idx_agent_thread_records_thread_step_stream_id";
DROP INDEX IF EXISTS "idx_agent_thread_records_thread_stream_id";
DROP INDEX IF EXISTS "idx_agent_thread_rounds_task_id";
DROP INDEX IF EXISTS "idx_agent_thread_rounds_thread_id";
DROP INDEX IF EXISTS "idx_agent_thread_rounds_thread_request_hash";
DROP INDEX IF EXISTS "idx_agent_thread_steps_stage";
DROP INDEX IF EXISTS "idx_agent_thread_steps_status";
DROP INDEX IF EXISTS "idx_agent_thread_steps_thread_active";
DROP INDEX IF EXISTS "idx_agent_thread_steps_thread_order";
DROP INDEX IF EXISTS "idx_agent_threads_current_task_id";
DROP INDEX IF EXISTS "idx_agent_user_active_threads_status_lease";
DROP INDEX IF EXISTS "idx_agent_user_active_threads_thread_id";
DROP INDEX IF EXISTS "idx_async_jobs_idempotency_key";
DROP INDEX IF EXISTS "idx_async_jobs_lock_until";
DROP INDEX IF EXISTS "idx_async_jobs_resource";
DROP INDEX IF EXISTS "idx_async_jobs_status_next";
DROP INDEX IF EXISTS "idx_async_jobs_type_status";
DROP INDEX IF EXISTS "idx_automation_groups_user_id";
DROP INDEX IF EXISTS "idx_chat_histories_conversation_id";
DROP INDEX IF EXISTS "idx_conversation_artifacts_history_id";
DROP INDEX IF EXISTS "idx_conversation_artifacts_owner_conversation_created";
DROP INDEX IF EXISTS "idx_conversation_idle_events_due";
DROP INDEX IF EXISTS "idx_conversation_idle_events_due_at";
DROP INDEX IF EXISTS "idx_conversation_idle_events_session_id";
DROP INDEX IF EXISTS "idx_conversation_idle_events_session_waiting";
DROP INDEX IF EXISTS "idx_conversation_idle_events_status";
DROP INDEX IF EXISTS "idx_conversation_idle_events_user_id";
DROP INDEX IF EXISTS "idx_datasets_kb_id";
DROP INDEX IF EXISTS "idx_documents_dataset_id";
DROP INDEX IF EXISTS "idx_documents_lazyllm_doc_id";
DROP INDEX IF EXISTS "idx_documents_p_id";
DROP INDEX IF EXISTS "idx_eval_set_import_previews_expires_at";
DROP INDEX IF EXISTS "idx_eval_set_import_previews_status";
DROP INDEX IF EXISTS "idx_eval_set_items_set_created";
DROP INDEX IF EXISTS "idx_eval_set_items_set_source";
DROP INDEX IF EXISTS "idx_eval_set_items_set_type";
DROP INDEX IF EXISTS "idx_eval_set_items_set_updated";
DROP INDEX IF EXISTS "idx_eval_set_shards_status";
DROP INDEX IF EXISTS "idx_eval_sets_group_id";
DROP INDEX IF EXISTS "idx_eval_sets_owner_id";
DROP INDEX IF EXISTS "idx_eval_sets_shard_id";
DROP INDEX IF EXISTS "idx_eval_sets_status";
DROP INDEX IF EXISTS "idx_mcp_tools_server";
DROP INDEX IF EXISTS "idx_multi_answers_chat_histories_conversation_id";
DROP INDEX IF EXISTS "idx_personal_resource_drafts_blob";
DROP INDEX IF EXISTS "idx_personal_resource_review_batches_session_created";
DROP INDEX IF EXISTS "idx_personal_resource_review_items_batch";
DROP INDEX IF EXISTS "idx_personal_resource_review_sessions_resource_status";
DROP INDEX IF EXISTS "idx_personal_resource_revisions_blob";
DROP INDEX IF EXISTS "idx_personal_resource_revisions_created";
DROP INDEX IF EXISTS "idx_plugin_attempt_input_bindings_attempt_id";
DROP INDEX IF EXISTS "idx_plugin_attempt_input_bindings_material_revision_id";
DROP INDEX IF EXISTS "idx_plugin_attempt_input_bindings_session_id";
DROP INDEX IF EXISTS "idx_plugin_drafts_created_by";
DROP INDEX IF EXISTS "idx_plugin_drafts_user_plugin_id";
DROP INDEX IF EXISTS "idx_plugin_revisions_resource";
DROP INDEX IF EXISTS "idx_plugin_route_decisions_session_id";
DROP INDEX IF EXISTS "idx_plugin_run_outbox_status";
DROP INDEX IF EXISTS "idx_plugin_transition_commands_session_id";
DROP INDEX IF EXISTS "idx_plugins_owner";
DROP INDEX IF EXISTS "idx_plugins_plugin_ref";
DROP INDEX IF EXISTS "idx_plugins_relative_root";
DROP INDEX IF EXISTS "idx_resource_session_snapshots_session_id";
DROP INDEX IF EXISTS "idx_resource_uid";
DROP INDEX IF EXISTS "idx_resource_update_tasks_pending";
DROP INDEX IF EXISTS "idx_resource_update_tasks_resource_id";
DROP INDEX IF EXISTS "idx_resource_update_tasks_resource_type";
DROP INDEX IF EXISTS "idx_resource_update_tasks_result_id";
DROP INDEX IF EXISTS "idx_resource_update_tasks_review_result_id";
DROP INDEX IF EXISTS "idx_resource_update_tasks_running_lock";
DROP INDEX IF EXISTS "idx_resource_update_tasks_status";
DROP INDEX IF EXISTS "idx_resource_update_tasks_task_type";
DROP INDEX IF EXISTS "idx_resource_update_tasks_trigger_id";
DROP INDEX IF EXISTS "idx_resource_update_tasks_trigger_type";
DROP INDEX IF EXISTS "idx_resource_update_tasks_user_created";
DROP INDEX IF EXISTS "idx_resource_update_tasks_user_id";
DROP INDEX IF EXISTS "idx_schedule_dependencies_source_schedule_id";
DROP INDEX IF EXISTS "idx_schedule_dependencies_target_schedule_id";
DROP INDEX IF EXISTS "idx_schedule_dependencies_user_id";
DROP INDEX IF EXISTS "idx_skill_draft_review_batches_session_created";
DROP INDEX IF EXISTS "idx_skill_draft_review_items_batch";
DROP INDEX IF EXISTS "idx_skill_draft_review_items_session_hunk";
DROP INDEX IF EXISTS "idx_skill_draft_review_sessions_skill_status";
DROP INDEX IF EXISTS "idx_skill_review_scheduler_state_scan";
DROP INDEX IF EXISTS "idx_skill_review_stats_user_request_status";
DROP INDEX IF EXISTS "idx_skill_review_stats_user_status_started";
DROP INDEX IF EXISTS "idx_skill_search_owner";
DROP INDEX IF EXISTS "idx_skill_share_items_source_skill";
DROP INDEX IF EXISTS "idx_skill_share_items_target_user";
DROP INDEX IF EXISTS "idx_skill_share_tasks_source_user";
DROP INDEX IF EXISTS "idx_task_center_tasks_group_id";
DROP INDEX IF EXISTS "idx_task_center_tasks_scheduled_fire_at";
DROP INDEX IF EXISTS "idx_task_run_inputs_downstream_task_id";
DROP INDEX IF EXISTS "idx_task_run_inputs_upstream_task_id";
DROP INDEX IF EXISTS "idx_task_run_outputs_conversation_id";
DROP INDEX IF EXISTS "idx_task_run_outputs_task_id";
DROP INDEX IF EXISTS "idx_tasks_algo_id";
DROP INDEX IF EXISTS "idx_tasks_dataset_id";
DROP INDEX IF EXISTS "idx_tasks_doc_id";
DROP INDEX IF EXISTS "idx_tasks_document_p_id";
DROP INDEX IF EXISTS "idx_tasks_kb_id";
DROP INDEX IF EXISTS "idx_tasks_lazyllm_task_id";
DROP INDEX IF EXISTS "idx_tasks_target_dataset_id";
DROP INDEX IF EXISTS "idx_tasks_task_type";
DROP INDEX IF EXISTS "idx_tct_user_status";
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
DROP INDEX IF EXISTS "idx_user_schedules_group_id";
DROP INDEX IF EXISTS "idx_word_column";
DROP INDEX IF EXISTS "idx_word_create_user_group_id";
DROP INDEX IF EXISTS "idx_word_group_conflict_user_updated";
DROP INDEX IF EXISTS "uk_agent_thread_records_record_key";
DROP INDEX IF EXISTS "uk_conversation_idle_events_event_id";
DROP INDEX IF EXISTS "uk_default_model_providers_name";
DROP INDEX IF EXISTS "uk_default_models_provider_name";
DROP INDEX IF EXISTS "uk_local_fs_chat_settings_user";
DROP INDEX IF EXISTS "uk_personal_resource_revisions_no";
DROP INDEX IF EXISTS "uk_personal_resources_user_type";
DROP INDEX IF EXISTS "uk_plugin_revisions_resource_no";
DROP INDEX IF EXISTS "uk_plugin_step_intent";
DROP INDEX IF EXISTS "uk_prompt_user_states_user_prompt";
DROP INDEX IF EXISTS "uk_resource_session_snapshots";
DROP INDEX IF EXISTS "uk_skill_draft_review_batch_sequence";
DROP INDEX IF EXISTS "uk_skill_revisions_skill_no";
DROP INDEX IF EXISTS "uk_skills_owner_identity";
DROP INDEX IF EXISTS "uk_skills_owner_relative_root";
DROP INDEX IF EXISTS "uk_task_run_input_snapshot";
DROP INDEX IF EXISTS "uk_user_disabled_tools_user_tool";
DROP INDEX IF EXISTS "uk_user_model_provider_group_models_group_name";
DROP INDEX IF EXISTS "uk_user_personalization_settings_user_id";
DROP INDEX IF EXISTS "uk_user_selected_models_user_type";
DROP INDEX IF EXISTS "uk_user_selected_providers_user_category";
DROP INDEX IF EXISTS "ukx_create_user_id_dataset_id";
DROP INDEX IF EXISTS "uniq_resource_update_active_auto_apply_result";
DROP INDEX IF EXISTS "uniq_resource_update_task_trigger";

CREATE TABLE IF NOT EXISTS `acl_groups` (`id` varchar(255),`name` varchar(255) NOT NULL DEFAULT "",PRIMARY KEY (`id`));

CREATE TABLE IF NOT EXISTS `acl_kbs` (`id` varchar(64),`name` varchar(255),`owner_id` varchar(255),`visibility` varchar(32),PRIMARY KEY (`id`));

CREATE TABLE IF NOT EXISTS `acl_rows` (`id` integer PRIMARY KEY AUTOINCREMENT,`resource_type` varchar(32),`resource_id` varchar(255),`grantee_type` varchar(32),`target_id` varchar(255),`permission` varchar(32),`created_by` varchar(255),`created_at` datetime,`expires_at` datetime);

CREATE TABLE IF NOT EXISTS `acl_user_groups` (`user_id` varchar(255),`group_id` varchar(255),PRIMARY KEY (`user_id`,`group_id`));

CREATE TABLE IF NOT EXISTS `acl_visibility` (`id` integer PRIMARY KEY AUTOINCREMENT,`resource_id` varchar(255),`level` varchar(32));

CREATE TABLE IF NOT EXISTS `agent_thread_records` (`id` varchar(32),`thread_id` varchar(128) NOT NULL,`round_id` varchar(32) NOT NULL DEFAULT "",`task_id` varchar(128) NOT NULL DEFAULT "",`stream_kind` varchar(32) NOT NULL,`record_key` varchar(64) NOT NULL,`event_name` varchar(128) NOT NULL DEFAULT "",`payload_text` text NOT NULL DEFAULT "",`raw_frame` text NOT NULL DEFAULT "",`created_at` datetime NOT NULL,`updated_at` datetime NOT NULL,PRIMARY KEY (`id`));

CREATE TABLE IF NOT EXISTS `agent_thread_rounds` (`round_id` varchar(32),`thread_id` varchar(128) NOT NULL,`request_hash` varchar(64) NOT NULL DEFAULT "",`task_id` varchar(128) NOT NULL DEFAULT "",`status` varchar(32) NOT NULL DEFAULT "created",`user_message` text NOT NULL DEFAULT "",`assistant_message` text NOT NULL DEFAULT "",`request_payload` text NOT NULL DEFAULT "",`created_at` datetime NOT NULL,`updated_at` datetime NOT NULL,PRIMARY KEY (`round_id`));

CREATE TABLE IF NOT EXISTS `agent_threads` (`thread_id` varchar(128),`current_task_id` varchar(128) NOT NULL DEFAULT "",`status` varchar(32) NOT NULL DEFAULT "created",`thread_payload` text NOT NULL DEFAULT "",`last_message_request_hash` varchar(64) NOT NULL DEFAULT "",`create_user_id` varchar(255) NOT NULL DEFAULT "",`create_user_name` varchar(255) NOT NULL DEFAULT "",`created_at` datetime NOT NULL,`updated_at` datetime NOT NULL,PRIMARY KEY (`thread_id`));

CREATE TABLE IF NOT EXISTS `agent_user_active_threads` (`user_id` varchar(255),`thread_id` varchar(128) NOT NULL DEFAULT "",`status` varchar(32) NOT NULL DEFAULT "creating",`create_token` varchar(64) NOT NULL DEFAULT "",`lease_until` datetime NOT NULL,`created_at` datetime NOT NULL,`updated_at` datetime NOT NULL,PRIMARY KEY (`user_id`));

CREATE TABLE IF NOT EXISTS `chat_histories` (`id` varchar(36),`seq` integer NOT NULL,`conversation_id` varchar(36) NOT NULL,`raw_content` text,`retrieval_result` json,`content` text,`result` text,`feed_back` integer DEFAULT 0,`reason` varchar(255),`expected_answer` text,`ext` json,`version` varchar(128) DEFAULT "2.3",`create_time` datetime NOT NULL,`update_time` datetime NOT NULL,PRIMARY KEY (`id`));

CREATE TABLE IF NOT EXISTS `conversations` (`id` varchar(36),`display_name` varchar(255),`channel_id` varchar(36) NOT NULL DEFAULT "default",`search_config` json,`application_id` varchar(64) DEFAULT "",`ext` json,`model` varchar(64) DEFAULT "",`models` json,`chat_times` integer NOT NULL DEFAULT 0,`create_user_id` varchar(255) NOT NULL,`create_user_name` varchar(255) NOT NULL,`created_at` datetime NOT NULL,`updated_at` datetime NOT NULL,`deleted_at` datetime,PRIMARY KEY (`id`));

CREATE TABLE IF NOT EXISTS `datasets` (`id` varchar(255),`kb_id` varchar(255) NOT NULL,`display_name` varchar(255) NOT NULL,`desc` longtext NOT NULL,`cover_image` varchar(255) NOT NULL,`resource_uid` varchar(36) NOT NULL,`bucket_name` varchar(255) NOT NULL,`oss_path` varchar(255) NOT NULL,`dataset_info` json,`dataset_state` integer NOT NULL,`embedding_model` varchar(255) NOT NULL,`embedding_model_provider` varchar(255) NOT NULL,`share_type` integer NOT NULL,`shared_at` datetime,`tenant_id` varchar(36) NOT NULL,`is_demonstrate` numeric NOT NULL DEFAULT false,`type` integer NOT NULL DEFAULT 1,`ext` json,`create_user_id` varchar(255) NOT NULL,`create_user_name` varchar(255) NOT NULL,`created_at` datetime NOT NULL,`updated_at` datetime NOT NULL,`deleted_at` datetime,PRIMARY KEY (`id`));

CREATE TABLE IF NOT EXISTS `default_datasets` (`id` integer PRIMARY KEY AUTOINCREMENT,`dataset_id` varchar(64) NOT NULL,`dataset_name` varchar(255) NOT NULL,`create_user_id` varchar(255) NOT NULL,`create_user_name` varchar(255) NOT NULL,`created_at` datetime NOT NULL,`updated_at` datetime NOT NULL,`deleted_at` datetime);

CREATE TABLE IF NOT EXISTS `default_model_providers` (`id` varchar(64),`name` varchar(255) NOT NULL,`description` text NOT NULL,`base_url` varchar(1024) NOT NULL DEFAULT "",`created_at` datetime NOT NULL,`updated_at` datetime NOT NULL,`deleted_at` datetime,PRIMARY KEY (`id`));

CREATE TABLE IF NOT EXISTS `default_models` (`id` varchar(64),`default_model_provider_id` varchar(64) NOT NULL,`provider_name` varchar(255) NOT NULL DEFAULT "",`name` varchar(512) NOT NULL,`model_type` varchar(64) NOT NULL,`base_url` varchar(1024) NOT NULL DEFAULT "",`created_at` datetime NOT NULL,`updated_at` datetime NOT NULL,`deleted_at` datetime,PRIMARY KEY (`id`));

CREATE TABLE IF NOT EXISTS `default_prompts` (`id` integer PRIMARY KEY AUTOINCREMENT,`prompt_id` varchar(64) NOT NULL,`prompt_name` varchar(255) NOT NULL,`create_user_id` varchar(255) NOT NULL,`create_user_name` varchar(255) NOT NULL,`created_at` datetime NOT NULL,`updated_at` datetime NOT NULL,`deleted_at` datetime);

CREATE TABLE IF NOT EXISTS `documents` (`id` varchar(128),`lazyllm_doc_id` varchar(128) NOT NULL DEFAULT "",`dataset_id` varchar(255) NOT NULL,`display_name` varchar(512) NOT NULL DEFAULT "",`p_id` varchar(255) NOT NULL DEFAULT "",`tags` json,`file_id` varchar(128) NOT NULL DEFAULT "",`pdf_convert_result` varchar(64) NOT NULL DEFAULT "",`ext` json,`create_user_id` varchar(255) NOT NULL,`create_user_name` varchar(255) NOT NULL,`created_at` datetime NOT NULL,`updated_at` datetime NOT NULL,`deleted_at` datetime,PRIMARY KEY (`id`));

CREATE TABLE IF NOT EXISTS `multi_answers_chat_histories` (`id` varchar(36),`seq` integer NOT NULL,`conversation_id` varchar(36) NOT NULL,`raw_content` text,`retrieval_result` json,`content` text,`result` text,`feed_back` integer DEFAULT 0,`reason` varchar(255),`ext` json,`endpoint` varchar(512),`create_time` datetime NOT NULL,`update_time` datetime NOT NULL,PRIMARY KEY (`id`));

CREATE TABLE IF NOT EXISTS `multi_answers_switches` (`id` integer PRIMARY KEY AUTOINCREMENT,`status` integer NOT NULL DEFAULT 0,`create_user_id` varchar(255) NOT NULL,`create_user_name` varchar(255) NOT NULL,`created_at` datetime NOT NULL,`updated_at` datetime NOT NULL,`deleted_at` datetime);

CREATE TABLE IF NOT EXISTS `prompts` (`id` varchar(64),`name` varchar(255) NOT NULL,`content` text NOT NULL,`create_user_id` varchar(255) NOT NULL,`create_user_name` varchar(255) NOT NULL,`created_at` datetime NOT NULL,`updated_at` datetime NOT NULL,`deleted_at` datetime,PRIMARY KEY (`id`));

CREATE TABLE IF NOT EXISTS `resource_session_snapshots` (`id` varchar(36),`session_id` varchar(128) NOT NULL,`user_id` varchar(255) NOT NULL DEFAULT "",`resource_type` varchar(32) NOT NULL,`resource_key` varchar(1024) NOT NULL,`category` varchar(128) NOT NULL DEFAULT "",`parent_skill_name` varchar(255) NOT NULL DEFAULT "",`skill_name` varchar(255) NOT NULL DEFAULT "",`file_ext` varchar(32) NOT NULL DEFAULT "",`relative_path` varchar(1024) NOT NULL DEFAULT "",`snapshot_hash` varchar(64) NOT NULL DEFAULT "",`created_at` datetime NOT NULL,PRIMARY KEY (`id`));

CREATE TABLE IF NOT EXISTS `resource_suggestions` (`id` varchar(36),`user_id` varchar(255) NOT NULL DEFAULT "",`resource_type` varchar(32) NOT NULL,`resource_key` varchar(1024) NOT NULL DEFAULT "",`category` varchar(128) NOT NULL DEFAULT "",`parent_skill_name` varchar(255) NOT NULL DEFAULT "",`skill_name` varchar(255) NOT NULL DEFAULT "",`file_ext` varchar(32) NOT NULL DEFAULT "",`relative_path` varchar(1024) NOT NULL DEFAULT "",`action` varchar(32) NOT NULL,`session_id` varchar(128) NOT NULL,`snapshot_hash` varchar(64) NOT NULL DEFAULT "",`title` varchar(255) NOT NULL DEFAULT "",`content` text,`reason` text,`full_content` text,`status` varchar(32) NOT NULL,`invalid_reason` text,`reviewer_id` varchar(255) NOT NULL DEFAULT "",`reviewer_name` varchar(255) NOT NULL DEFAULT "",`reviewed_at` datetime,`ext` json,`created_at` datetime NOT NULL,`updated_at` datetime NOT NULL,PRIMARY KEY (`id`));

CREATE TABLE IF NOT EXISTS `skill_resources` (`id` varchar(36),`owner_user_id` varchar(255) NOT NULL,`owner_user_name` varchar(255) NOT NULL DEFAULT "",`category` varchar(128) NOT NULL,`parent_skill_name` varchar(255) NOT NULL DEFAULT "",`skill_name` varchar(255) NOT NULL DEFAULT "",`node_type` varchar(32) NOT NULL,`description` text,`tags` json,`file_ext` varchar(32) NOT NULL DEFAULT "md",`relative_path` varchar(1024) NOT NULL,`content` text NOT NULL DEFAULT "",`content_size` integer NOT NULL DEFAULT 0,`mime_type` varchar(128) NOT NULL DEFAULT "text/plain",`content_hash` varchar(64) NOT NULL DEFAULT "",`version` integer NOT NULL DEFAULT 1,`draft_content` text NOT NULL DEFAULT "",`draft_source_version` integer NOT NULL DEFAULT 0,`draft_status` varchar(32) NOT NULL DEFAULT "",`draft_updated_at` datetime,`auto_evo` numeric NOT NULL DEFAULT false,`auto_evo_apply_status` varchar(32) NOT NULL DEFAULT "idle",`auto_evo_generation` integer NOT NULL DEFAULT 0,`auto_evo_started_at` datetime,`auto_evo_finished_at` datetime,`auto_evo_error` text NOT NULL DEFAULT "",`is_enabled` numeric NOT NULL DEFAULT true,`update_status` varchar(32) NOT NULL DEFAULT "up_to_date",`ext` json,`create_user_id` varchar(255) NOT NULL,`create_user_name` varchar(255) NOT NULL DEFAULT "",`created_at` datetime NOT NULL,`updated_at` datetime NOT NULL,PRIMARY KEY (`id`));

CREATE TABLE IF NOT EXISTS `skill_share_items` (`id` varchar(36),`share_task_id` varchar(36) NOT NULL,`target_user_id` varchar(255) NOT NULL,`target_user_name` varchar(255) NOT NULL DEFAULT "",`status` varchar(32) NOT NULL,`target_relative_root` varchar(1024) NOT NULL DEFAULT "",`accepted_at` datetime,`rejected_at` datetime,`target_root_skill_id` varchar(36) NOT NULL DEFAULT "",`error_message` text,`created_at` datetime NOT NULL,`updated_at` datetime NOT NULL,PRIMARY KEY (`id`));

CREATE TABLE IF NOT EXISTS `skill_share_tasks` (`id` varchar(36),`source_user_id` varchar(255) NOT NULL,`source_user_name` varchar(255) NOT NULL DEFAULT "",`source_skill_id` varchar(36) NOT NULL,`source_category` varchar(128) NOT NULL DEFAULT "",`source_parent_skill_name` varchar(255) NOT NULL DEFAULT "",`source_relative_root` varchar(1024) NOT NULL DEFAULT "",`message` text,`created_at` datetime NOT NULL,`updated_at` datetime NOT NULL,PRIMARY KEY (`id`));

CREATE TABLE IF NOT EXISTS `system_memories` (`id` varchar(36),`user_id` varchar(255) NOT NULL DEFAULT "",`content` text NOT NULL DEFAULT "",`content_hash` varchar(64) NOT NULL DEFAULT "",`version` integer NOT NULL DEFAULT 1,`draft_content` text,`draft_source_version` integer NOT NULL DEFAULT 0,`draft_status` varchar(32) NOT NULL DEFAULT "",`draft_updated_at` datetime,`auto_evo` numeric NOT NULL DEFAULT true,`auto_evo_apply_status` varchar(32) NOT NULL DEFAULT "idle",`auto_evo_generation` integer NOT NULL DEFAULT 0,`auto_evo_started_at` datetime,`auto_evo_finished_at` datetime,`auto_evo_error` text NOT NULL DEFAULT "",`ext` json,`updated_by` varchar(255) NOT NULL DEFAULT "",`updated_by_name` varchar(255) NOT NULL DEFAULT "",`created_at` datetime NOT NULL,`updated_at` datetime NOT NULL,PRIMARY KEY (`id`));

CREATE TABLE IF NOT EXISTS `system_user_preferences` (`id` varchar(36),`user_id` varchar(255) NOT NULL DEFAULT "",`content` text NOT NULL DEFAULT "",`content_hash` varchar(64) NOT NULL DEFAULT "",`version` integer NOT NULL DEFAULT 1,`draft_content` text,`draft_source_version` integer NOT NULL DEFAULT 0,`draft_status` varchar(32) NOT NULL DEFAULT "",`draft_updated_at` datetime,`auto_evo` numeric NOT NULL DEFAULT true,`auto_evo_apply_status` varchar(32) NOT NULL DEFAULT "idle",`auto_evo_generation` integer NOT NULL DEFAULT 0,`auto_evo_started_at` datetime,`auto_evo_finished_at` datetime,`auto_evo_error` text NOT NULL DEFAULT "",`ext` json,`updated_by` varchar(255) NOT NULL DEFAULT "",`updated_by_name` varchar(255) NOT NULL DEFAULT "",`created_at` datetime NOT NULL,`updated_at` datetime NOT NULL,PRIMARY KEY (`id`));

CREATE TABLE IF NOT EXISTS `tasks` (`id` varchar(128),`lazyllm_task_id` varchar(128) NOT NULL DEFAULT "",`doc_id` varchar(128),`kb_id` varchar(255),`algo_id` varchar(255),`dataset_id` varchar(255) NOT NULL,`task_type` varchar(128) NOT NULL DEFAULT "",`document_pid` varchar(255) NOT NULL DEFAULT "",`target_pid` varchar(255) NOT NULL DEFAULT "",`target_dataset_id` varchar(255) NOT NULL DEFAULT "",`display_name` varchar(512) NOT NULL DEFAULT "",`ext` json,`create_user_id` varchar(255) NOT NULL,`create_user_name` varchar(255) NOT NULL,`created_at` datetime NOT NULL,`updated_at` datetime NOT NULL,`deleted_at` datetime,PRIMARY KEY (`id`));

CREATE TABLE IF NOT EXISTS `upload_sessions` (`id` integer PRIMARY KEY AUTOINCREMENT,`upload_id` varchar(128) NOT NULL,`task_id` varchar(128) NOT NULL,`dataset_id` varchar(255) NOT NULL,`tenant_id` varchar(36) NOT NULL,`document_id` varchar(128) NOT NULL,`upload_state` varchar(64) NOT NULL DEFAULT "",`ext` json,`create_user_id` varchar(255) NOT NULL,`create_user_name` varchar(255) NOT NULL,`created_at` datetime NOT NULL,`updated_at` datetime NOT NULL,`deleted_at` datetime);

CREATE TABLE IF NOT EXISTS `uploaded_files` (`id` integer PRIMARY KEY AUTOINCREMENT,`upload_file_id` varchar(128) NOT NULL,`dataset_id` varchar(255) NOT NULL,`tenant_id` varchar(36) NOT NULL,`task_id` varchar(128) NOT NULL DEFAULT "",`document_id` varchar(128) NOT NULL DEFAULT "",`status` varchar(64) NOT NULL DEFAULT "",`ext` json,`create_user_id` varchar(255) NOT NULL,`create_user_name` varchar(255) NOT NULL,`created_at` datetime NOT NULL,`updated_at` datetime NOT NULL,`deleted_at` datetime);

CREATE TABLE IF NOT EXISTS `user_model_provider_group_models` (`id` varchar(64),`user_model_provider_id` varchar(64) NOT NULL,`user_model_provider_group_id` varchar(64) NOT NULL,`provider_name` varchar(255) NOT NULL DEFAULT "",`name` varchar(512) NOT NULL,`model_type` varchar(64) NOT NULL,`base_url` varchar(1024) NOT NULL DEFAULT "",`is_default` boolean NOT NULL DEFAULT false,`create_user_id` varchar(255) NOT NULL,`create_user_name` varchar(255) NOT NULL,`created_at` datetime NOT NULL,`updated_at` datetime NOT NULL,`deleted_at` datetime,PRIMARY KEY (`id`));

CREATE TABLE IF NOT EXISTS `user_model_provider_groups` (`id` varchar(64),`user_model_provider_id` varchar(64) NOT NULL,`name` varchar(255) NOT NULL,`base_url` varchar(1024) NOT NULL,`api_key` text NOT NULL,`is_verified` boolean NOT NULL DEFAULT false,`create_user_id` varchar(255) NOT NULL,`create_user_name` varchar(255) NOT NULL,`created_at` datetime NOT NULL,`updated_at` datetime NOT NULL,`deleted_at` datetime,PRIMARY KEY (`id`));

CREATE TABLE IF NOT EXISTS `user_model_providers` (`id` varchar(64),`default_model_provider_id` varchar(64) NOT NULL,`name` varchar(255) NOT NULL,`description` text NOT NULL,`base_url` varchar(1024) NOT NULL DEFAULT "",`create_user_id` varchar(255) NOT NULL,`create_user_name` varchar(255) NOT NULL,`created_at` datetime NOT NULL,`updated_at` datetime NOT NULL,`deleted_at` datetime,PRIMARY KEY (`id`));

CREATE TABLE IF NOT EXISTS `user_personalization_settings` (`id` integer PRIMARY KEY AUTOINCREMENT,`user_id` varchar(255) NOT NULL,`enabled` numeric NOT NULL DEFAULT true,`updated_by` varchar(255) NOT NULL DEFAULT "",`updated_by_name` varchar(255) NOT NULL DEFAULT "",`created_at` datetime NOT NULL,`updated_at` datetime NOT NULL);

CREATE TABLE IF NOT EXISTS `user_selected_models` (`id` integer PRIMARY KEY AUTOINCREMENT,`user_id` varchar(255) NOT NULL,`user_name` varchar(255) NOT NULL DEFAULT "",`model_type` varchar(64) NOT NULL,`user_model_provider_group_model_id` varchar(64) NOT NULL,`share` boolean NOT NULL DEFAULT false,`created_at` datetime NOT NULL,`updated_at` datetime NOT NULL);

CREATE TABLE IF NOT EXISTS `word_group_conflicts` (`id` varchar(64),`reason` text NOT NULL DEFAULT "",`word` text NOT NULL DEFAULT "",`description` text NOT NULL DEFAULT "",`group_ids` text NOT NULL DEFAULT "[]",`create_user_id` varchar(255) NOT NULL,`message_ids` text NOT NULL DEFAULT "[]",`created_at` datetime NOT NULL,`updated_at` datetime NOT NULL,`deleted_at` datetime,PRIMARY KEY (`id`));

CREATE TABLE IF NOT EXISTS `words` (`id` varchar(64),`word` varchar(512) NOT NULL,`word_kind` varchar(32) NOT NULL DEFAULT "term",`group_id` varchar(64) NOT NULL,`description` varchar(512) NOT NULL DEFAULT "",`source` varchar(32) NOT NULL DEFAULT "user",`reference_info` text NOT NULL DEFAULT "",`locked` boolean NOT NULL DEFAULT false,`create_user_id` varchar(255) NOT NULL,`create_user_name` varchar(255) NOT NULL,`created_at` datetime NOT NULL,`updated_at` datetime NOT NULL,`deleted_at` datetime,PRIMARY KEY (`id`));

CREATE INDEX IF NOT EXISTS `idx_acl_resource` ON `acl_rows`(`resource_type`,`resource_id`);

CREATE INDEX IF NOT EXISTS `idx_acl_visibility_resource_id` ON `acl_visibility`(`resource_id`);

CREATE INDEX IF NOT EXISTS `idx_agent_thread_records_round_stream_id` ON `agent_thread_records`(`round_id`,`stream_kind`);

CREATE INDEX IF NOT EXISTS `idx_agent_thread_records_task_id` ON `agent_thread_records`(`task_id`);

CREATE INDEX IF NOT EXISTS `idx_agent_thread_records_thread_round_id` ON `agent_thread_records`(`thread_id`,`round_id`);

CREATE INDEX IF NOT EXISTS `idx_agent_thread_records_thread_stream_id` ON `agent_thread_records`(`thread_id`,`stream_kind`);

CREATE INDEX IF NOT EXISTS `idx_agent_thread_rounds_task_id` ON `agent_thread_rounds`(`task_id`);

CREATE INDEX IF NOT EXISTS `idx_agent_thread_rounds_thread_id` ON `agent_thread_rounds`(`thread_id`);

CREATE INDEX IF NOT EXISTS `idx_agent_thread_rounds_thread_request_hash` ON `agent_thread_rounds`(`thread_id`,`request_hash`);

CREATE INDEX IF NOT EXISTS `idx_agent_threads_current_task_id` ON `agent_threads`(`current_task_id`);

CREATE INDEX IF NOT EXISTS `idx_agent_user_active_threads_status_lease` ON `agent_user_active_threads`(`status`,`lease_until`);

CREATE INDEX IF NOT EXISTS `idx_agent_user_active_threads_thread_id` ON `agent_user_active_threads`(`thread_id`);

CREATE INDEX IF NOT EXISTS `idx_chat_histories_conversation_id` ON `chat_histories`(`conversation_id`);

CREATE INDEX IF NOT EXISTS `idx_datasets_kb_id` ON `datasets`(`kb_id`);

CREATE INDEX IF NOT EXISTS `idx_documents_dataset_id` ON `documents`(`dataset_id`);

CREATE INDEX IF NOT EXISTS `idx_documents_lazyllm_doc_id` ON `documents`(`lazyllm_doc_id`);

CREATE INDEX IF NOT EXISTS `idx_documents_p_id` ON `documents`(`p_id`);

CREATE INDEX IF NOT EXISTS `idx_multi_answers_chat_histories_conversation_id` ON `multi_answers_chat_histories`(`conversation_id`);

CREATE INDEX IF NOT EXISTS `idx_resource_session_snapshots_session_id` ON `resource_session_snapshots`(`session_id`);

CREATE INDEX IF NOT EXISTS `idx_resource_suggestions_list` ON `resource_suggestions`(`user_id`,`resource_type`,`status`);

CREATE INDEX IF NOT EXISTS `idx_resource_suggestions_session_id` ON `resource_suggestions`(`session_id`);

CREATE INDEX IF NOT EXISTS `idx_resource_uid` ON `datasets`(`resource_uid`);

CREATE INDEX IF NOT EXISTS `idx_skill_resources_owner_node_enabled` ON `skill_resources`(`owner_user_id`,`node_type`,`is_enabled`,`category`);

CREATE INDEX IF NOT EXISTS `idx_skill_share_items_target_user` ON `skill_share_items`(`share_task_id`,`target_user_id`,`status`);

CREATE INDEX IF NOT EXISTS `idx_skill_share_tasks_source_user` ON `skill_share_tasks`(`source_user_id`);

CREATE INDEX IF NOT EXISTS `idx_tasks_algo_id` ON `tasks`(`algo_id`);

CREATE INDEX IF NOT EXISTS `idx_tasks_dataset_id` ON `tasks`(`dataset_id`);

CREATE INDEX IF NOT EXISTS `idx_tasks_doc_id` ON `tasks`(`doc_id`);

CREATE INDEX IF NOT EXISTS `idx_tasks_document_p_id` ON `tasks`(`document_pid`);

CREATE INDEX IF NOT EXISTS `idx_tasks_kb_id` ON `tasks`(`kb_id`);

CREATE INDEX IF NOT EXISTS `idx_tasks_lazyllm_task_id` ON `tasks`(`lazyllm_task_id`);

CREATE INDEX IF NOT EXISTS `idx_tasks_target_dataset_id` ON `tasks`(`target_dataset_id`);

CREATE INDEX IF NOT EXISTS `idx_tasks_task_type` ON `tasks`(`task_type`);

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

CREATE INDEX IF NOT EXISTS `idx_word_column` ON `words`(`create_user_id`,`word`);

CREATE INDEX IF NOT EXISTS `idx_word_create_user_group_id` ON `words`(`create_user_id`,`group_id`);

CREATE INDEX IF NOT EXISTS `idx_word_group_conflict_user_updated` ON `word_group_conflicts`(`create_user_id`,`updated_at` desc);

CREATE UNIQUE INDEX IF NOT EXISTS `uk_agent_thread_records_record_key` ON `agent_thread_records`(`thread_id`,`round_id`,`stream_kind`,`record_key`);

CREATE UNIQUE INDEX IF NOT EXISTS `uk_default_model_providers_name` ON `default_model_providers`(`name`);

CREATE UNIQUE INDEX IF NOT EXISTS `uk_default_models_provider_name` ON `default_models`(`default_model_provider_id`,`name`);

CREATE UNIQUE INDEX IF NOT EXISTS `uk_resource_session_snapshots` ON `resource_session_snapshots`(`session_id`,`resource_type`,`resource_key`);

CREATE UNIQUE INDEX IF NOT EXISTS `uk_skill_resources_owner_relative_path` ON `skill_resources`(`owner_user_id`,`relative_path`);

CREATE UNIQUE INDEX IF NOT EXISTS `uk_system_memories_user_id` ON `system_memories`(`user_id`);

CREATE UNIQUE INDEX IF NOT EXISTS `uk_system_user_preferences_user_id` ON `system_user_preferences`(`user_id`);

CREATE UNIQUE INDEX IF NOT EXISTS `uk_user_model_provider_group_models_group_name` ON `user_model_provider_group_models`(`user_model_provider_group_id`,`name`);

CREATE UNIQUE INDEX IF NOT EXISTS `uk_user_personalization_settings_user_id` ON `user_personalization_settings`(`user_id`);

CREATE UNIQUE INDEX IF NOT EXISTS `uk_user_selected_models_user_type` ON `user_selected_models`(`user_id`,`model_type`);

CREATE UNIQUE INDEX IF NOT EXISTS `ukx_create_user_id_dataset_id` ON `default_datasets`(`dataset_id`);

INSERT INTO "acl_groups" (id,name) SELECT id,name FROM "__v02_acl_groups";
INSERT INTO "acl_kbs" (id,name,owner_id,visibility) SELECT id,name,owner_id,visibility FROM "__v02_acl_kbs";
INSERT INTO "acl_rows" (id,resource_type,resource_id,grantee_type,target_id,permission,created_by,created_at,expires_at) SELECT id,resource_type,resource_id,grantee_type,target_id,permission,created_by,created_at,expires_at FROM "__v02_acl_rows";
INSERT INTO "acl_user_groups" (user_id,group_id) SELECT user_id,group_id FROM "__v02_acl_user_groups";
INSERT INTO "acl_visibility" (id,resource_id,level) SELECT id,resource_id,level FROM "__v02_acl_visibility";
INSERT INTO "agent_thread_records" (id,thread_id,round_id,task_id,stream_kind,record_key,event_name,payload_text,raw_frame,created_at,updated_at) SELECT id,thread_id,round_id,task_id,stream_kind,record_key,event_name,payload_text,raw_frame,created_at,updated_at FROM "__v02_agent_thread_records";
INSERT INTO "agent_thread_rounds" (round_id,thread_id,request_hash,task_id,status,user_message,assistant_message,request_payload,created_at,updated_at) SELECT round_id,thread_id,request_hash,task_id,status,user_message,assistant_message,request_payload,created_at,updated_at FROM "__v02_agent_thread_rounds";
INSERT INTO "agent_threads" (thread_id,current_task_id,status,thread_payload,last_message_request_hash,create_user_id,create_user_name,created_at,updated_at) SELECT thread_id,current_task_id,status,thread_payload,last_message_request_hash,create_user_id,create_user_name,created_at,updated_at FROM "__v02_agent_threads";
INSERT INTO "agent_user_active_threads" (user_id,thread_id,status,create_token,lease_until,created_at,updated_at) SELECT user_id,thread_id,status,create_token,lease_until,created_at,updated_at FROM "__v02_agent_user_active_threads";
INSERT INTO "chat_histories" (id,seq,conversation_id,raw_content,retrieval_result,content,result,feed_back,reason,expected_answer,ext,version,create_time,update_time) SELECT id,seq,conversation_id,raw_content,retrieval_result,content,result,feed_back,reason,expected_answer,ext,version,create_time,update_time FROM "__v02_chat_histories";
INSERT INTO "conversations" (id,display_name,channel_id,search_config,application_id,ext,model,models,chat_times,create_user_id,create_user_name,created_at,updated_at,deleted_at) SELECT id,display_name,channel_id,search_config,application_id,ext,model,models,chat_times,create_user_id,create_user_name,created_at,updated_at,deleted_at FROM "__v02_conversations";
INSERT INTO "datasets" (id,kb_id,display_name,desc,cover_image,resource_uid,bucket_name,oss_path,dataset_info,dataset_state,embedding_model,embedding_model_provider,share_type,shared_at,tenant_id,is_demonstrate,type,ext,create_user_id,create_user_name,created_at,updated_at,deleted_at) SELECT id,kb_id,display_name,desc,cover_image,resource_uid,bucket_name,oss_path,dataset_info,dataset_state,embedding_model,embedding_model_provider,share_type,shared_at,tenant_id,is_demonstrate,type,ext,create_user_id,create_user_name,created_at,updated_at,deleted_at FROM "__v02_datasets";
INSERT INTO "default_datasets" (id,dataset_id,dataset_name,create_user_id,create_user_name,created_at,updated_at,deleted_at) SELECT id,dataset_id,dataset_name,create_user_id,create_user_name,created_at,updated_at,deleted_at FROM "__v02_default_datasets";
INSERT INTO "default_model_providers" (id,name,description,base_url,created_at,updated_at,deleted_at) SELECT id,name,description,base_url,created_at,updated_at,deleted_at FROM "__v02_default_model_providers";
INSERT INTO "default_models" (id,default_model_provider_id,provider_name,name,model_type,created_at,updated_at,deleted_at) SELECT id,default_model_provider_id,provider_name,name,model_type,created_at,updated_at,deleted_at FROM "__v02_default_models";
INSERT INTO "documents" (id,lazyllm_doc_id,dataset_id,display_name,p_id,tags,file_id,pdf_convert_result,ext,create_user_id,create_user_name,created_at,updated_at,deleted_at) SELECT id,lazyllm_doc_id,dataset_id,display_name,p_id,tags,file_id,pdf_convert_result,ext,create_user_id,create_user_name,created_at,updated_at,deleted_at FROM "__v02_documents";
INSERT INTO "multi_answers_chat_histories" (id,seq,conversation_id,raw_content,retrieval_result,content,result,feed_back,reason,ext,endpoint,create_time,update_time) SELECT id,seq,conversation_id,raw_content,retrieval_result,content,result,feed_back,reason,ext,endpoint,create_time,update_time FROM "__v02_multi_answers_chat_histories";
INSERT INTO "multi_answers_switches" (id,status,create_user_id,create_user_name,created_at,updated_at,deleted_at) SELECT id,status,create_user_id,create_user_name,created_at,updated_at,deleted_at FROM "__v02_multi_answers_switches";
INSERT INTO "prompts" (id,name,content,create_user_id,create_user_name,created_at,updated_at,deleted_at) SELECT id,name,content,create_user_id,create_user_name,created_at,updated_at,deleted_at FROM "__v02_prompts";
INSERT INTO "resource_session_snapshots" (id,session_id,user_id,resource_type,resource_key,category,parent_skill_name,skill_name,file_ext,relative_path,snapshot_hash,created_at) SELECT id,session_id,user_id,resource_type,resource_key,category,parent_skill_name,skill_name,file_ext,relative_path,snapshot_hash,created_at FROM "__v02_resource_session_snapshots";
INSERT INTO "skill_share_items" (id,share_task_id,target_user_id,target_user_name,status,target_relative_root,accepted_at,rejected_at,target_root_skill_id,error_message,created_at,updated_at) SELECT id,share_task_id,target_user_id,target_user_name,status,target_relative_root,accepted_at,rejected_at,target_root_skill_id,error_message,created_at,updated_at FROM "__v02_skill_share_items";
INSERT INTO "skill_share_tasks" (id,source_user_id,source_user_name,source_skill_id,source_category,source_parent_skill_name,source_relative_root,message,created_at,updated_at) SELECT id,source_user_id,source_user_name,source_skill_id,source_category,source_parent_skill_name,source_relative_root,message,created_at,updated_at FROM "__v02_skill_share_tasks";
INSERT INTO "tasks" (id,lazyllm_task_id,doc_id,kb_id,algo_id,dataset_id,task_type,document_pid,target_pid,target_dataset_id,display_name,ext,create_user_id,create_user_name,created_at,updated_at,deleted_at) SELECT id,lazyllm_task_id,doc_id,kb_id,algo_id,dataset_id,task_type,document_pid,target_pid,target_dataset_id,display_name,ext,create_user_id,create_user_name,created_at,updated_at,deleted_at FROM "__v02_tasks";
INSERT INTO "upload_sessions" (id,upload_id,task_id,dataset_id,tenant_id,document_id,upload_state,ext,create_user_id,create_user_name,created_at,updated_at,deleted_at) SELECT id,upload_id,task_id,dataset_id,tenant_id,document_id,upload_state,ext,create_user_id,create_user_name,created_at,updated_at,deleted_at FROM "__v02_upload_sessions";
INSERT INTO "uploaded_files" (id,upload_file_id,dataset_id,tenant_id,task_id,document_id,status,ext,create_user_id,create_user_name,created_at,updated_at,deleted_at) SELECT id,upload_file_id,dataset_id,tenant_id,task_id,document_id,status,ext,create_user_id,create_user_name,created_at,updated_at,deleted_at FROM "__v02_uploaded_files";
INSERT INTO "user_model_provider_group_models" (id,user_model_provider_id,user_model_provider_group_id,provider_name,name,model_type,is_default,create_user_id,create_user_name,created_at,updated_at,deleted_at) SELECT id,user_model_provider_id,user_model_provider_group_id,provider_name,name,model_type,is_default,create_user_id,create_user_name,created_at,updated_at,deleted_at FROM "__v02_user_model_provider_group_models";
INSERT INTO "user_model_provider_groups" (id,user_model_provider_id,name,base_url,api_key,is_verified,create_user_id,create_user_name,created_at,updated_at,deleted_at) SELECT id,user_model_provider_id,name,base_url,api_key,is_verified,create_user_id,create_user_name,created_at,updated_at,deleted_at FROM "__v02_user_model_provider_groups";
INSERT INTO "user_model_providers" (id,default_model_provider_id,name,description,base_url,create_user_id,create_user_name,created_at,updated_at,deleted_at) SELECT id,default_model_provider_id,name,description,base_url,create_user_id,create_user_name,created_at,updated_at,deleted_at FROM "__v02_user_model_providers";
INSERT INTO "user_personalization_settings" (id,user_id,enabled,updated_by,updated_by_name,created_at,updated_at) SELECT id,user_id,enabled,updated_by,updated_by_name,created_at,updated_at FROM "__v02_user_personalization_settings";
INSERT INTO "user_selected_models" (id,user_id,user_name,model_type,user_model_provider_group_model_id,share,created_at,updated_at) SELECT id,user_id,user_name,model_type,user_model_provider_group_model_id,share,created_at,updated_at FROM "__v02_user_selected_models";
INSERT INTO "word_group_conflicts" (id,reason,word,description,group_ids,create_user_id,message_ids,created_at,updated_at,deleted_at) SELECT id,reason,word,description,group_ids,create_user_id,message_ids,created_at,updated_at,deleted_at FROM "__v02_word_group_conflicts";
INSERT INTO "words" (id,word,word_kind,group_id,description,source,reference_info,locked,create_user_id,create_user_name,created_at,updated_at,deleted_at) SELECT id,word,word_kind,group_id,description,source,reference_info,locked,create_user_id,create_user_name,created_at,updated_at,deleted_at FROM "__v02_words";
DROP TABLE "__v02_acl_groups";
DROP TABLE "__v02_acl_kbs";
DROP TABLE "__v02_acl_rows";
DROP TABLE "__v02_acl_user_groups";
DROP TABLE "__v02_acl_visibility";
DROP TABLE "__v02_agent_thread_records";
DROP TABLE "__v02_agent_thread_rounds";
DROP TABLE "__v02_agent_thread_steps";
DROP TABLE "__v02_agent_threads";
DROP TABLE "__v02_agent_user_active_threads";
DROP TABLE "__v02_async_jobs";
DROP TABLE "__v02_automation_groups";
DROP TABLE "__v02_chat_histories";
DROP TABLE "__v02_conversation_artifacts";
DROP TABLE "__v02_conversation_idle_events";
DROP TABLE "__v02_conversations";
DROP TABLE "__v02_datasets";
DROP TABLE "__v02_default_datasets";
DROP TABLE "__v02_default_model_providers";
DROP TABLE "__v02_default_models";
DROP TABLE "__v02_documents";
DROP TABLE "__v02_eval_set_import_previews";
DROP TABLE "__v02_eval_set_items";
DROP TABLE "__v02_eval_set_shards";
DROP TABLE "__v02_eval_sets";
DROP TABLE "__v02_external_database_connections";
DROP TABLE "__v02_local_fs_chat_settings";
DROP TABLE "__v02_mcp_server_tools";
DROP TABLE "__v02_mcp_servers";
DROP TABLE "__v02_memory_review";
DROP TABLE "__v02_multi_answers_chat_histories";
DROP TABLE "__v02_multi_answers_switches";
DROP TABLE "__v02_personal_resource_blobs";
DROP TABLE "__v02_personal_resource_drafts";
DROP TABLE "__v02_personal_resource_review_action_batches";
DROP TABLE "__v02_personal_resource_review_action_items";
DROP TABLE "__v02_personal_resource_review_sessions";
DROP TABLE "__v02_personal_resource_revisions";
DROP TABLE "__v02_personal_resources";
DROP TABLE "__v02_plugin_attempt_input_bindings";
DROP TABLE "__v02_plugin_blobs";
DROP TABLE "__v02_plugin_drafts";
DROP TABLE "__v02_plugin_human_artifacts";
DROP TABLE "__v02_plugin_revision_entries";
DROP TABLE "__v02_plugin_revisions";
DROP TABLE "__v02_plugin_route_decisions";
DROP TABLE "__v02_plugin_run_outbox";
DROP TABLE "__v02_plugin_session_steps";
DROP TABLE "__v02_plugin_sessions";
DROP TABLE "__v02_plugin_slot_order";
DROP TABLE "__v02_plugin_slot_revisions";
DROP TABLE "__v02_plugin_step_intents";
DROP TABLE "__v02_plugin_transition_commands";
DROP TABLE "__v02_plugins";
DROP TABLE "__v02_prompt_categories";
DROP TABLE "__v02_prompt_user_states";
DROP TABLE "__v02_prompts";
DROP TABLE "__v02_resource_session_snapshots";
DROP TABLE "__v02_resource_update_tasks";
DROP TABLE "__v02_schedule_dependencies";
DROP TABLE "__v02_skill_blobs";
DROP TABLE "__v02_skill_draft_entries";
DROP TABLE "__v02_skill_draft_review_action_batches";
DROP TABLE "__v02_skill_draft_review_action_items";
DROP TABLE "__v02_skill_draft_review_sessions";
DROP TABLE "__v02_skill_drafts";
DROP TABLE "__v02_skill_market_items";
DROP TABLE "__v02_skill_review_results";
DROP TABLE "__v02_skill_review_scheduler_state";
DROP TABLE "__v02_skill_review_stats";
DROP TABLE "__v02_skill_revision_entries";
DROP TABLE "__v02_skill_revisions";
DROP TABLE "__v02_skill_search_indexes";
DROP TABLE "__v02_skill_share_items";
DROP TABLE "__v02_skill_share_tasks";
DROP TABLE "__v02_skills";
DROP TABLE "__v02_sub_agent_artifacts";
DROP TABLE "__v02_sub_agent_steps";
DROP TABLE "__v02_sub_agent_tasks";
DROP TABLE "__v02_task_center_tasks";
DROP TABLE "__v02_task_run_inputs";
DROP TABLE "__v02_task_run_outputs";
DROP TABLE "__v02_tasks";
DROP TABLE "__v02_upload_sessions";
DROP TABLE "__v02_uploaded_files";
DROP TABLE "__v02_user_chat_settings";
DROP TABLE "__v02_user_disabled_tools";
DROP TABLE "__v02_user_model_provider_group_models";
DROP TABLE "__v02_user_model_provider_groups";
DROP TABLE "__v02_user_model_providers";
DROP TABLE "__v02_user_personalization_settings";
DROP TABLE "__v02_user_plugin_settings";
DROP TABLE "__v02_user_schedules";
DROP TABLE "__v02_user_selected_models";
DROP TABLE "__v02_user_selected_providers";
DROP TABLE "__v02_user_ui_preferences";
DROP TABLE "__v02_word_group_conflicts";
DROP TABLE "__v02_words";

UPDATE default_models SET model_type = CASE model_type
  WHEN 'vlm' THEN 'VLM' WHEN 'embed' THEN 'embedding'
  WHEN 'cross_modal_embed' THEN 'multimodal_embedding' WHEN 'reranker' THEN 'rerank'
  ELSE model_type END
WHERE model_type IN ('vlm','embed','cross_modal_embed','reranker');
UPDATE user_model_provider_group_models SET model_type = CASE model_type
  WHEN 'vlm' THEN 'VLM' WHEN 'embed' THEN 'embedding'
  WHEN 'cross_modal_embed' THEN 'multimodal_embedding' WHEN 'reranker' THEN 'rerank'
  ELSE model_type END
WHERE model_type IN ('vlm','embed','cross_modal_embed','reranker');
UPDATE user_selected_models SET model_type = CASE model_type
  WHEN 'llm' THEN 'llm-chat' WHEN 'evo_llm' THEN 'llm-evo' WHEN 'vlm' THEN 'VLM'
  WHEN 'embed_main' THEN 'embedding' WHEN 'embed_image' THEN 'multimodal_embedding'
  WHEN 'reranker' THEN 'rerank' ELSE model_type END
WHERE model_type IN ('llm','evo_llm','vlm','embed_main','embed_image','reranker');
