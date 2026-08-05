-- 20260723183515_squash_post_init
-- +migrate Up
-- +migrate Supersedes: 20260506120000, 20260521000000, 20260527120000, 20260527130000, 20260529100000, 20260531090000, 20260531093000, 20260531100000, 20260602120000, 20260604100000, 20260604120000, 20260605120000, 20260608120000, 20260609120000, 20260610120000, 20260610123000, 20260611100000, 20260611110000, 20260612100000, 20260612110000, 20260612120000, 20260613120000, 20260615120000, 20260615140000, 20260615200000, 20260617100000, 20260618090000, 20260618100000, 20260620120000, 20260622100000, 20260622120000, 20260622130000, 20260622200000, 20260622210000, 20260625100000, 20260625200000, 20260626100000, 20260626120000, 20260626200000, 20260626210000, 20260626220000, 20260626230000, 20260629100000, 20260630120000, 20260701090000, 20260701120000, 20260701130000, 20260703120000, 20260703130000, 20260703180000, 20260704180000, 20260706120000, 20260706120001, 20260706170000, 20260707100001, 20260707120000, 20260707160000, 20260708120000, 20260709103300, 20260709103400, 20260709120000, 20260709130000, 20260709140000, 20260709150000, 20260710113000, 20260710120000, 20260710123000, 20260710180000, 20260711120000, 20260713100000, 20260713110000, 20260713170000, 20260713190000, 20260713200000, 20260714120000, 20260714130000, 20260714170000, 20260714190000, 20260715100000, 20260715170000, 20260716150000, 20260716160000, 20260717120000, 20260719112959, 20260719120000, 20260721180000, 20260721182344, 20260722142937
-- +migrate Dialect postgres

-- Flattened net migration from the unchanged init schema to the final schema.
-- Intermediate objects and superseded column/index/constraint states are intentionally omitted.

-- Objects created by init that do not exist in the final schema.
DROP TABLE public.default_prompts CASCADE;
DROP TABLE public.resource_suggestions CASCADE;
DROP TABLE public.skill_resources CASCADE;
DROP TABLE public.system_memories CASCADE;
DROP TABLE public.system_user_preferences CASCADE;

-- Net changes to tables that already exist in the init migration.

-- Drop changed indexes and constraints before changing their columns.

-- Apply each column's net change once.
ALTER TABLE "public"."agent_thread_records" ADD COLUMN "step_id" character varying(128) DEFAULT ''::character varying NOT NULL;
ALTER TABLE "public"."chat_histories" ADD COLUMN "tool_call_turns" integer DEFAULT 0 NOT NULL;
ALTER TABLE "public"."chat_histories" ADD COLUMN "thinking_duration_s" bigint DEFAULT 0 NOT NULL;
ALTER TABLE "public"."conversations" ADD COLUMN "enable_plugin" boolean;
ALTER TABLE "public"."conversations" ADD COLUMN "plugin_mode" character varying(16) DEFAULT NULL::character varying;
ALTER TABLE "public"."conversations" ADD COLUMN "enable_subagent" boolean;
ALTER TABLE "public"."conversations" ADD COLUMN "is_task_conv" boolean DEFAULT false NOT NULL;
ALTER TABLE "public"."default_model_providers" ADD COLUMN "category" character varying(64) DEFAULT 'model'::character varying NOT NULL;
ALTER TABLE "public"."default_model_providers" ADD COLUMN "capabilities" character varying(512) DEFAULT 'multi_group,custom_base_url,has_models'::character varying NOT NULL;
ALTER TABLE "public"."default_model_providers" ADD COLUMN "description_i18n" jsonb DEFAULT '{}'::jsonb NOT NULL;
ALTER TABLE "public"."default_models" DROP COLUMN "base_url";
ALTER TABLE "public"."default_models" ADD COLUMN "max_input_tokens" character varying(16);
ALTER TABLE "public"."documents" ADD COLUMN "document_type" character varying(64);
ALTER TABLE "public"."multi_answers_chat_histories" ADD COLUMN "tool_call_turns" integer DEFAULT 0 NOT NULL;
ALTER TABLE "public"."multi_answers_chat_histories" ADD COLUMN "thinking_duration_s" bigint DEFAULT 0 NOT NULL;
ALTER TABLE "public"."prompts" ADD COLUMN "category" character varying(64) DEFAULT 'custom'::character varying NOT NULL;
ALTER TABLE "public"."skill_share_items" ADD COLUMN "source_skill_id" character varying(36) DEFAULT ''::character varying NOT NULL;
ALTER TABLE "public"."uploaded_files" ADD COLUMN "content_hash" character varying(64) DEFAULT ''::character varying NOT NULL;
ALTER TABLE "public"."user_model_provider_group_models" DROP COLUMN "base_url";
ALTER TABLE "public"."user_model_provider_group_models" ADD COLUMN "max_input_tokens" character varying(16);
ALTER TABLE "public"."user_model_provider_groups" ADD COLUMN "api_key_ciphertext" text DEFAULT ''::text NOT NULL;
ALTER TABLE "public"."user_model_provider_groups" ADD COLUMN "credential_version" integer DEFAULT 0 NOT NULL;
ALTER TABLE "public"."user_model_providers" ADD COLUMN "category" character varying(64) DEFAULT 'model'::character varying NOT NULL;
ALTER TABLE "public"."user_model_providers" ADD COLUMN "capabilities" character varying(512) DEFAULT 'multi_group,custom_base_url,has_models'::character varying NOT NULL;
ALTER TABLE "public"."user_selected_models" ADD COLUMN "share" boolean DEFAULT false NOT NULL;

-- Add final constraints and indexes after all columns are ready.
CREATE INDEX idx_agent_thread_records_thread_step_stream_id ON public.agent_thread_records USING btree (thread_id, step_id, stream_kind, id);
ALTER TABLE "public"."chat_histories" ADD CONSTRAINT "chk_chat_histories_tool_call_turns_non_negative" CHECK (tool_call_turns >= 0);
CREATE INDEX idx_chat_histories_conversation_create_time ON public.chat_histories USING btree (conversation_id, create_time);
CREATE INDEX idx_conversations_is_task_conv ON public.conversations USING btree (is_task_conv);
CREATE INDEX idx_conversations_user_not_deleted ON public.conversations USING btree (create_user_id, id) WHERE (deleted_at IS NULL);
ALTER TABLE "public"."multi_answers_chat_histories" ADD CONSTRAINT "chk_multi_answers_chat_histories_tool_call_turns_non_negative" CHECK (tool_call_turns >= 0);
CREATE INDEX idx_skill_share_items_source_skill ON public.skill_share_items USING btree (source_skill_id);
CREATE INDEX idx_uploaded_files_reusable_hash ON public.uploaded_files (create_user_id, content_hash)
    WHERE deleted_at IS NULL AND content_hash <> '' AND status IN ('UPLOADED', 'BOUND');
CREATE UNIQUE INDEX uk_user_selected_models_shared_model ON public.user_selected_models USING btree (model_type) WHERE (share = true);

-- Objects introduced after init, emitted once in their final form.

--
-- Name: agent_thread_steps; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.agent_thread_steps (
    thread_id character varying(128) NOT NULL,
    step_id character varying(128) NOT NULL,
    title character varying(255) DEFAULT ''::character varying NOT NULL,
    status character varying(32) DEFAULT 'running'::character varying NOT NULL,
    active boolean DEFAULT false NOT NULL,
    order_index integer DEFAULT 0 NOT NULL,
    event_count bigint DEFAULT 0 NOT NULL,
    current_task_id character varying(128) DEFAULT ''::character varying NOT NULL,
    started_at timestamp with time zone,
    ended_at timestamp with time zone,
    created_at timestamp with time zone NOT NULL,
    updated_at timestamp with time zone NOT NULL,
    stage character varying(32) DEFAULT ''::character varying NOT NULL,
    next_step_id character varying(128) DEFAULT ''::character varying NOT NULL,
    version integer
);


--
-- Name: async_jobs; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.async_jobs (
    id character varying(64) NOT NULL,
    job_type character varying(64) NOT NULL,
    status character varying(32) NOT NULL,
    resource_type character varying(64) DEFAULT ''::character varying NOT NULL,
    resource_id character varying(128) DEFAULT ''::character varying NOT NULL,
    idempotency_key character varying(128) DEFAULT ''::character varying NOT NULL,
    payload_json json,
    result_json json,
    error_code character varying(64) DEFAULT ''::character varying NOT NULL,
    error_message text DEFAULT ''::text NOT NULL,
    error_details_json json,
    progress_current bigint DEFAULT 0 NOT NULL,
    progress_total bigint DEFAULT 0 NOT NULL,
    attempt_count integer DEFAULT 0 NOT NULL,
    max_attempts integer DEFAULT 1 NOT NULL,
    next_run_at timestamp with time zone NOT NULL,
    locked_by character varying(128) DEFAULT ''::character varying NOT NULL,
    lock_until timestamp with time zone,
    started_at timestamp with time zone,
    finished_at timestamp with time zone,
    heartbeat_at timestamp with time zone,
    create_user_id character varying(255) DEFAULT ''::character varying NOT NULL,
    create_user_name character varying(255) DEFAULT ''::character varying NOT NULL,
    created_at timestamp with time zone NOT NULL,
    updated_at timestamp with time zone NOT NULL,
    CONSTRAINT chk_async_jobs_status CHECK (((status)::text = ANY (ARRAY['pending'::text, 'running'::text, 'succeeded'::text, 'failed'::text, 'canceled'::text])))
);


--
-- Name: automation_groups; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.automation_groups (
    id character varying(36) NOT NULL,
    user_id character varying(255) NOT NULL,
    name character varying(128) NOT NULL,
    remark text DEFAULT ''::text NOT NULL,
    timezone character varying(64) DEFAULT 'Asia/Shanghai'::character varying NOT NULL,
    enabled boolean DEFAULT true NOT NULL,
    created_at timestamp with time zone NOT NULL,
    updated_at timestamp with time zone NOT NULL
);


--
-- Name: conversation_artifacts; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.conversation_artifacts (
    id character varying(36) NOT NULL,
    conversation_id character varying(36) NOT NULL,
    history_id character varying(36) NOT NULL,
    filename character varying(255) NOT NULL,
    slot character varying(255) NOT NULL,
    content_type character varying(32) NOT NULL,
    value jsonb NOT NULL,
    caption text,
    create_user_id character varying(255) NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    CONSTRAINT chk_conversation_artifacts_content_type CHECK (content_type IN ('text', 'json', 'file')),
    CONSTRAINT chk_conversation_artifacts_filename CHECK ((length(btrim((filename)::text)) > 0))
);


--
-- Name: conversation_idle_events; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.conversation_idle_events (
    id character varying(36) NOT NULL,
    event_id character varying(512) NOT NULL,
    session_id character varying(128) NOT NULL,
    user_id character varying(255) NOT NULL,
    last_message_id character varying(128) NOT NULL,
    last_activity_at timestamp with time zone NOT NULL,
    due_at timestamp with time zone NOT NULL,
    status character varying(32) NOT NULL,
    skip_reason character varying(128) DEFAULT ''::character varying NOT NULL,
    error_code character varying(64) DEFAULT ''::character varying NOT NULL,
    error_message text DEFAULT ''::text NOT NULL,
    memory_task_id character varying(36) DEFAULT ''::character varying NOT NULL,
    user_preference_task_id character varying(36) DEFAULT ''::character varying NOT NULL,
    created_at timestamp with time zone NOT NULL,
    updated_at timestamp with time zone NOT NULL,
    triggered_at timestamp with time zone,
    CONSTRAINT chk_conversation_idle_events_status CHECK (((status)::text = ANY (ARRAY['waiting'::text, 'processing'::text, 'triggered'::text, 'skipped'::text, 'failed'::text])))
);


--
-- Name: eval_set_import_previews; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.eval_set_import_previews (
    token character varying(64) NOT NULL,
    status character varying(32) DEFAULT 'ready'::character varying NOT NULL,
    file_name character varying(512) DEFAULT ''::character varying NOT NULL,
    file_type character varying(16) NOT NULL,
    temp_path text DEFAULT ''::text NOT NULL,
    total_rows bigint DEFAULT 0 NOT NULL,
    empty_rows bigint DEFAULT 0 NOT NULL,
    valid_rows bigint DEFAULT 0 NOT NULL,
    preview_rows_json json,
    error_details_json json,
    create_user_id character varying(255) NOT NULL,
    create_user_name character varying(255) DEFAULT ''::character varying NOT NULL,
    created_at timestamp with time zone NOT NULL,
    expires_at timestamp with time zone NOT NULL,
    consumed_at timestamp with time zone,
    CONSTRAINT chk_eval_set_import_previews_status CHECK (((status)::text = ANY (ARRAY['ready'::text, 'consumed'::text, 'expired'::text])))
);


--
-- Name: eval_set_items; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.eval_set_items (
    id character varying(64) NOT NULL,
    shard_id character varying(64) NOT NULL,
    eval_set_id character varying(64) NOT NULL,
    case_id character varying(255) DEFAULT ''::character varying NOT NULL,
    question text NOT NULL,
    ground_truth text NOT NULL,
    question_type character varying(128) NOT NULL,
    generate_reason text DEFAULT ''::text NOT NULL,
    key_points text DEFAULT ''::text NOT NULL,
    reference_chunk_ids text DEFAULT ''::text NOT NULL,
    reference_context text DEFAULT ''::text NOT NULL,
    algorithm_reference_context text DEFAULT ''::text NOT NULL,
    reference_doc text DEFAULT ''::text NOT NULL,
    reference_doc_ids text DEFAULT ''::text NOT NULL,
    is_deleted boolean DEFAULT false NOT NULL,
    estimated_bytes bigint DEFAULT 0 NOT NULL,
    source character varying(32) NOT NULL,
    source_session_id character varying(128) DEFAULT ''::character varying NOT NULL,
    source_history_id character varying(128) DEFAULT ''::character varying NOT NULL,
    create_user_id character varying(255) NOT NULL,
    create_user_name character varying(255) DEFAULT ''::character varying NOT NULL,
    created_at timestamp with time zone NOT NULL,
    updated_at timestamp with time zone NOT NULL,
    CONSTRAINT chk_eval_set_items_source CHECK (((source)::text = ANY (ARRAY['upload'::text, 'manual'::text, 'flowback'::text])))
)
PARTITION BY LIST (shard_id);


