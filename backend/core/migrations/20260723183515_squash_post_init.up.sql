-- 20260723183515_squash_post_init
-- +migrate Up
-- +migrate Supersedes: 20260506120000, 20260521000000, 20260527120000, 20260527130000, 20260529100000, 20260531090000, 20260531093000, 20260531100000, 20260602120000, 20260604100000, 20260604120000, 20260605120000, 20260608120000, 20260609120000, 20260610120000, 20260610123000, 20260611100000, 20260611110000, 20260612100000, 20260612110000, 20260612120000, 20260613120000, 20260615120000, 20260615140000, 20260615200000, 20260617100000, 20260618090000, 20260618100000, 20260620120000, 20260622100000, 20260622120000, 20260622130000, 20260622200000, 20260622210000, 20260625100000, 20260625200000, 20260626100000, 20260626120000, 20260626200000, 20260626210000, 20260626220000, 20260626230000, 20260629100000, 20260630120000, 20260701090000, 20260701120000, 20260701130000, 20260703120000, 20260703180000, 20260704180000, 20260706120000, 20260706120001, 20260706170000, 20260707100001, 20260707120000, 20260707160000, 20260708120000, 20260709103300, 20260709103400, 20260709120000, 20260709130000, 20260709140000, 20260709150000, 20260710113000, 20260710120000, 20260710123000, 20260710180000, 20260711120000, 20260713100000, 20260713110000, 20260713170000, 20260713190000, 20260713200000, 20260714120000, 20260714130000, 20260714170000, 20260714190000, 20260715100000, 20260715170000, 20260716150000, 20260716160000, 20260717120000, 20260719112959, 20260719120000, 20260721180000, 20260721182344, 20260722142937

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
