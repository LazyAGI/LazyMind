-- 20260723183515_squash_post_init
-- +migrate Down

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