--
-- Name: COLUMN eval_set_items.is_deleted; Type: COMMENT; Schema: public; Owner: -
--

COMMENT ON COLUMN public.eval_set_items.is_deleted IS 'Template/business field imported from eval-set files; not a logical-delete marker. System deletion is physical DELETE.';


--
-- Name: eval_set_items_p_eval_shard_0001; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.eval_set_items_p_eval_shard_0001 (
    id character varying(64) NOT NULL,
    shard_id character varying(64) NOT NULL,
    eval_set_id character varying(64) NOT NULL,
    case_id character varying(255) DEFAULT ''::character varying NOT NULL,
    question text NOT NULL,
    ground_truth text NOT NULL,
    question_type character varying(128) NOT NULL,
    generate_reason text DEFAULT ''::text NOT NULL,
    key_points text DEFAULT ''::text NOT NULL,
    reference_chunk_ids text DEFAULT ''::text NOT NULL,
    reference_context text DEFAULT ''::text NOT NULL,
    algorithm_reference_context text DEFAULT ''::text NOT NULL,
    reference_doc text DEFAULT ''::text NOT NULL,
    reference_doc_ids text DEFAULT ''::text NOT NULL,
    is_deleted boolean DEFAULT false NOT NULL,
    estimated_bytes bigint DEFAULT 0 NOT NULL,
    source character varying(32) NOT NULL,
    source_session_id character varying(128) DEFAULT ''::character varying NOT NULL,
    source_history_id character varying(128) DEFAULT ''::character varying NOT NULL,
    create_user_id character varying(255) NOT NULL,
    create_user_name character varying(255) DEFAULT ''::character varying NOT NULL,
    created_at timestamp with time zone NOT NULL,
    updated_at timestamp with time zone NOT NULL,
    CONSTRAINT chk_eval_set_items_source CHECK (((source)::text = ANY (ARRAY['upload'::text, 'manual'::text, 'flowback'::text])))
);


--
-- Name: eval_set_shards; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.eval_set_shards (
    id character varying(64) NOT NULL,
    status character varying(32) DEFAULT 'open'::character varying NOT NULL,
    row_limit bigint DEFAULT 200000 NOT NULL,
    row_open_threshold bigint DEFAULT 120000 NOT NULL,
    size_limit_bytes bigint DEFAULT '8589934592'::bigint NOT NULL,
    size_open_threshold_bytes bigint DEFAULT '5368709120'::bigint NOT NULL,
    actual_rows bigint DEFAULT 0 NOT NULL,
    estimated_bytes bigint DEFAULT 0 NOT NULL,
    created_at timestamp with time zone NOT NULL,
    updated_at timestamp with time zone NOT NULL,
    sealed_at timestamp with time zone,
    CONSTRAINT chk_eval_set_shards_status CHECK (((status)::text = ANY (ARRAY['open'::text, 'sealed'::text])))
);


--
-- Name: eval_sets; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.eval_sets (
    id character varying(64) NOT NULL,
    name character varying(255) NOT NULL,
    description text DEFAULT ''::text NOT NULL,
    dataset_ids jsonb DEFAULT '[]'::jsonb NOT NULL,
    owner_id character varying(255) NOT NULL,
    group_id character varying(255) DEFAULT ''::character varying NOT NULL,
    shard_id character varying(64) NOT NULL,
    status character varying(32) DEFAULT 'active'::character varying NOT NULL,
    item_count bigint DEFAULT 0 NOT NULL,
    create_user_id character varying(255) NOT NULL,
    create_user_name character varying(255) DEFAULT ''::character varying NOT NULL,
    created_at timestamp with time zone NOT NULL,
    updated_at timestamp with time zone NOT NULL,
    CONSTRAINT chk_eval_sets_status CHECK (((status)::text = ANY (ARRAY['active'::text, 'importing'::text, 'failed'::text])))
);


--
-- Name: external_database_connections; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.external_database_connections (
    id character varying(64) NOT NULL,
    display_name character varying(255) NOT NULL,
    description text DEFAULT ''::text NOT NULL,
    db_type character varying(32) NOT NULL,
    host character varying(255) NOT NULL,
    port integer NOT NULL,
    database_name character varying(255) NOT NULL,
    username character varying(255) NOT NULL,
    password_json json NOT NULL,
    options_json json NOT NULL,
    is_verified boolean DEFAULT false NOT NULL,
    last_checked_at timestamp with time zone,
    last_check_error text DEFAULT ''::text NOT NULL,
    create_user_id character varying(255) NOT NULL,
    create_user_name character varying(255) NOT NULL,
    created_at timestamp with time zone NOT NULL,
    updated_at timestamp with time zone NOT NULL,
    deleted_at timestamp with time zone
);


--
-- Name: local_fs_chat_settings; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.local_fs_chat_settings (
    id bigint NOT NULL,
    create_user_id character varying(255) NOT NULL,
    create_user_name character varying(255) DEFAULT ''::character varying NOT NULL,
    enabled boolean DEFAULT false NOT NULL,
    created_at timestamp with time zone NOT NULL,
    updated_at timestamp with time zone NOT NULL
);


--
-- Name: local_fs_chat_settings_id_seq; Type: SEQUENCE; Schema: public; Owner: -
--

CREATE SEQUENCE public.local_fs_chat_settings_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;


--
-- Name: local_fs_chat_settings_id_seq; Type: SEQUENCE OWNED BY; Schema: public; Owner: -
--

ALTER SEQUENCE public.local_fs_chat_settings_id_seq OWNED BY public.local_fs_chat_settings.id;


--
-- Name: mcp_server_tools; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.mcp_server_tools (
    id character varying(64) NOT NULL,
    mcp_server_id character varying(64) NOT NULL,
    tool_name character varying(255) NOT NULL,
    description text DEFAULT ''::text NOT NULL,
    input_schema_json json DEFAULT '{}'::json NOT NULL,
    last_discovered_at timestamp with time zone NOT NULL,
    created_at timestamp with time zone NOT NULL,
    updated_at timestamp with time zone NOT NULL,
    deleted_at timestamp with time zone
);


--
-- Name: mcp_servers; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.mcp_servers (
    id character varying(64) NOT NULL,
    create_user_id character varying(255) NOT NULL,
    create_user_name character varying(255) DEFAULT ''::character varying NOT NULL,
    name character varying(255) NOT NULL,
    transport character varying(32) NOT NULL,
    url text DEFAULT ''::text NOT NULL,
    headers_json json DEFAULT '{}'::json NOT NULL,
    allowed_tools_json json DEFAULT '[]'::json NOT NULL,
    enabled boolean DEFAULT false NOT NULL,
    is_verified boolean DEFAULT false NOT NULL,
    share boolean DEFAULT false NOT NULL,
    timeout integer DEFAULT 5 NOT NULL,
    created_at timestamp with time zone NOT NULL,
    updated_at timestamp with time zone NOT NULL,
    deleted_at timestamp with time zone
);


--
-- Name: memory_review; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.memory_review (
    id text NOT NULL,
    user_id text DEFAULT ''::text NOT NULL,
    target text NOT NULL,
    session_id text NOT NULL,
    source_content text DEFAULT ''::text NOT NULL,
    content text DEFAULT ''::text NOT NULL,
    operations jsonb DEFAULT '[]'::jsonb NOT NULL,
    state text DEFAULT 'success'::text NOT NULL,
    review_status text DEFAULT 'pending'::text NOT NULL,
    "time" timestamp with time zone DEFAULT CURRENT_TIMESTAMP NOT NULL,
    CONSTRAINT chk_memory_review_review_status CHECK ((review_status = ANY (ARRAY['pending'::text, 'accepted'::text, 'rejected'::text, 'expired'::text]))),
    CONSTRAINT chk_memory_review_state CHECK ((state = 'success'::text)),
    CONSTRAINT chk_memory_review_target CHECK ((target = ANY (ARRAY['memory'::text, 'user_preference'::text])))
);


--
-- Name: personal_resource_blobs; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.personal_resource_blobs (
    hash character varying(64) NOT NULL,
    size bigint NOT NULL,
    mime character varying(128),
    file_type character varying(32) DEFAULT 'unknown'::character varying NOT NULL,
    "binary" boolean DEFAULT false NOT NULL,
    storage_backend character varying(32) NOT NULL,
    storage_key text,
    content bytea,
    created_at timestamp without time zone NOT NULL,
    CONSTRAINT chk_personal_resource_blob_storage_backend CHECK (storage_backend IN ('postgres', 'local_file', 's3'))
);


--
-- Name: personal_resource_drafts; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.personal_resource_drafts (
    resource_id character varying(36) NOT NULL,
    base_revision_id character varying(36),
    path character varying(1024) NOT NULL,
    blob_hash character varying(64) NOT NULL,
    content_hash character varying(64) NOT NULL,
    size bigint DEFAULT 0 NOT NULL,
    mime character varying(128),
    file_type character varying(32) DEFAULT 'unknown'::character varying NOT NULL,
    "binary" boolean DEFAULT false NOT NULL,
    draft_status character varying(32) DEFAULT ''::character varying NOT NULL,
    draft_updated_at timestamp without time zone,
    task_id character varying(128) DEFAULT ''::character varying NOT NULL,
    conversation_id character varying(128),
    updated_by character varying(255),
    version bigint DEFAULT 1 NOT NULL,
    created_at timestamp without time zone NOT NULL,
    updated_at timestamp without time zone NOT NULL
);


--
-- Name: personal_resource_review_action_batches; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.personal_resource_review_action_batches (
    id character varying(36) NOT NULL,
    session_id character varying(36) NOT NULL,
    resource_id character varying(36) NOT NULL,
    before_draft_blob_hash character varying(64) NOT NULL,
    after_draft_blob_hash character varying(64) NOT NULL,
    before_draft_version bigint NOT NULL,
    after_draft_version bigint NOT NULL,
    review_version bigint NOT NULL,
    created_by character varying(255),
    created_at timestamp without time zone NOT NULL
);


--
-- Name: personal_resource_review_action_items; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.personal_resource_review_action_items (
    id character varying(36) NOT NULL,
    batch_id character varying(36) NOT NULL,
    hunk_id character varying(128) NOT NULL,
    decision character varying(16) NOT NULL,
    old_start integer DEFAULT 0 NOT NULL,
    old_lines integer DEFAULT 0 NOT NULL,
    new_start integer DEFAULT 0 NOT NULL,
    new_lines integer DEFAULT 0 NOT NULL,
    created_at timestamp without time zone NOT NULL,
    CONSTRAINT chk_personal_resource_review_action_decision CHECK (decision IN ('accept', 'reject', 'accepted', 'rejected'))
);


--
-- Name: personal_resource_review_sessions; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.personal_resource_review_sessions (
    id character varying(36) NOT NULL,
    resource_id character varying(36) NOT NULL,
    path character varying(1024) NOT NULL,
    base_revision_id character varying(36) NOT NULL,
    head_revision_id character varying(36) NOT NULL,
    draft_version bigint NOT NULL,
    draft_blob_hash character varying(64) NOT NULL,
    review_version bigint DEFAULT 1 NOT NULL,
    status character varying(32) DEFAULT 'active'::character varying NOT NULL,
    created_by character varying(255),
    created_at timestamp without time zone NOT NULL,
    updated_at timestamp without time zone NOT NULL
);


--
-- Name: personal_resource_revisions; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.personal_resource_revisions (
    id character varying(36) NOT NULL,
    resource_id character varying(36) NOT NULL,
    parent_revision_id character varying(36),
    revision_no bigint NOT NULL,
    path character varying(1024) NOT NULL,
    blob_hash character varying(64) NOT NULL,
    content_hash character varying(64) NOT NULL,
    size bigint DEFAULT 0 NOT NULL,
    mime character varying(128),
    file_type character varying(32) DEFAULT 'unknown'::character varying NOT NULL,
    "binary" boolean DEFAULT false NOT NULL,
    message text,
    change_source character varying(32) DEFAULT 'draft_commit'::character varying NOT NULL,
    source_ref_type character varying(64) DEFAULT ''::character varying NOT NULL,
    source_ref_id character varying(128) DEFAULT ''::character varying NOT NULL,
    created_by character varying(255),
    created_at timestamp without time zone NOT NULL
);


--
-- Name: personal_resources; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.personal_resources (
    id character varying(36) NOT NULL,
    user_id character varying(255) NOT NULL,
    resource_type character varying(64) NOT NULL,
    head_revision_id character varying(36),
    version bigint DEFAULT 1 NOT NULL,
    created_at timestamp without time zone NOT NULL,
    updated_at timestamp without time zone NOT NULL,
    auto_evo boolean DEFAULT true NOT NULL,
    auto_evo_apply_status character varying(32) DEFAULT 'idle'::character varying NOT NULL,
    auto_evo_generation bigint DEFAULT 0 NOT NULL,
    auto_evo_started_at timestamp without time zone,
    auto_evo_finished_at timestamp without time zone,
    auto_evo_error text DEFAULT ''::text NOT NULL,
    ext json,
    updated_by character varying(255) DEFAULT ''::character varying NOT NULL,
    updated_by_name character varying(255) DEFAULT ''::character varying NOT NULL,
    CONSTRAINT chk_personal_resources_type CHECK (resource_type IN ('memory', 'user_preference'))
);


--
-- Name: plugin_attempt_input_bindings; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.plugin_attempt_input_bindings (
    id character varying(36) NOT NULL,
    session_id character varying(36) NOT NULL,
    attempt_id character varying(36) NOT NULL,
    material_id character varying(64) NOT NULL,
    material_revision_id character varying(36) NOT NULL,
    bind_as character varying(64) DEFAULT ''::character varying NOT NULL,
    created_at timestamp without time zone NOT NULL
);


--
-- Name: plugin_blobs; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.plugin_blobs (
    hash character varying(64) NOT NULL,
    size bigint NOT NULL,
    mime character varying(128),
    file_type character varying(32) DEFAULT 'unknown'::character varying NOT NULL,
    is_binary boolean DEFAULT false NOT NULL,
    content bytea NOT NULL,
    created_at timestamp without time zone NOT NULL
);


--
-- Name: plugin_drafts; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.plugin_drafts (
    id character varying(36) NOT NULL,
    name character varying(255) DEFAULT ''::character varying NOT NULL,
    content text DEFAULT ''::text NOT NULL,
    created_by character varying(255) DEFAULT ''::character varying NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    plugin_yaml_content text DEFAULT ''::text NOT NULL,
    state_yaml_content text DEFAULT ''::text NOT NULL,
    scenario_content text DEFAULT ''::text NOT NULL,
    scripts_content text DEFAULT '{}'::text NOT NULL,
    generate_status character varying(32) DEFAULT ''::character varying NOT NULL,
    generate_error text DEFAULT ''::text NOT NULL,
    state_layout_content text DEFAULT ''::text NOT NULL,
    version integer DEFAULT 1 NOT NULL,
    generate_warning text DEFAULT ''::text NOT NULL,
    source_type character varying(16) DEFAULT ''::character varying NOT NULL,
    source_skill_id character varying(36) DEFAULT ''::character varying NOT NULL,
    source_skill_name character varying(255) DEFAULT ''::character varying NOT NULL,
    design_brief_content text DEFAULT ''::text NOT NULL,
    plugin_id character varying(255) DEFAULT ''::character varying NOT NULL,
    base_revision_id character varying(36) DEFAULT ''::character varying NOT NULL,
    source_skill_revision_id character varying(36) DEFAULT ''::character varying NOT NULL,
    source_skill_revision_no bigint DEFAULT 0 NOT NULL,
    source_skill_tree_hash character varying(64) DEFAULT ''::character varying NOT NULL,
    source_analysis_id character varying(36) DEFAULT ''::character varying NOT NULL
);


--
-- Name: plugin_generation_analyses; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.plugin_generation_analyses (
    id character varying(36) NOT NULL,
    draft_id character varying(36) NOT NULL,
    user_id character varying(255) NOT NULL,
    source_type character varying(16) NOT NULL,
    source_skill_id character varying(36) DEFAULT ''::character varying NOT NULL,
    source_skill_revision_id character varying(36) DEFAULT ''::character varying NOT NULL,
    source_skill_revision_no bigint DEFAULT 0 NOT NULL,
    source_skill_tree_hash character varying(64) DEFAULT ''::character varying NOT NULL,
    status character varying(32) NOT NULL,
    verdict_code character varying(64) DEFAULT ''::character varying NOT NULL,
    verdict_message text DEFAULT ''::text NOT NULL,
    candidates_json text DEFAULT '[]'::text NOT NULL,
    selected_candidate_id character varying(128) DEFAULT ''::character varying NOT NULL,
    coverage_report_json text DEFAULT '{}'::text NOT NULL,
    tool_mapping_report_json text DEFAULT '{}'::text NOT NULL,
    script_report_json text DEFAULT '{}'::text NOT NULL,
    source_package_json text DEFAULT '{}'::text NOT NULL,
    created_at timestamp without time zone NOT NULL,
    updated_at timestamp without time zone NOT NULL
);


--
-- Name: plugin_human_artifacts; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.plugin_human_artifacts (
    id character varying(36) NOT NULL,
    session_id character varying(36) NOT NULL,
    slot character varying(64) NOT NULL,
    content_type character varying(32) NOT NULL,
    value jsonb NOT NULL,
    caption text,
    created_at timestamp with time zone NOT NULL
);


--
-- Name: plugin_repair_runs; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.plugin_repair_runs (
    id character varying(36) NOT NULL,
    draft_id character varying(36) NOT NULL,
    user_id character varying(255) NOT NULL,
    base_plugin_revision_id character varying(36) DEFAULT ''::character varying NOT NULL,
    draft_version_before integer NOT NULL,
    target character varying(32) NOT NULL,
    mode character varying(32) NOT NULL,
    source_analysis_id character varying(36) DEFAULT ''::character varying NOT NULL,
    source_skill_revision_id character varying(36) DEFAULT ''::character varying NOT NULL,
    repair_hint text DEFAULT ''::text NOT NULL,
    diagnostics_before_json text DEFAULT '{}'::text NOT NULL,
    changes_json text DEFAULT '{}'::text NOT NULL,
    diagnostics_after_json text DEFAULT '{}'::text NOT NULL,
    status character varying(32) NOT NULL,
    created_at timestamp without time zone NOT NULL,
    updated_at timestamp without time zone NOT NULL
);


--
-- Name: plugin_revision_entries; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.plugin_revision_entries (
    revision_id character varying(36) NOT NULL,
    path character varying(1024) NOT NULL,
    entry_type character varying(16) DEFAULT 'file'::character varying NOT NULL,
    blob_hash character varying(64),
    size bigint DEFAULT 0 NOT NULL,
    mime character varying(128),
    file_type character varying(32) DEFAULT 'unknown'::character varying NOT NULL,
    is_binary boolean DEFAULT false NOT NULL,
    mode integer DEFAULT 420 NOT NULL
);


--
-- Name: plugin_revisions; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.plugin_revisions (
    id character varying(36) NOT NULL,
    plugin_resource_id character varying(36) NOT NULL,
    parent_revision_id character varying(36),
    revision_no bigint NOT NULL,
    tree_hash character varying(64) NOT NULL,
    message text DEFAULT ''::text NOT NULL,
    created_by character varying(255),
    created_at timestamp without time zone NOT NULL,
    compiled_graph jsonb,
    graph_hash character varying(64) DEFAULT ''::character varying NOT NULL,
    graph_schema_version character varying(16) DEFAULT ''::character varying NOT NULL
);


--
-- Name: plugin_route_decisions; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.plugin_route_decisions (
    id character varying(36) NOT NULL,
    session_id character varying(36) NOT NULL,
    from_step_id character varying(64) NOT NULL,
    source_attempt_id character varying(36) DEFAULT ''::character varying NOT NULL,
    activated_json jsonb DEFAULT '[]'::jsonb NOT NULL,
    pruned_json jsonb DEFAULT '[]'::jsonb NOT NULL,
    bypassed_json jsonb DEFAULT '[]'::jsonb NOT NULL,
    witness_json jsonb DEFAULT '[]'::jsonb NOT NULL,
    validity character varying(16) DEFAULT 'effective'::character varying NOT NULL,
    state_version bigint NOT NULL,
    created_at timestamp without time zone NOT NULL
);


--
-- Name: plugin_run_outbox; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.plugin_run_outbox (
    task_id character varying(36) NOT NULL,
    payload jsonb NOT NULL,
    status character varying(16) NOT NULL,
    last_error text DEFAULT ''::text NOT NULL,
    created_at timestamp without time zone NOT NULL,
    updated_at timestamp without time zone NOT NULL
);


--
-- Name: plugin_session_steps; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.plugin_session_steps (
    id character varying(36) NOT NULL,
    session_id character varying(36) NOT NULL,
    step_id character varying(64) NOT NULL,
    attempt integer DEFAULT 1 NOT NULL,
    task_id character varying(36) NOT NULL,
    status character varying(16) DEFAULT 'pending'::character varying NOT NULL,
    created_at timestamp with time zone DEFAULT CURRENT_TIMESTAMP NOT NULL,
    updated_at timestamp with time zone DEFAULT CURRENT_TIMESTAMP NOT NULL,
    validity character varying(16) DEFAULT 'effective'::character varying NOT NULL
);


--
-- Name: plugin_sessions; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.plugin_sessions (
    id character varying(36) NOT NULL,
    conversation_id character varying(36) NOT NULL,
    plugin_id character varying(64) NOT NULL,
    trigger_history_id character varying(36),
    status character varying(16) DEFAULT 'active'::character varying NOT NULL,
    current_step_id character varying(64),
    create_user_id character varying(255) DEFAULT ''::character varying NOT NULL,
    created_at timestamp with time zone DEFAULT CURRENT_TIMESTAMP NOT NULL,
    updated_at timestamp with time zone DEFAULT CURRENT_TIMESTAMP NOT NULL,
    intent_context text DEFAULT '{}'::text NOT NULL,
    dismissed boolean DEFAULT false NOT NULL,
    plugin_ref character varying(512) DEFAULT ''::character varying NOT NULL,
    plugin_revision_id character varying(36) DEFAULT ''::character varying NOT NULL,
    plugin_revision_no bigint DEFAULT 0 NOT NULL,
    plugin_tree_hash character varying(64) DEFAULT ''::character varying NOT NULL,
    plugin_remote_root character varying(1024) DEFAULT ''::character varying NOT NULL,
    state_version bigint DEFAULT 0 NOT NULL,
    graph_hash character varying(64) DEFAULT ''::character varying NOT NULL,
    graph_schema_version character varying(16) DEFAULT ''::character varying NOT NULL
);


--
-- Name: plugin_slot_order; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.plugin_slot_order (
    session_id character varying(36) NOT NULL,
    slot_id character varying(64) NOT NULL,
    order_list jsonb DEFAULT '[]'::jsonb NOT NULL,
    order_version integer DEFAULT 0 NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL
);


--
-- Name: plugin_slot_revisions; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.plugin_slot_revisions (
    id character varying(36) NOT NULL,
    session_id character varying(36) NOT NULL,
    slot_id character varying(64) NOT NULL,
    revision integer NOT NULL,
    list_index integer,
    selected boolean DEFAULT true NOT NULL,
    slot character varying(255) NOT NULL,
    step_id character varying(64) NOT NULL,
    attempt integer NOT NULL,
    created_at timestamp with time zone DEFAULT CURRENT_TIMESTAMP NOT NULL,
    content_snapshot jsonb,
    change_source character varying(16) DEFAULT 'ai'::character varying NOT NULL,
    artifact_seq integer,
    human_artifact_id character varying(36),
    validity character varying(16) DEFAULT 'effective'::character varying NOT NULL,
    producer_attempt_id character varying(36) DEFAULT ''::character varying NOT NULL
);


--
-- Name: plugin_step_intents; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.plugin_step_intents (
    id character varying(36) NOT NULL,
    session_id character varying(36) NOT NULL,
    step_id character varying(64) NOT NULL,
    intent_context text DEFAULT '{}'::text NOT NULL,
    updated_at timestamp with time zone DEFAULT CURRENT_TIMESTAMP NOT NULL
);


--
-- Name: plugin_transition_commands; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.plugin_transition_commands (
    command_id character varying(36) NOT NULL,
    session_id character varying(36) DEFAULT ''::character varying NOT NULL,
    operation character varying(16) NOT NULL,
    target_step_id character varying(64) DEFAULT ''::character varying NOT NULL,
    status character varying(16) NOT NULL,
    task_id character varying(36) DEFAULT ''::character varying NOT NULL,
    expected_state_version bigint DEFAULT 0 NOT NULL,
    resulting_state_version bigint DEFAULT 0 NOT NULL,
    response_json jsonb NOT NULL,
    created_at timestamp without time zone NOT NULL,
    updated_at timestamp without time zone NOT NULL
);


--
-- Name: plugins; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.plugins (
    id character varying(36) NOT NULL,
    plugin_ref character varying(512) NOT NULL,
    plugin_id character varying(255) NOT NULL,
    owner_user_id character varying(255) NOT NULL,
    owner_scope character varying(128) NOT NULL,
    source_type character varying(16) DEFAULT 'user'::character varying NOT NULL,
    relative_root character varying(1024) NOT NULL,
    name character varying(255) DEFAULT ''::character varying NOT NULL,
    description text DEFAULT ''::text NOT NULL,
    when_to_use text DEFAULT ''::text NOT NULL,
    head_revision_id character varying(36),
    version bigint DEFAULT 0 NOT NULL,
    status character varying(16) DEFAULT 'active'::character varying NOT NULL,
    contains_scripts boolean DEFAULT false NOT NULL,
    created_at timestamp without time zone NOT NULL,
    updated_at timestamp without time zone NOT NULL
);


--
-- Name: prompt_categories; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.prompt_categories (
    id character varying(64) NOT NULL,
    name character varying(64) NOT NULL,
    create_user_id character varying(255) NOT NULL,
    create_user_name character varying(255) DEFAULT ''::character varying NOT NULL,
    created_at timestamp with time zone NOT NULL,
    updated_at timestamp with time zone NOT NULL,
    deleted_at timestamp with time zone
);


--
-- Name: prompt_user_states; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.prompt_user_states (
    id character varying(64) NOT NULL,
    prompt_id character varying(64) NOT NULL,
    is_favorite boolean DEFAULT false NOT NULL,
    usage_count bigint DEFAULT 0 NOT NULL,
    last_used_at timestamp with time zone,
    create_user_id character varying(255) NOT NULL,
    create_user_name character varying(255) DEFAULT ''::character varying NOT NULL,
    created_at timestamp with time zone NOT NULL,
    updated_at timestamp with time zone NOT NULL,
    deleted_at timestamp with time zone
);


--
-- Name: resource_update_tasks; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.resource_update_tasks (
    id character varying(36) NOT NULL,
    task_type character varying(32) NOT NULL,
    resource_type character varying(32) NOT NULL,
    user_id character varying(255) DEFAULT ''::character varying NOT NULL,
    resource_id character varying(128) DEFAULT ''::character varying NOT NULL,
    trigger_type character varying(32) NOT NULL,
    trigger_id character varying(512) NOT NULL,
    status character varying(32) NOT NULL,
    request_json json,
    review_result_id character varying(128),
    result_id character varying(128),
    error_code character varying(64) DEFAULT ''::character varying NOT NULL,
    error_message text DEFAULT ''::text NOT NULL,
    attempt_count integer DEFAULT 0 NOT NULL,
    next_run_at timestamp with time zone NOT NULL,
    locked_by character varying(128) DEFAULT ''::character varying NOT NULL,
    locked_until timestamp with time zone,
    created_at timestamp with time zone NOT NULL,
    updated_at timestamp with time zone NOT NULL,
    started_at timestamp with time zone,
    finished_at timestamp with time zone,
    CONSTRAINT chk_resource_update_tasks_attempt_count_non_negative CHECK ((attempt_count >= 0)),
    CONSTRAINT chk_resource_update_tasks_resource_type CHECK (((resource_type)::text = ANY (ARRAY['skill'::text, 'memory'::text, 'user_preference'::text]))),
    CONSTRAINT chk_resource_update_tasks_status CHECK (((status)::text = ANY (ARRAY['pending'::text, 'running'::text, 'done'::text, 'failed'::text, 'skipped'::text]))),
    CONSTRAINT chk_resource_update_tasks_trigger_type CHECK (((trigger_type)::text = ANY (ARRAY['scheduled'::text, 'conversation_idle'::text, 'manual'::text, 'review_result'::text, 'auto_evo_enabled'::text])))
);


--
-- Name: schedule_dependencies; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.schedule_dependencies (
    id character varying(36) NOT NULL,
    user_id character varying(255) NOT NULL,
    source_schedule_id character varying(36) NOT NULL,
    target_schedule_id character varying(36) NOT NULL,
    window_type character varying(32) DEFAULT 'between_target_fires'::character varying NOT NULL,
    window_config_json text,
    content_types_json text,
    incomplete_policy character varying(48) DEFAULT 'wait_then_run_with_warning'::character varying NOT NULL,
    max_wait_seconds integer DEFAULT 7200 NOT NULL,
    enabled boolean DEFAULT true NOT NULL,
    created_at timestamp with time zone NOT NULL,
    updated_at timestamp with time zone NOT NULL,
    CONSTRAINT chk_schedule_dependency_distinct CHECK (((source_schedule_id)::text <> (target_schedule_id)::text))
);


--
-- Name: skill_blobs; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.skill_blobs (
    hash character varying(64) NOT NULL,
    size bigint NOT NULL,
    mime character varying(128),
    file_type character varying(32) DEFAULT 'unknown'::character varying NOT NULL,
    "binary" boolean DEFAULT false NOT NULL,
    storage_backend character varying(32) NOT NULL,
    storage_key text,
    content bytea,
    created_at timestamp without time zone NOT NULL,
    CONSTRAINT chk_skill_blob_storage_backend CHECK (storage_backend IN ('postgres', 'local_file', 's3')),
    CONSTRAINT chk_skill_blob_storage_shape CHECK (
        ("binary" = false AND storage_backend = 'postgres' AND content IS NOT NULL AND storage_key IS NULL)
        OR ("binary" = true AND storage_backend IN ('local_file', 's3') AND content IS NULL AND storage_key IS NOT NULL)
    )
);


--
-- Name: skill_draft_entries; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.skill_draft_entries (
    skill_id character varying(36) NOT NULL,
    path character varying(1024) NOT NULL,
    op character varying(16) NOT NULL,
    entry_type character varying(16),
    blob_hash character varying(64),
    size bigint DEFAULT 0 NOT NULL,
    mime character varying(128),
    file_type character varying(32),
    "binary" boolean DEFAULT false NOT NULL,
    mode integer DEFAULT 420 NOT NULL,
    updated_at timestamp without time zone NOT NULL,
    CONSTRAINT chk_skill_draft_entry_op CHECK (op IN ('upsert', 'delete')),
    CONSTRAINT chk_skill_draft_entry_shape CHECK (
        op = 'delete' OR (op = 'upsert' AND entry_type IN ('file', 'dir'))
    )
);


--
-- Name: skill_draft_review_action_batches; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.skill_draft_review_action_batches (
    id character varying(36) NOT NULL,
    review_session_id character varying(36) NOT NULL,
    sequence bigint NOT NULL,
    undo_locked boolean DEFAULT false NOT NULL,
    undone_at timestamp without time zone,
    undone_by character varying(255),
    created_by character varying(255),
    created_at timestamp without time zone NOT NULL
);


--
-- Name: skill_draft_review_action_items; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.skill_draft_review_action_items (
    id character varying(36) NOT NULL,
    batch_id character varying(36) NOT NULL,
    review_session_id character varying(36) NOT NULL,
    path character varying(1024) NOT NULL,
    hunk_id character varying(128) NOT NULL,
    before_decision character varying(16) DEFAULT 'pending'::character varying NOT NULL,
    after_decision character varying(16) NOT NULL,
    created_at timestamp without time zone NOT NULL,
    CONSTRAINT chk_skill_draft_review_item_after_decision CHECK (after_decision IN ('accepted', 'rejected')),
    CONSTRAINT chk_skill_draft_review_item_before_decision CHECK (before_decision IN ('pending', 'accepted', 'rejected'))
);


--
-- Name: skill_draft_review_sessions; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.skill_draft_review_sessions (
    id character varying(36) NOT NULL,
    skill_id character varying(36) NOT NULL,
    base_revision_id character varying(36) NOT NULL,
    draft_version_at_start bigint NOT NULL,
    draft_snapshot_hash character varying(64) NOT NULL,
    status character varying(32) DEFAULT 'active'::character varying NOT NULL,
    version bigint DEFAULT 1 NOT NULL,
    undo_limit integer DEFAULT 20 NOT NULL,
    created_by character varying(255),
    updated_by character varying(255),
    created_at timestamp without time zone NOT NULL,
    updated_at timestamp without time zone NOT NULL,
    CONSTRAINT chk_skill_draft_review_session_status CHECK (status IN ('active', 'invalidated', 'committed', 'discarded'))
);


--
-- Name: skill_drafts; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.skill_drafts (
    skill_id character varying(36) NOT NULL,
    base_revision_id character varying(36),
    draft_status character varying(32) DEFAULT ''::character varying NOT NULL,
    draft_updated_at timestamp without time zone,
    task_id character varying(128) DEFAULT ''::character varying NOT NULL,
    conversation_id character varying(128),
    updated_by character varying(255),
    version bigint DEFAULT 1 NOT NULL,
    created_at timestamp without time zone NOT NULL,
    updated_at timestamp without time zone NOT NULL
);


--
-- Name: skill_market_installs; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.skill_market_installs (
    market_item_id character varying(36) NOT NULL,
    user_id character varying(255) NOT NULL,
    skill_id character varying(36) NOT NULL,
    created_at timestamp without time zone NOT NULL,
    updated_at timestamp without time zone NOT NULL
);


--
-- Name: skill_market_items; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.skill_market_items (
    id character varying(36) NOT NULL,
    source_skill_id character varying(36) NOT NULL,
    status character varying(32) DEFAULT 'draft'::character varying NOT NULL,
    icon text DEFAULT ''::text NOT NULL,
    sort_order integer DEFAULT 0 NOT NULL,
    version_note text DEFAULT ''::text NOT NULL,
    created_by character varying(255),
    updated_by character varying(255),
    published_at timestamp without time zone,
    created_at timestamp without time zone NOT NULL,
    updated_at timestamp without time zone NOT NULL,
    tags jsonb DEFAULT '[]'::jsonb NOT NULL
);


--
-- Name: skill_review_results; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.skill_review_results (
    id text NOT NULL,
    skill_name text NOT NULL,
    type text NOT NULL,
    review_status text DEFAULT 'pending'::text NOT NULL,
    userid text NOT NULL,
    requestid text NOT NULL,
    skill_content text NOT NULL,
    summary text,
    "time" timestamp with time zone DEFAULT CURRENT_TIMESTAMP NOT NULL,
    category text DEFAULT ''::text NOT NULL,
    CONSTRAINT chk_skill_review_results_review_status CHECK ((review_status = ANY (ARRAY['pending'::text, 'accepted'::text, 'rejected'::text, 'expired'::text]))),
    CONSTRAINT chk_skill_review_results_type CHECK ((type = ANY (ARRAY['new'::text, 'patch'::text])))
);


--
-- Name: skill_review_run_stats; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.skill_review_run_stats (
    id text NOT NULL,
    requestid text NOT NULL,
    userid text NOT NULL,
    status text NOT NULL,
    started_at text NOT NULL,
    duration_ms integer DEFAULT 0 NOT NULL,
    summary jsonb DEFAULT '{}'::jsonb NOT NULL,
    CONSTRAINT chk_skill_review_run_stats_duration_ms_non_negative CHECK ((duration_ms >= 0)),
    CONSTRAINT chk_skill_review_run_stats_status CHECK ((status = ANY (ARRAY['running'::text, 'completed'::text, 'skipped'::text, 'failed'::text])))
);


--
-- Name: skill_review_scheduler_state; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.skill_review_scheduler_state (
    user_id character varying(255) NOT NULL,
    last_window_end timestamp with time zone NOT NULL,
    next_run_at timestamp with time zone NOT NULL,
    stage_index integer DEFAULT 0 NOT NULL,
    stage_success_count integer DEFAULT 0 NOT NULL,
    total_success_count integer DEFAULT 0 NOT NULL,
    last_accepted_at timestamp with time zone,
    last_quantity_check_at timestamp with time zone,
    last_preflight_check_at timestamp with time zone,
    active_task_id character varying(36) DEFAULT ''::character varying NOT NULL,
    locked_by character varying(128) DEFAULT ''::character varying NOT NULL,
    locked_until timestamp with time zone,
    last_error_code character varying(64) DEFAULT ''::character varying NOT NULL,
    last_error_message text DEFAULT ''::text NOT NULL,
    created_at timestamp with time zone NOT NULL,
    updated_at timestamp with time zone NOT NULL,
    CONSTRAINT chk_skill_review_scheduler_stage_index_non_negative CHECK ((stage_index >= 0)),
    CONSTRAINT chk_skill_review_scheduler_stage_success_count_non_negative CHECK ((stage_success_count >= 0)),
    CONSTRAINT chk_skill_review_scheduler_total_success_count_non_negative CHECK ((total_success_count >= 0))
);


--
-- Name: skill_review_stats; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.skill_review_stats (
    id text NOT NULL,
    requestid text NOT NULL,
    userid text NOT NULL,
    status text NOT NULL,
    started_at text NOT NULL,
    duration_ms integer DEFAULT 0 NOT NULL,
    summary jsonb DEFAULT '{}'::jsonb NOT NULL,
    CONSTRAINT chk_skill_review_stats_duration_ms_non_negative CHECK ((duration_ms >= 0))
);


--
-- Name: skill_revision_entries; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.skill_revision_entries (
    revision_id character varying(36) NOT NULL,
    path character varying(1024) NOT NULL,
    entry_type character varying(16) NOT NULL,
    blob_hash character varying(64),
    size bigint DEFAULT 0 NOT NULL,
    mime character varying(128),
    file_type character varying(32) DEFAULT 'unknown'::character varying NOT NULL,
    "binary" boolean DEFAULT false NOT NULL,
    mode integer DEFAULT 420 NOT NULL,
    CONSTRAINT chk_skill_revision_entry_blob_shape CHECK (((((entry_type)::text = 'file'::text) AND (blob_hash IS NOT NULL)) OR (((entry_type)::text = 'dir'::text) AND (blob_hash IS NULL)))),
    CONSTRAINT chk_skill_revision_entry_type CHECK (entry_type IN ('file', 'dir'))
);


--
-- Name: skill_revisions; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.skill_revisions (
    id character varying(36) NOT NULL,
    skill_id character varying(36) NOT NULL,
    parent_revision_id character varying(36),
    revision_no bigint NOT NULL,
    tree_hash character varying(64) NOT NULL,
    message text,
    change_source character varying(32) DEFAULT 'draft_commit'::character varying NOT NULL,
    source_ref_type character varying(64) DEFAULT ''::character varying NOT NULL,
    source_ref_id character varying(128) DEFAULT ''::character varying NOT NULL,
    created_by character varying(255),
    created_at timestamp without time zone NOT NULL
);


--
-- Name: skill_search_indexes; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.skill_search_indexes (
    skill_id character varying(36) NOT NULL,
    owner_user_id character varying(255) NOT NULL,
    head_revision_id character varying(36) NOT NULL,
    content text NOT NULL,
    updated_at timestamp without time zone NOT NULL
);


--
-- Name: skills; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.skills (
    id character varying(36) NOT NULL,
    owner_user_id character varying(255) NOT NULL,
    owner_user_name character varying(255) DEFAULT ''::character varying NOT NULL,
    create_user_id character varying(255) NOT NULL,
    create_user_name character varying(255) DEFAULT ''::character varying NOT NULL,
    category character varying(128) NOT NULL,
    skill_name character varying(255) NOT NULL,
    origin_builtin_skill_uid character varying(64) DEFAULT ''::character varying NOT NULL,
    description text,
    tags json,
    relative_root character varying(1024) NOT NULL,
    skill_md_path character varying(1024) DEFAULT 'SKILL.md'::character varying NOT NULL,
    head_revision_id character varying(36),
    version bigint DEFAULT 1 NOT NULL,
    auto_evo boolean DEFAULT false NOT NULL,
    auto_evo_apply_status character varying(32) DEFAULT 'idle'::character varying NOT NULL,
    auto_evo_generation bigint DEFAULT 0 NOT NULL,
    auto_evo_started_at timestamp without time zone,
    auto_evo_finished_at timestamp without time zone,
    auto_evo_error text DEFAULT ''::text NOT NULL,
    is_enabled boolean DEFAULT true NOT NULL,
    update_status character varying(32) DEFAULT 'up_to_date'::character varying NOT NULL,
    ext json,
    created_at timestamp without time zone NOT NULL,
    updated_at timestamp without time zone NOT NULL,
    deleted_at timestamp without time zone,
    deleted_by character varying(255)
);


--
-- Name: sub_agent_artifacts; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.sub_agent_artifacts (
    id character varying(36) NOT NULL,
    task_id character varying(36) NOT NULL,
    slot character varying(64) NOT NULL,
    content_type character varying(32) NOT NULL,
    value json NOT NULL,
    seq integer DEFAULT 1 NOT NULL,
    created_at timestamp with time zone NOT NULL,
    hidden boolean DEFAULT false NOT NULL,
    caption text
);


--
-- Name: sub_agent_steps; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.sub_agent_steps (
    id character varying(36) NOT NULL,
    task_id character varying(36) NOT NULL,
    seq integer NOT NULL,
    role character varying(16) NOT NULL,
    content json NOT NULL,
    created_at timestamp with time zone NOT NULL
);


--
-- Name: sub_agent_tasks; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.sub_agent_tasks (
    id character varying(36) NOT NULL,
    conversation_id character varying(36) NOT NULL,
    trigger_history_id character varying(36),
    seq_in_conversation integer NOT NULL,
    agent_type character varying(64) NOT NULL,
    title character varying(255) NOT NULL,
    objective text DEFAULT ''::text NOT NULL,
    params json,
    mode character varying(8) NOT NULL,
    status character varying(16) DEFAULT 'pending'::character varying NOT NULL,
    progress_pct integer DEFAULT 0 NOT NULL,
    current_phase text,
    estimated_sec integer,
    summary text DEFAULT ''::text NOT NULL,
    last_heartbeat timestamp with time zone DEFAULT now() NOT NULL,
    workspace_path character varying(512) DEFAULT ''::character varying NOT NULL,
    input_slots json DEFAULT '[]'::json NOT NULL,
    output_slots json DEFAULT '[]'::json NOT NULL,
    create_user_id character varying(255) DEFAULT ''::character varying NOT NULL,
    created_at timestamp with time zone NOT NULL,
    updated_at timestamp with time zone NOT NULL,
    CONSTRAINT chk_sub_agent_tasks_mode CHECK (((mode)::text = ANY (ARRAY['auto'::text, 'manual'::text]))),
    CONSTRAINT chk_sub_agent_tasks_status CHECK (((status)::text = ANY (ARRAY['pending'::text, 'running'::text, 'succeeded'::text, 'failed'::text, 'interrupted'::text, 'canceled'::text])))
);


--
-- Name: task_center_tasks; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.task_center_tasks (
    id character varying(36) NOT NULL,
    user_id character varying(255) NOT NULL,
    conversation_id character varying(36) NOT NULL,
    plugin_session_id character varying(36),
    task_type character varying(32) NOT NULL,
    title text,
    status character varying(16) DEFAULT 'pending'::character varying NOT NULL,
    schedule_id character varying(36),
    progress_json text,
    predicted_completion_at timestamp with time zone,
    created_at timestamp with time zone NOT NULL,
    updated_at timestamp with time zone NOT NULL,
    finished_at timestamp with time zone,
    archived_at timestamp with time zone,
    group_id character varying(36),
    scheduled_fire_at timestamp with time zone,
    logical_slot_key character varying(160) DEFAULT ''::character varying NOT NULL,
    window_start timestamp with time zone,
    window_end timestamp with time zone,
    trigger_type character varying(32) DEFAULT 'manual'::character varying NOT NULL,
    attempt integer DEFAULT 1 NOT NULL,
    definition_version integer DEFAULT 1 NOT NULL,
    dependency_status character varying(32) DEFAULT 'none'::character varying NOT NULL,
    has_late_inputs boolean DEFAULT false NOT NULL,
    CONSTRAINT chk_tct_status CHECK (status IN ('pending', 'waiting_inputs', 'running', 'waiting', 'succeeded', 'failed', 'skipped', 'canceled')),
    CONSTRAINT chk_tct_task_type CHECK (((task_type)::text = ANY (ARRAY['plugin_run'::text, 'background_chat'::text, 'scheduled'::text])))
);


--
-- Name: task_run_inputs; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.task_run_inputs (
    id character varying(36) NOT NULL,
    downstream_task_id character varying(36) NOT NULL,
    upstream_task_id character varying(36) NOT NULL,
    dependency_id character varying(36) NOT NULL,
    source_logical_slot_key character varying(160),
    output_id character varying(36) NOT NULL,
    output_content_hash character varying(64) NOT NULL,
    "position" integer NOT NULL,
    snapshot_json text,
    created_at timestamp with time zone NOT NULL
);


--
-- Name: task_run_outputs; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.task_run_outputs (
    id character varying(36) NOT NULL,
    task_id character varying(36) NOT NULL,
    conversation_id character varying(36) NOT NULL,
    final_answer_text text,
    summary_text text,
    artifact_manifest_json text,
    output_status character varying(24) NOT NULL,
    content_hash character varying(64) NOT NULL,
    created_at timestamp with time zone NOT NULL,
    updated_at timestamp with time zone NOT NULL
);


--
-- Name: user_chat_settings; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.user_chat_settings (
    user_id character varying(255) NOT NULL,
    enable_plugin boolean DEFAULT true NOT NULL,
    plugin_mode character varying(16) DEFAULT 'dynamic'::character varying NOT NULL,
    enable_subagent boolean DEFAULT true NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL
);


--
-- Name: user_disabled_tools; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.user_disabled_tools (
    id bigint NOT NULL,
    tool_name character varying(255) NOT NULL,
    create_user_id character varying(255) NOT NULL,
    create_user_name character varying(255) DEFAULT ''::character varying NOT NULL,
    created_at timestamp with time zone NOT NULL,
    updated_at timestamp with time zone NOT NULL,
    deleted_at timestamp with time zone
);


--
-- Name: user_disabled_tools_id_seq; Type: SEQUENCE; Schema: public; Owner: -
--

CREATE SEQUENCE public.user_disabled_tools_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;


--
-- Name: user_disabled_tools_id_seq; Type: SEQUENCE OWNED BY; Schema: public; Owner: -
--

ALTER SEQUENCE public.user_disabled_tools_id_seq OWNED BY public.user_disabled_tools.id;


--
-- Name: user_plugin_settings; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.user_plugin_settings (
    user_id character varying(255) NOT NULL,
    plugin_ref character varying(512) NOT NULL,
    enabled boolean DEFAULT false NOT NULL,
    updated_at timestamp without time zone NOT NULL
);


--
-- Name: user_schedules; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.user_schedules (
    id character varying(36) NOT NULL,
    user_id character varying(255) NOT NULL,
    cron_expr character varying(64) NOT NULL,
    timezone character varying(64) DEFAULT 'Asia/Shanghai'::character varying NOT NULL,
    prompt_template text NOT NULL,
    enabled boolean DEFAULT true NOT NULL,
    last_run_at timestamp with time zone,
    next_run_at timestamp with time zone NOT NULL,
    created_at timestamp with time zone NOT NULL,
    kb_ids text DEFAULT '[]'::text NOT NULL,
    file_ids text DEFAULT '[]'::text NOT NULL,
    name character varying(128) DEFAULT ''::character varying NOT NULL,
    remark text DEFAULT ''::text NOT NULL,
    run_count integer DEFAULT 0 NOT NULL,
    group_id character varying(36),
    group_position integer DEFAULT 0 NOT NULL,
    definition_version integer DEFAULT 1 NOT NULL
);


--
-- Name: user_selected_providers; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.user_selected_providers (
    id bigint NOT NULL,
    user_id character varying(255) NOT NULL,
    user_name character varying(255) DEFAULT ''::character varying NOT NULL,
    category character varying(64) NOT NULL,
    user_model_provider_group_id character varying(64) NOT NULL,
    share boolean DEFAULT false NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL
);


--
-- Name: user_selected_providers_id_seq; Type: SEQUENCE; Schema: public; Owner: -
--

CREATE SEQUENCE public.user_selected_providers_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;


--
-- Name: user_selected_providers_id_seq; Type: SEQUENCE OWNED BY; Schema: public; Owner: -
--

ALTER SEQUENCE public.user_selected_providers_id_seq OWNED BY public.user_selected_providers.id;


--
-- Name: user_ui_preferences; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.user_ui_preferences (
    user_id character varying(255) NOT NULL,
    chat_preference_notice_dismissed boolean DEFAULT false NOT NULL,
    developer_mode_active boolean DEFAULT false NOT NULL,
    accepted_user_agreement_version character varying(64) DEFAULT ''::character varying NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL
);


--
-- Name: eval_set_items_p_eval_shard_0001; Type: TABLE ATTACH; Schema: public; Owner: -
--

ALTER TABLE ONLY public.eval_set_items ATTACH PARTITION public.eval_set_items_p_eval_shard_0001 FOR VALUES IN ('eval_shard_0001');


--
-- Name: local_fs_chat_settings id; Type: DEFAULT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.local_fs_chat_settings ALTER COLUMN id SET DEFAULT nextval('public.local_fs_chat_settings_id_seq'::regclass);


--
-- Name: user_disabled_tools id; Type: DEFAULT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.user_disabled_tools ALTER COLUMN id SET DEFAULT nextval('public.user_disabled_tools_id_seq'::regclass);


--
-- Name: user_selected_providers id; Type: DEFAULT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.user_selected_providers ALTER COLUMN id SET DEFAULT nextval('public.user_selected_providers_id_seq'::regclass);


--
-- Name: agent_thread_steps agent_thread_steps_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.agent_thread_steps
    ADD CONSTRAINT agent_thread_steps_pkey PRIMARY KEY (thread_id, step_id);


--
-- Name: async_jobs async_jobs_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.async_jobs
    ADD CONSTRAINT async_jobs_pkey PRIMARY KEY (id);


--
-- Name: automation_groups automation_groups_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.automation_groups
    ADD CONSTRAINT automation_groups_pkey PRIMARY KEY (id);


--
-- Name: conversation_artifacts conversation_artifacts_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.conversation_artifacts
    ADD CONSTRAINT conversation_artifacts_pkey PRIMARY KEY (id);


--
-- Name: conversation_idle_events conversation_idle_events_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.conversation_idle_events
    ADD CONSTRAINT conversation_idle_events_pkey PRIMARY KEY (id);


--
-- Name: eval_set_import_previews eval_set_import_previews_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.eval_set_import_previews
    ADD CONSTRAINT eval_set_import_previews_pkey PRIMARY KEY (token);


--
-- Name: eval_set_items eval_set_items_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.eval_set_items
    ADD CONSTRAINT eval_set_items_pkey PRIMARY KEY (shard_id, id);


--
-- Name: eval_set_items_p_eval_shard_0001 eval_set_items_p_eval_shard_0001_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.eval_set_items_p_eval_shard_0001
    ADD CONSTRAINT eval_set_items_p_eval_shard_0001_pkey PRIMARY KEY (shard_id, id);


--
-- Name: eval_set_shards eval_set_shards_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.eval_set_shards
    ADD CONSTRAINT eval_set_shards_pkey PRIMARY KEY (id);


--
-- Name: eval_sets eval_sets_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.eval_sets
    ADD CONSTRAINT eval_sets_pkey PRIMARY KEY (id);


--
-- Name: external_database_connections external_database_connections_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.external_database_connections
    ADD CONSTRAINT external_database_connections_pkey PRIMARY KEY (id);


--
-- Name: local_fs_chat_settings local_fs_chat_settings_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.local_fs_chat_settings
    ADD CONSTRAINT local_fs_chat_settings_pkey PRIMARY KEY (id);


--
-- Name: mcp_server_tools mcp_server_tools_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.mcp_server_tools
    ADD CONSTRAINT mcp_server_tools_pkey PRIMARY KEY (id);


--
-- Name: mcp_servers mcp_servers_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.mcp_servers
    ADD CONSTRAINT mcp_servers_pkey PRIMARY KEY (id);


--
-- Name: memory_review memory_review_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.memory_review
    ADD CONSTRAINT memory_review_pkey PRIMARY KEY (id);


--
-- Name: personal_resource_blobs personal_resource_blobs_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.personal_resource_blobs
    ADD CONSTRAINT personal_resource_blobs_pkey PRIMARY KEY (hash);


--
-- Name: personal_resource_drafts personal_resource_drafts_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.personal_resource_drafts
    ADD CONSTRAINT personal_resource_drafts_pkey PRIMARY KEY (resource_id);


--
-- Name: personal_resource_review_action_batches personal_resource_review_action_batches_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.personal_resource_review_action_batches
    ADD CONSTRAINT personal_resource_review_action_batches_pkey PRIMARY KEY (id);


--
-- Name: personal_resource_review_action_items personal_resource_review_action_items_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.personal_resource_review_action_items
    ADD CONSTRAINT personal_resource_review_action_items_pkey PRIMARY KEY (id);


--
-- Name: personal_resource_review_sessions personal_resource_review_sessions_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.personal_resource_review_sessions
    ADD CONSTRAINT personal_resource_review_sessions_pkey PRIMARY KEY (id);


--
-- Name: personal_resource_revisions personal_resource_revisions_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.personal_resource_revisions
    ADD CONSTRAINT personal_resource_revisions_pkey PRIMARY KEY (id);


--
-- Name: personal_resources personal_resources_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.personal_resources
    ADD CONSTRAINT personal_resources_pkey PRIMARY KEY (id);


--
-- Name: plugin_attempt_input_bindings plugin_attempt_input_bindings_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.plugin_attempt_input_bindings
    ADD CONSTRAINT plugin_attempt_input_bindings_pkey PRIMARY KEY (id);


--
-- Name: plugin_blobs plugin_blobs_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.plugin_blobs
    ADD CONSTRAINT plugin_blobs_pkey PRIMARY KEY (hash);


--
-- Name: plugin_drafts plugin_drafts_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.plugin_drafts
    ADD CONSTRAINT plugin_drafts_pkey PRIMARY KEY (id);


--
-- Name: plugin_generation_analyses plugin_generation_analyses_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.plugin_generation_analyses
    ADD CONSTRAINT plugin_generation_analyses_pkey PRIMARY KEY (id);


--
-- Name: plugin_human_artifacts plugin_human_artifacts_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.plugin_human_artifacts
    ADD CONSTRAINT plugin_human_artifacts_pkey PRIMARY KEY (id);


--
-- Name: plugin_repair_runs plugin_repair_runs_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.plugin_repair_runs
    ADD CONSTRAINT plugin_repair_runs_pkey PRIMARY KEY (id);


--
-- Name: plugin_revision_entries plugin_revision_entries_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.plugin_revision_entries
    ADD CONSTRAINT plugin_revision_entries_pkey PRIMARY KEY (revision_id, path);


--
-- Name: plugin_revisions plugin_revisions_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.plugin_revisions
    ADD CONSTRAINT plugin_revisions_pkey PRIMARY KEY (id);


--
-- Name: plugin_revisions plugin_revisions_plugin_resource_id_revision_no_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.plugin_revisions
    ADD CONSTRAINT plugin_revisions_plugin_resource_id_revision_no_key UNIQUE (plugin_resource_id, revision_no);


--
-- Name: plugin_route_decisions plugin_route_decisions_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.plugin_route_decisions
    ADD CONSTRAINT plugin_route_decisions_pkey PRIMARY KEY (id);


--
-- Name: plugin_run_outbox plugin_run_outbox_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.plugin_run_outbox
    ADD CONSTRAINT plugin_run_outbox_pkey PRIMARY KEY (task_id);


--
-- Name: plugin_session_steps plugin_session_steps_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.plugin_session_steps
    ADD CONSTRAINT plugin_session_steps_pkey PRIMARY KEY (id);


--
-- Name: plugin_sessions plugin_sessions_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.plugin_sessions
    ADD CONSTRAINT plugin_sessions_pkey PRIMARY KEY (id);


--
-- Name: plugin_slot_order plugin_slot_order_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.plugin_slot_order
    ADD CONSTRAINT plugin_slot_order_pkey PRIMARY KEY (session_id, slot_id);


--
-- Name: plugin_slot_revisions plugin_slot_revisions_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.plugin_slot_revisions
    ADD CONSTRAINT plugin_slot_revisions_pkey PRIMARY KEY (id);


--
-- Name: plugin_step_intents plugin_step_intents_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.plugin_step_intents
    ADD CONSTRAINT plugin_step_intents_pkey PRIMARY KEY (id);


--
-- Name: plugin_transition_commands plugin_transition_commands_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.plugin_transition_commands
    ADD CONSTRAINT plugin_transition_commands_pkey PRIMARY KEY (command_id);


--
-- Name: plugins plugins_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.plugins
    ADD CONSTRAINT plugins_pkey PRIMARY KEY (id);


--
-- Name: plugins plugins_plugin_ref_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.plugins
    ADD CONSTRAINT plugins_plugin_ref_key UNIQUE (plugin_ref);


--
-- Name: plugins plugins_relative_root_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.plugins
    ADD CONSTRAINT plugins_relative_root_key UNIQUE (relative_root);


--
-- Name: prompt_categories prompt_categories_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.prompt_categories
    ADD CONSTRAINT prompt_categories_pkey PRIMARY KEY (id);


--
-- Name: prompt_user_states prompt_user_states_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.prompt_user_states
    ADD CONSTRAINT prompt_user_states_pkey PRIMARY KEY (id);


--
-- Name: resource_update_tasks resource_update_tasks_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.resource_update_tasks
    ADD CONSTRAINT resource_update_tasks_pkey PRIMARY KEY (id);


--
-- Name: schedule_dependencies schedule_dependencies_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.schedule_dependencies
    ADD CONSTRAINT schedule_dependencies_pkey PRIMARY KEY (id);


--
-- Name: skill_blobs skill_blobs_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.skill_blobs
    ADD CONSTRAINT skill_blobs_pkey PRIMARY KEY (hash);


--
-- Name: skill_draft_entries skill_draft_entries_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.skill_draft_entries
    ADD CONSTRAINT skill_draft_entries_pkey PRIMARY KEY (skill_id, path);


--
-- Name: skill_draft_review_action_batches skill_draft_review_action_batche_review_session_id_sequence_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.skill_draft_review_action_batches
    ADD CONSTRAINT skill_draft_review_action_batche_review_session_id_sequence_key UNIQUE (review_session_id, sequence);


--
-- Name: skill_draft_review_action_batches skill_draft_review_action_batches_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.skill_draft_review_action_batches
    ADD CONSTRAINT skill_draft_review_action_batches_pkey PRIMARY KEY (id);


--
-- Name: skill_draft_review_action_items skill_draft_review_action_items_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.skill_draft_review_action_items
    ADD CONSTRAINT skill_draft_review_action_items_pkey PRIMARY KEY (id);


--
-- Name: skill_draft_review_sessions skill_draft_review_sessions_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.skill_draft_review_sessions
    ADD CONSTRAINT skill_draft_review_sessions_pkey PRIMARY KEY (id);


--
-- Name: skill_drafts skill_drafts_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.skill_drafts
    ADD CONSTRAINT skill_drafts_pkey PRIMARY KEY (skill_id);


--
-- Name: skill_market_installs skill_market_installs_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.skill_market_installs
    ADD CONSTRAINT skill_market_installs_pkey PRIMARY KEY (market_item_id, user_id);


--
-- Name: skill_market_items skill_market_items_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.skill_market_items
    ADD CONSTRAINT skill_market_items_pkey PRIMARY KEY (id);


--
-- Name: skill_review_results skill_review_results_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.skill_review_results
    ADD CONSTRAINT skill_review_results_pkey PRIMARY KEY (id);


--
-- Name: skill_review_run_stats skill_review_run_stats_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.skill_review_run_stats
    ADD CONSTRAINT skill_review_run_stats_pkey PRIMARY KEY (id);


--
-- Name: skill_review_scheduler_state skill_review_scheduler_state_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.skill_review_scheduler_state
    ADD CONSTRAINT skill_review_scheduler_state_pkey PRIMARY KEY (user_id);


--
-- Name: skill_review_stats skill_review_stats_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.skill_review_stats
    ADD CONSTRAINT skill_review_stats_pkey PRIMARY KEY (id);


--
-- Name: skill_revision_entries skill_revision_entries_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.skill_revision_entries
    ADD CONSTRAINT skill_revision_entries_pkey PRIMARY KEY (revision_id, path);


--
-- Name: skill_revisions skill_revisions_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.skill_revisions
    ADD CONSTRAINT skill_revisions_pkey PRIMARY KEY (id);


--
-- Name: skill_search_indexes skill_search_indexes_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.skill_search_indexes
    ADD CONSTRAINT skill_search_indexes_pkey PRIMARY KEY (skill_id);


--
-- Name: skills skills_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.skills
    ADD CONSTRAINT skills_pkey PRIMARY KEY (id);


--
-- Name: sub_agent_artifacts sub_agent_artifacts_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.sub_agent_artifacts
    ADD CONSTRAINT sub_agent_artifacts_pkey PRIMARY KEY (id);


--
-- Name: sub_agent_steps sub_agent_steps_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.sub_agent_steps
    ADD CONSTRAINT sub_agent_steps_pkey PRIMARY KEY (id);


--
-- Name: sub_agent_tasks sub_agent_tasks_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.sub_agent_tasks
    ADD CONSTRAINT sub_agent_tasks_pkey PRIMARY KEY (id);


--
-- Name: task_center_tasks task_center_tasks_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.task_center_tasks
    ADD CONSTRAINT task_center_tasks_pkey PRIMARY KEY (id);


--
-- Name: task_run_inputs task_run_inputs_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.task_run_inputs
    ADD CONSTRAINT task_run_inputs_pkey PRIMARY KEY (id);


--
-- Name: task_run_outputs task_run_outputs_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.task_run_outputs
    ADD CONSTRAINT task_run_outputs_pkey PRIMARY KEY (id);


--
-- Name: task_run_outputs task_run_outputs_task_id_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.task_run_outputs
    ADD CONSTRAINT task_run_outputs_task_id_key UNIQUE (task_id);


--
-- Name: schedule_dependencies uk_schedule_dependency; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.schedule_dependencies
    ADD CONSTRAINT uk_schedule_dependency UNIQUE (source_schedule_id, target_schedule_id);


--
-- Name: user_selected_providers uk_user_selected_providers_user_category; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.user_selected_providers
    ADD CONSTRAINT uk_user_selected_providers_user_category UNIQUE (user_id, category);


--
-- Name: user_chat_settings user_chat_settings_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.user_chat_settings
    ADD CONSTRAINT user_chat_settings_pkey PRIMARY KEY (user_id);


--
-- Name: user_disabled_tools user_disabled_tools_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.user_disabled_tools
    ADD CONSTRAINT user_disabled_tools_pkey PRIMARY KEY (id);


--
-- Name: user_plugin_settings user_plugin_settings_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.user_plugin_settings
    ADD CONSTRAINT user_plugin_settings_pkey PRIMARY KEY (user_id, plugin_ref);


--
-- Name: user_schedules user_schedules_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.user_schedules
    ADD CONSTRAINT user_schedules_pkey PRIMARY KEY (id);


--
-- Name: user_selected_providers user_selected_providers_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.user_selected_providers
    ADD CONSTRAINT user_selected_providers_pkey PRIMARY KEY (id);


--
-- Name: user_ui_preferences user_ui_preferences_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.user_ui_preferences
    ADD CONSTRAINT user_ui_preferences_pkey PRIMARY KEY (user_id);


--
-- Name: idx_eval_set_items_set_source; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_eval_set_items_set_source ON ONLY public.eval_set_items USING btree (shard_id, eval_set_id, source);


--
-- Name: eval_set_items_p_eval_shard_000_shard_id_eval_set_id_source_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX eval_set_items_p_eval_shard_000_shard_id_eval_set_id_source_idx ON public.eval_set_items_p_eval_shard_0001 USING btree (shard_id, eval_set_id, source);


--
-- Name: idx_eval_set_items_set_created; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_eval_set_items_set_created ON ONLY public.eval_set_items USING btree (shard_id, eval_set_id, created_at DESC);


--
-- Name: eval_set_items_p_eval_shard_0_shard_id_eval_set_id_created__idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX eval_set_items_p_eval_shard_0_shard_id_eval_set_id_created__idx ON public.eval_set_items_p_eval_shard_0001 USING btree (shard_id, eval_set_id, created_at DESC);


--
-- Name: idx_eval_set_items_set_type; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_eval_set_items_set_type ON ONLY public.eval_set_items USING btree (shard_id, eval_set_id, question_type);


--
-- Name: eval_set_items_p_eval_shard_0_shard_id_eval_set_id_question_idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX eval_set_items_p_eval_shard_0_shard_id_eval_set_id_question_idx ON public.eval_set_items_p_eval_shard_0001 USING btree (shard_id, eval_set_id, question_type);


--
-- Name: idx_eval_set_items_set_updated; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_eval_set_items_set_updated ON ONLY public.eval_set_items USING btree (shard_id, eval_set_id, updated_at DESC);


--
-- Name: eval_set_items_p_eval_shard_0_shard_id_eval_set_id_updated__idx; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX eval_set_items_p_eval_shard_0_shard_id_eval_set_id_updated__idx ON public.eval_set_items_p_eval_shard_0001 USING btree (shard_id, eval_set_id, updated_at DESC);


--
-- Name: idx_agent_thread_steps_stage; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_agent_thread_steps_stage ON public.agent_thread_steps USING btree (stage);


--
-- Name: idx_agent_thread_steps_thread_active; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_agent_thread_steps_thread_active ON public.agent_thread_steps USING btree (thread_id, active, updated_at);


--
-- Name: idx_agent_thread_steps_thread_order; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_agent_thread_steps_thread_order ON public.agent_thread_steps USING btree (thread_id, order_index, step_id);


--
-- Name: idx_async_jobs_idempotency_key; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_async_jobs_idempotency_key ON public.async_jobs USING btree (idempotency_key);


--
-- Name: idx_async_jobs_lock_until; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_async_jobs_lock_until ON public.async_jobs USING btree (lock_until);


--
-- Name: idx_async_jobs_resource; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_async_jobs_resource ON public.async_jobs USING btree (resource_type, resource_id);


--
-- Name: idx_async_jobs_status_next; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_async_jobs_status_next ON public.async_jobs USING btree (status, next_run_at);


--
-- Name: idx_async_jobs_type_idempotency_key_unique; Type: INDEX; Schema: public; Owner: -
--

CREATE UNIQUE INDEX idx_async_jobs_type_idempotency_key_unique ON public.async_jobs USING btree (job_type, idempotency_key) WHERE ((idempotency_key)::text <> ''::text);


--
-- Name: idx_async_jobs_type_status; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_async_jobs_type_status ON public.async_jobs USING btree (job_type, status);


--
-- Name: idx_automation_groups_user; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_automation_groups_user ON public.automation_groups USING btree (user_id);


--
-- Name: idx_conversation_artifacts_history_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_conversation_artifacts_history_id ON public.conversation_artifacts USING btree (history_id);


--
-- Name: idx_conversation_artifacts_owner_conversation_created; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_conversation_artifacts_owner_conversation_created ON public.conversation_artifacts USING btree (create_user_id, conversation_id, created_at);


--
-- Name: idx_conversation_idle_events_due; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_conversation_idle_events_due ON public.conversation_idle_events USING btree (status, due_at) WHERE ((status)::text = 'waiting'::text);


--
-- Name: idx_conversation_idle_events_due_at; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_conversation_idle_events_due_at ON public.conversation_idle_events USING btree (due_at);


--
-- Name: idx_conversation_idle_events_session_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_conversation_idle_events_session_id ON public.conversation_idle_events USING btree (session_id);


--
-- Name: idx_conversation_idle_events_session_waiting; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_conversation_idle_events_session_waiting ON public.conversation_idle_events USING btree (session_id, status, due_at DESC);


--
-- Name: idx_conversation_idle_events_status; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_conversation_idle_events_status ON public.conversation_idle_events USING btree (status);


--
-- Name: idx_conversation_idle_events_user_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_conversation_idle_events_user_id ON public.conversation_idle_events USING btree (user_id);


--
-- Name: idx_eval_set_import_previews_expires_at; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_eval_set_import_previews_expires_at ON public.eval_set_import_previews USING btree (expires_at);


--
-- Name: idx_eval_set_import_previews_status; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_eval_set_import_previews_status ON public.eval_set_import_previews USING btree (status);


--
-- Name: idx_eval_set_import_previews_user; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_eval_set_import_previews_user ON public.eval_set_import_previews USING btree (create_user_id);


--
-- Name: idx_eval_set_shards_status; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_eval_set_shards_status ON public.eval_set_shards USING btree (status);


--
-- Name: idx_eval_sets_dataset_ids; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_eval_sets_dataset_ids ON public.eval_sets USING gin (dataset_ids);


--
-- Name: idx_eval_sets_group; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_eval_sets_group ON public.eval_sets USING btree (group_id);


--
-- Name: idx_eval_sets_owner; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_eval_sets_owner ON public.eval_sets USING btree (owner_id);


--
-- Name: idx_eval_sets_shard; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_eval_sets_shard ON public.eval_sets USING btree (shard_id);


--
-- Name: idx_eval_sets_status; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_eval_sets_status ON public.eval_sets USING btree (status);


--
-- Name: idx_external_database_connections_user; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_external_database_connections_user ON public.external_database_connections USING btree (create_user_id, deleted_at, updated_at);


--
-- Name: idx_mcp_servers_share; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_mcp_servers_share ON public.mcp_servers USING btree (share, enabled, deleted_at);


--
-- Name: idx_mcp_servers_user; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_mcp_servers_user ON public.mcp_servers USING btree (create_user_id, deleted_at);


--
-- Name: idx_mcp_tools_server; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_mcp_tools_server ON public.mcp_server_tools USING btree (mcp_server_id, deleted_at);


--
-- Name: idx_memory_review_pending_scan; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_memory_review_pending_scan ON public.memory_review USING btree (target, user_id, state, review_status, "time");


--
-- Name: idx_paib_attempt; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_paib_attempt ON public.plugin_attempt_input_bindings USING btree (attempt_id);


--
-- Name: idx_paib_material_revision; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_paib_material_revision ON public.plugin_attempt_input_bindings USING btree (material_revision_id);


--
-- Name: idx_personal_resource_drafts_blob; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_personal_resource_drafts_blob ON public.personal_resource_drafts USING btree (blob_hash);


--
-- Name: idx_personal_resource_review_batches_session_created; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_personal_resource_review_batches_session_created ON public.personal_resource_review_action_batches USING btree (session_id, created_at DESC);


--
-- Name: idx_personal_resource_review_items_batch; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_personal_resource_review_items_batch ON public.personal_resource_review_action_items USING btree (batch_id);


--
-- Name: idx_personal_resource_review_sessions_resource_status; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_personal_resource_review_sessions_resource_status ON public.personal_resource_review_sessions USING btree (resource_id, status);


--
-- Name: idx_personal_resource_revisions_blob; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_personal_resource_revisions_blob ON public.personal_resource_revisions USING btree (blob_hash);


--
-- Name: idx_personal_resource_revisions_created; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_personal_resource_revisions_created ON public.personal_resource_revisions USING btree (resource_id, created_at DESC);


--
-- Name: idx_plugin_drafts_created_by; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_plugin_drafts_created_by ON public.plugin_drafts USING btree (created_by);


--
-- Name: idx_plugin_drafts_user_plugin_id; Type: INDEX; Schema: public; Owner: -
--

CREATE UNIQUE INDEX idx_plugin_drafts_user_plugin_id ON public.plugin_drafts USING btree (created_by, plugin_id) WHERE ((plugin_id)::text <> ''::text);


--
-- Name: idx_plugin_generation_analyses_draft; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_plugin_generation_analyses_draft ON public.plugin_generation_analyses USING btree (draft_id, created_at);


--
-- Name: idx_plugin_human_artifacts_session_slot; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_plugin_human_artifacts_session_slot ON public.plugin_human_artifacts USING btree (session_id, slot);


--
-- Name: idx_plugin_repair_runs_draft; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_plugin_repair_runs_draft ON public.plugin_repair_runs USING btree (draft_id, created_at);


--
-- Name: idx_plugin_revisions_resource; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_plugin_revisions_resource ON public.plugin_revisions USING btree (plugin_resource_id);


--
-- Name: idx_plugin_run_outbox_status; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_plugin_run_outbox_status ON public.plugin_run_outbox USING btree (status, created_at);


--
-- Name: idx_plugins_owner; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_plugins_owner ON public.plugins USING btree (owner_user_id, status);


--
-- Name: idx_prd_session; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_prd_session ON public.plugin_route_decisions USING btree (session_id, from_step_id);


--
-- Name: idx_ps_conv; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_ps_conv ON public.plugin_sessions USING btree (conversation_id, created_at DESC);


--
-- Name: idx_ps_conv_active; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_ps_conv_active ON public.plugin_sessions USING btree (conversation_id, status);


--
-- Name: idx_psr_session; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_psr_session ON public.plugin_slot_revisions USING btree (session_id, slot_id);


--
-- Name: idx_psr_slot; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_psr_slot ON public.plugin_slot_revisions USING btree (slot);


--
-- Name: idx_psr_slot_rev; Type: INDEX; Schema: public; Owner: -
--

CREATE UNIQUE INDEX idx_psr_slot_rev ON public.plugin_slot_revisions USING btree (session_id, slot_id, COALESCE(list_index, '-1'::integer), revision);


--
-- Name: idx_pss_session; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_pss_session ON public.plugin_session_steps USING btree (session_id, step_id, attempt);


--
-- Name: idx_pss_task; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_pss_task ON public.plugin_session_steps USING btree (task_id);


--
-- Name: idx_ptc_session; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_ptc_session ON public.plugin_transition_commands USING btree (session_id, created_at);


--
-- Name: idx_resource_update_tasks_pending; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_resource_update_tasks_pending ON public.resource_update_tasks USING btree (status, next_run_at, created_at) WHERE ((status)::text = 'pending'::text);


--
-- Name: idx_resource_update_tasks_resource_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_resource_update_tasks_resource_id ON public.resource_update_tasks USING btree (resource_id);


--
-- Name: idx_resource_update_tasks_resource_type; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_resource_update_tasks_resource_type ON public.resource_update_tasks USING btree (resource_type);


--
-- Name: idx_resource_update_tasks_result_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_resource_update_tasks_result_id ON public.resource_update_tasks USING btree (result_id);


--
-- Name: idx_resource_update_tasks_review_result_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_resource_update_tasks_review_result_id ON public.resource_update_tasks USING btree (review_result_id);


--
-- Name: idx_resource_update_tasks_running_lock; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_resource_update_tasks_running_lock ON public.resource_update_tasks USING btree (status, locked_until) WHERE ((status)::text = 'running'::text);


--
-- Name: idx_resource_update_tasks_status; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_resource_update_tasks_status ON public.resource_update_tasks USING btree (status);


--
-- Name: idx_resource_update_tasks_task_type; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_resource_update_tasks_task_type ON public.resource_update_tasks USING btree (task_type);


--
-- Name: idx_resource_update_tasks_trigger_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_resource_update_tasks_trigger_id ON public.resource_update_tasks USING btree (trigger_id);


--
-- Name: idx_resource_update_tasks_trigger_type; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_resource_update_tasks_trigger_type ON public.resource_update_tasks USING btree (trigger_type);


--
-- Name: idx_resource_update_tasks_user_created; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_resource_update_tasks_user_created ON public.resource_update_tasks USING btree (user_id, created_at DESC);


--
-- Name: idx_resource_update_tasks_user_id; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_resource_update_tasks_user_id ON public.resource_update_tasks USING btree (user_id);


--
-- Name: idx_saa_task_slot; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_saa_task_slot ON public.sub_agent_artifacts USING btree (task_id, slot, seq);


--
-- Name: idx_saa_task_visible; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_saa_task_visible ON public.sub_agent_artifacts USING btree (task_id, slot, hidden, seq);


--
-- Name: idx_sas_task; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_sas_task ON public.sub_agent_steps USING btree (task_id, seq);


--
-- Name: idx_sat_status; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_sat_status ON public.sub_agent_tasks USING btree (status, last_heartbeat);


--
-- Name: idx_sat_trigger; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_sat_trigger ON public.sub_agent_tasks USING btree (trigger_history_id);


--
-- Name: idx_schedule_dependencies_source; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_schedule_dependencies_source ON public.schedule_dependencies USING btree (source_schedule_id);


--
-- Name: idx_schedule_dependencies_target; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_schedule_dependencies_target ON public.schedule_dependencies USING btree (target_schedule_id);


--
-- Name: idx_skill_draft_entries_blob; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_skill_draft_entries_blob ON public.skill_draft_entries USING btree (blob_hash);


--
-- Name: idx_skill_draft_review_batches_session_created; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_skill_draft_review_batches_session_created ON public.skill_draft_review_action_batches USING btree (review_session_id, created_at DESC);


--
-- Name: idx_skill_draft_review_items_batch; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_skill_draft_review_items_batch ON public.skill_draft_review_action_items USING btree (batch_id);


--
-- Name: idx_skill_draft_review_items_session_hunk; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_skill_draft_review_items_session_hunk ON public.skill_draft_review_action_items USING btree (review_session_id, path, hunk_id);


--
-- Name: idx_skill_draft_review_sessions_skill_status; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_skill_draft_review_sessions_skill_status ON public.skill_draft_review_sessions USING btree (skill_id, status, updated_at DESC);


--
-- Name: idx_skill_market_installs_skill; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_skill_market_installs_skill ON public.skill_market_installs USING btree (skill_id);


--
-- Name: idx_skill_market_installs_user; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_skill_market_installs_user ON public.skill_market_installs USING btree (user_id, market_item_id);


--
-- Name: idx_skill_market_items_status; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_skill_market_items_status ON public.skill_market_items USING btree (status, sort_order, updated_at DESC);


--
-- Name: idx_skill_review_results_pending_identity; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_skill_review_results_pending_identity ON public.skill_review_results USING btree (userid, category, skill_name) WHERE (review_status = 'pending'::text);


--
-- Name: idx_skill_review_results_pending_scan; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_skill_review_results_pending_scan ON public.skill_review_results USING btree (userid, review_status, type, skill_name, "time" DESC);


--
-- Name: idx_skill_review_scheduler_state_scan; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_skill_review_scheduler_state_scan ON public.skill_review_scheduler_state USING btree (locked_until, next_run_at, last_quantity_check_at);


--
-- Name: idx_skill_revision_entries_blob; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_skill_revision_entries_blob ON public.skill_revision_entries USING btree (blob_hash);


--
-- Name: idx_skill_revisions_skill_created; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_skill_revisions_skill_created ON public.skill_revisions USING btree (skill_id, created_at DESC);


--
-- Name: idx_skill_search_owner; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_skill_search_owner ON public.skill_search_indexes USING btree (owner_user_id);


--
-- Name: idx_skills_owner_deleted; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_skills_owner_deleted ON public.skills USING btree (owner_user_id, deleted_at);


--
-- Name: idx_skills_owner_enabled; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_skills_owner_enabled ON public.skills USING btree (owner_user_id, is_enabled, category);


--
-- Name: idx_task_center_schedule_execution; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_task_center_schedule_execution ON public.task_center_tasks USING btree (schedule_id, scheduled_fire_at, created_at);


--
-- Name: idx_task_run_inputs_downstream; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_task_run_inputs_downstream ON public.task_run_inputs USING btree (downstream_task_id);


--
-- Name: idx_task_run_inputs_upstream; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_task_run_inputs_upstream ON public.task_run_inputs USING btree (upstream_task_id);


--
-- Name: idx_task_run_outputs_conversation; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_task_run_outputs_conversation ON public.task_run_outputs USING btree (conversation_id);


--
-- Name: idx_tct_user_status; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_tct_user_status ON public.task_center_tasks USING btree (user_id, status);


--
-- Name: idx_us_next_run; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_us_next_run ON public.user_schedules USING btree (next_run_at) WHERE (enabled = true);


--
-- Name: idx_us_user; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_us_user ON public.user_schedules USING btree (user_id);


--
-- Name: uk_conversation_idle_events_event_id; Type: INDEX; Schema: public; Owner: -
--

CREATE UNIQUE INDEX uk_conversation_idle_events_event_id ON public.conversation_idle_events USING btree (event_id);


--
-- Name: uk_local_fs_chat_settings_user; Type: INDEX; Schema: public; Owner: -
--

CREATE UNIQUE INDEX uk_local_fs_chat_settings_user ON public.local_fs_chat_settings USING btree (create_user_id);


--
-- Name: uk_personal_resource_revisions_no; Type: INDEX; Schema: public; Owner: -
--

CREATE UNIQUE INDEX uk_personal_resource_revisions_no ON public.personal_resource_revisions USING btree (resource_id, revision_no);


--
-- Name: uk_personal_resources_user_type; Type: INDEX; Schema: public; Owner: -
--

CREATE UNIQUE INDEX uk_personal_resources_user_type ON public.personal_resources USING btree (user_id, resource_type);


--
-- Name: uk_plugin_step_intent; Type: INDEX; Schema: public; Owner: -
--

CREATE UNIQUE INDEX uk_plugin_step_intent ON public.plugin_step_intents USING btree (session_id, step_id);


--
-- Name: uk_prompt_categories_user_name; Type: INDEX; Schema: public; Owner: -
--

CREATE UNIQUE INDEX uk_prompt_categories_user_name ON public.prompt_categories USING btree (create_user_id, name);


--
-- Name: uk_prompt_user_states_user_prompt; Type: INDEX; Schema: public; Owner: -
--

CREATE UNIQUE INDEX uk_prompt_user_states_user_prompt ON public.prompt_user_states USING btree (create_user_id, prompt_id);


--
-- Name: uk_skill_revisions_skill_no; Type: INDEX; Schema: public; Owner: -
--

CREATE UNIQUE INDEX uk_skill_revisions_skill_no ON public.skill_revisions USING btree (skill_id, revision_no);


--
-- Name: uk_skills_owner_identity; Type: INDEX; Schema: public; Owner: -
--

CREATE UNIQUE INDEX uk_skills_owner_identity ON public.skills USING btree (owner_user_id, category, skill_name) WHERE (deleted_at IS NULL);


--
-- Name: uk_skills_owner_relative_root; Type: INDEX; Schema: public; Owner: -
--

CREATE UNIQUE INDEX uk_skills_owner_relative_root ON public.skills USING btree (owner_user_id, relative_root) WHERE (deleted_at IS NULL);


--
-- Name: uk_task_run_input_snapshot; Type: INDEX; Schema: public; Owner: -
--

CREATE UNIQUE INDEX uk_task_run_input_snapshot ON public.task_run_inputs USING btree (downstream_task_id, dependency_id, upstream_task_id);


--
-- Name: uk_user_disabled_tools_user_tool; Type: INDEX; Schema: public; Owner: -
--

CREATE UNIQUE INDEX uk_user_disabled_tools_user_tool ON public.user_disabled_tools USING btree (create_user_id, tool_name);


--
-- Name: uniq_active_skill_maintenance_admission; Type: INDEX; Schema: public; Owner: -
--

CREATE UNIQUE INDEX uniq_active_skill_maintenance_admission
    ON public.resource_update_tasks (user_id)
    WHERE resource_type = 'skill'
      AND task_type IN ('generate_review', 'organize_skill')
      AND status IN ('pending', 'running');


--
-- Name: uniq_resource_update_active_auto_apply_result; Type: INDEX; Schema: public; Owner: -
--

CREATE UNIQUE INDEX uniq_resource_update_active_auto_apply_result
    ON public.resource_update_tasks (resource_type, review_result_id)
    WHERE task_type = 'auto_apply_review'
      AND status IN ('pending', 'running');


--
-- Name: uniq_resource_update_task_trigger; Type: INDEX; Schema: public; Owner: -
--

CREATE UNIQUE INDEX uniq_resource_update_task_trigger ON public.resource_update_tasks USING btree (task_type, resource_type, trigger_type, trigger_id);


--
-- Name: uq_sat_conv_seq; Type: INDEX; Schema: public; Owner: -
--

CREATE UNIQUE INDEX uq_sat_conv_seq ON public.sub_agent_tasks USING btree (conversation_id, seq_in_conversation);


--
-- Name: eval_set_items_p_eval_shard_0001_pkey; Type: INDEX ATTACH; Schema: public; Owner: -
--

ALTER INDEX public.eval_set_items_pkey ATTACH PARTITION public.eval_set_items_p_eval_shard_0001_pkey;


--
-- Name: eval_set_items_p_eval_shard_000_shard_id_eval_set_id_source_idx; Type: INDEX ATTACH; Schema: public; Owner: -
--

ALTER INDEX public.idx_eval_set_items_set_source ATTACH PARTITION public.eval_set_items_p_eval_shard_000_shard_id_eval_set_id_source_idx;


--
-- Name: eval_set_items_p_eval_shard_0_shard_id_eval_set_id_created__idx; Type: INDEX ATTACH; Schema: public; Owner: -
--

ALTER INDEX public.idx_eval_set_items_set_created ATTACH PARTITION public.eval_set_items_p_eval_shard_0_shard_id_eval_set_id_created__idx;


--
-- Name: eval_set_items_p_eval_shard_0_shard_id_eval_set_id_question_idx; Type: INDEX ATTACH; Schema: public; Owner: -
--

ALTER INDEX public.idx_eval_set_items_set_type ATTACH PARTITION public.eval_set_items_p_eval_shard_0_shard_id_eval_set_id_question_idx;


--
-- Name: eval_set_items_p_eval_shard_0_shard_id_eval_set_id_updated__idx; Type: INDEX ATTACH; Schema: public; Owner: -
--

ALTER INDEX public.idx_eval_set_items_set_updated ATTACH PARTITION public.eval_set_items_p_eval_shard_0_shard_id_eval_set_id_updated__idx;


--
-- Name: eval_set_items fk_eval_set_items_set; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE public.eval_set_items
    ADD CONSTRAINT fk_eval_set_items_set FOREIGN KEY (eval_set_id) REFERENCES public.eval_sets(id);


--
-- Name: eval_set_items fk_eval_set_items_shard; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE public.eval_set_items
    ADD CONSTRAINT fk_eval_set_items_shard FOREIGN KEY (shard_id) REFERENCES public.eval_set_shards(id);


--
-- Name: eval_sets fk_eval_sets_shard; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.eval_sets
    ADD CONSTRAINT fk_eval_sets_shard FOREIGN KEY (shard_id) REFERENCES public.eval_set_shards(id);


--
-- Name: plugin_human_artifacts plugin_human_artifacts_session_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.plugin_human_artifacts
    ADD CONSTRAINT plugin_human_artifacts_session_id_fkey FOREIGN KEY (session_id) REFERENCES public.plugin_sessions(id);


--
-- Name: plugin_session_steps plugin_session_steps_session_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.plugin_session_steps
    ADD CONSTRAINT plugin_session_steps_session_id_fkey FOREIGN KEY (session_id) REFERENCES public.plugin_sessions(id);


--
-- Name: plugin_slot_revisions plugin_slot_revisions_human_artifact_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.plugin_slot_revisions
    ADD CONSTRAINT plugin_slot_revisions_human_artifact_id_fkey FOREIGN KEY (human_artifact_id) REFERENCES public.plugin_human_artifacts(id);


--
-- Name: plugin_slot_revisions plugin_slot_revisions_session_id_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.plugin_slot_revisions
    ADD CONSTRAINT plugin_slot_revisions_session_id_fkey FOREIGN KEY (session_id) REFERENCES public.plugin_sessions(id);

-- Net data transformations for rows that may already exist at the init version.
UPDATE public.default_models
SET model_type = CASE model_type
    WHEN 'VLM' THEN 'vlm'
    WHEN 'embedding' THEN 'embed'
    WHEN 'embed_main' THEN 'embed'
    WHEN 'multimodal_embedding' THEN 'cross_modal_embed'
    WHEN 'embed_image' THEN 'cross_modal_embed'
    WHEN 'reranker' THEN 'rerank'
    ELSE model_type
END
WHERE model_type IN ('VLM', 'embedding', 'embed_main', 'multimodal_embedding', 'embed_image', 'reranker');

UPDATE public.user_model_provider_group_models
SET model_type = CASE model_type
    WHEN 'VLM' THEN 'vlm'
    WHEN 'embedding' THEN 'embed'
    WHEN 'embed_main' THEN 'embed'
    WHEN 'multimodal_embedding' THEN 'cross_modal_embed'
    WHEN 'embed_image' THEN 'cross_modal_embed'
    WHEN 'reranker' THEN 'rerank'
    ELSE model_type
END
WHERE model_type IN ('VLM', 'embedding', 'embed_main', 'multimodal_embedding', 'embed_image', 'reranker');

UPDATE public.user_selected_models
SET model_type = CASE model_type
    WHEN 'llm-chat' THEN 'llm'
    WHEN 'llm-evo' THEN 'evo_llm'
    WHEN 'llm2' THEN 'evo_llm'
    WHEN 'VLM' THEN 'vlm'
    WHEN 'embedding' THEN 'embed_main'
    WHEN 'multimodal_embedding' THEN 'embed_image'
    WHEN 'rerank' THEN 'reranker'
    ELSE model_type
END
WHERE model_type IN ('llm-chat', 'llm-evo', 'llm2', 'VLM', 'embedding', 'multimodal_embedding', 'rerank');

-- Seed data whose final state is not represented by schema DDL.
INSERT INTO public.eval_set_shards (
    id, status, row_limit, row_open_threshold, size_limit_bytes,
    size_open_threshold_bytes, actual_rows, estimated_bytes,
    created_at, updated_at
) VALUES (
    'eval_shard_0001', 'open', 200000, 120000, 8589934592,
    5368709120, 0, 0, now(), now()
) ON CONFLICT (id) DO NOTHING;

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

CREATE TABLE IF NOT EXISTS `plugin_generation_analyses` (`id` varchar(36),`draft_id` varchar(36) NOT NULL,`user_id` varchar(255) NOT NULL,`source_type` varchar(16) NOT NULL,`source_skill_id` varchar(36) NOT NULL DEFAULT "",`source_skill_revision_id` varchar(36) NOT NULL DEFAULT "",`source_skill_revision_no` integer NOT NULL DEFAULT 0,`source_skill_tree_hash` varchar(64) NOT NULL DEFAULT "",`status` varchar(32) NOT NULL,`verdict_code` varchar(64) NOT NULL DEFAULT "",`verdict_message` text NOT NULL DEFAULT "",`candidates_json` text NOT NULL DEFAULT "[]",`selected_candidate_id` varchar(128) NOT NULL DEFAULT "",`coverage_report_json` text NOT NULL DEFAULT "{}",`tool_mapping_report_json` text NOT NULL DEFAULT "{}",`script_report_json` text NOT NULL DEFAULT "{}",`source_package_json` text NOT NULL DEFAULT "{}",`created_at` datetime NOT NULL,`updated_at` datetime NOT NULL,PRIMARY KEY (`id`));

CREATE TABLE IF NOT EXISTS `plugin_human_artifacts` (`id` varchar(36),`session_id` varchar(36) NOT NULL,`slot` varchar(64) NOT NULL,`content_type` varchar(32) NOT NULL,`value` jsonb NOT NULL,`caption` text,`created_at` datetime NOT NULL,PRIMARY KEY (`id`));

CREATE TABLE IF NOT EXISTS `plugin_revision_entries` (`revision_id` varchar(36),`path` varchar(1024),`entry_type` varchar(16) NOT NULL DEFAULT "file",`blob_hash` varchar(64),`size` integer NOT NULL DEFAULT 0,`mime` varchar(128),`file_type` varchar(32) NOT NULL DEFAULT "unknown",`is_binary` numeric NOT NULL DEFAULT false,`mode` integer NOT NULL DEFAULT 420,PRIMARY KEY (`revision_id`,`path`));

CREATE TABLE IF NOT EXISTS `plugin_revisions` (`id` varchar(36),`plugin_resource_id` varchar(36) NOT NULL,`parent_revision_id` varchar(36),`revision_no` integer NOT NULL,`tree_hash` varchar(64) NOT NULL,`compiled_graph` jsonb,`graph_hash` varchar(64) NOT NULL DEFAULT "",`graph_schema_version` varchar(16) NOT NULL DEFAULT "",`message` text NOT NULL DEFAULT "",`created_by` varchar(255),`created_at` datetime NOT NULL,PRIMARY KEY (`id`));

CREATE TABLE IF NOT EXISTS `plugin_repair_runs` (`id` varchar(36),`draft_id` varchar(36) NOT NULL,`user_id` varchar(255) NOT NULL,`base_plugin_revision_id` varchar(36) NOT NULL DEFAULT "",`draft_version_before` integer NOT NULL,`target` varchar(32) NOT NULL,`mode` varchar(32) NOT NULL,`source_analysis_id` varchar(36) NOT NULL DEFAULT "",`source_skill_revision_id` varchar(36) NOT NULL DEFAULT "",`repair_hint` text NOT NULL DEFAULT "",`diagnostics_before_json` text NOT NULL DEFAULT "{}",`changes_json` text NOT NULL DEFAULT "{}",`diagnostics_after_json` text NOT NULL DEFAULT "{}",`status` varchar(32) NOT NULL,`created_at` datetime NOT NULL,`updated_at` datetime NOT NULL,PRIMARY KEY (`id`));

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

CREATE TABLE IF NOT EXISTS `skill_market_installs` (`market_item_id` varchar(36) NOT NULL,`user_id` varchar(255) NOT NULL,`skill_id` varchar(36) NOT NULL,`created_at` datetime NOT NULL,`updated_at` datetime NOT NULL,PRIMARY KEY (`market_item_id`,`user_id`));

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

CREATE INDEX IF NOT EXISTS `idx_plugin_generation_analyses_draft` ON `plugin_generation_analyses`(`draft_id`,`created_at`);

CREATE INDEX IF NOT EXISTS `idx_plugin_repair_runs_draft` ON `plugin_repair_runs`(`draft_id`,`created_at`);

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

CREATE INDEX IF NOT EXISTS `idx_skill_market_installs_skill` ON `skill_market_installs`(`skill_id`);

CREATE INDEX IF NOT EXISTS `idx_skill_market_installs_user` ON `skill_market_installs`(`user_id`,`market_item_id`);

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
