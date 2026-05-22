-- 20260321131500_init
-- +migrate Up

CREATE TABLE public.acl_groups (
    id character varying(255) NOT NULL,
    name character varying(255) DEFAULT ''::character varying NOT NULL
);



CREATE TABLE public.acl_kbs (
    id character varying(64) NOT NULL,
    name character varying(255),
    owner_id character varying(255),
    visibility character varying(32)
);



CREATE TABLE public.acl_rows (
    id bigint NOT NULL,
    resource_type character varying(32),
    resource_id character varying(255),
    grantee_type character varying(32),
    target_id character varying(255),
    permission character varying(32),
    created_by character varying(255),
    created_at timestamp with time zone,
    expires_at timestamp with time zone
);



CREATE SEQUENCE public.acl_rows_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;



ALTER SEQUENCE public.acl_rows_id_seq OWNED BY public.acl_rows.id;



CREATE TABLE public.acl_user_groups (
    user_id character varying(255) NOT NULL,
    group_id character varying(255) NOT NULL
);



CREATE TABLE public.acl_visibility (
    id bigint NOT NULL,
    resource_id character varying(255),
    level character varying(32)
);



CREATE SEQUENCE public.acl_visibility_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;



ALTER SEQUENCE public.acl_visibility_id_seq OWNED BY public.acl_visibility.id;



CREATE TABLE public.agent_thread_records (
    id character varying(32) NOT NULL,
    thread_id character varying(128) NOT NULL,
    round_id character varying(32) DEFAULT ''::character varying NOT NULL,
    task_id character varying(128) DEFAULT ''::character varying NOT NULL,
    stream_kind character varying(32) NOT NULL,
    record_key character varying(64) NOT NULL,
    event_name character varying(128) DEFAULT ''::character varying NOT NULL,
    payload_text text DEFAULT ''::text NOT NULL,
    raw_frame text DEFAULT ''::text NOT NULL,
    created_at timestamp with time zone NOT NULL,
    updated_at timestamp with time zone NOT NULL
);



CREATE TABLE public.agent_thread_rounds (
    round_id character varying(32) NOT NULL,
    thread_id character varying(128) NOT NULL,
    request_hash character varying(64) DEFAULT ''::character varying NOT NULL,
    task_id character varying(128) DEFAULT ''::character varying NOT NULL,
    status character varying(32) DEFAULT 'created'::character varying NOT NULL,
    user_message text DEFAULT ''::text NOT NULL,
    assistant_message text DEFAULT ''::text NOT NULL,
    request_payload text DEFAULT ''::text NOT NULL,
    created_at timestamp with time zone NOT NULL,
    updated_at timestamp with time zone NOT NULL
);



CREATE TABLE public.agent_threads (
    thread_id character varying(128) NOT NULL,
    current_task_id character varying(128) DEFAULT ''::character varying NOT NULL,
    status character varying(32) DEFAULT 'created'::character varying NOT NULL,
    thread_payload text DEFAULT ''::text NOT NULL,
    last_message_request_hash character varying(64) DEFAULT ''::character varying NOT NULL,
    create_user_id character varying(255) DEFAULT ''::character varying NOT NULL,
    create_user_name character varying(255) DEFAULT ''::character varying NOT NULL,
    created_at timestamp with time zone NOT NULL,
    updated_at timestamp with time zone NOT NULL
);



CREATE TABLE public.agent_user_active_threads (
    user_id character varying(255) NOT NULL,
    thread_id character varying(128) DEFAULT ''::character varying NOT NULL,
    status character varying(32) DEFAULT 'creating'::character varying NOT NULL,
    create_token character varying(64) DEFAULT ''::character varying NOT NULL,
    lease_until timestamp with time zone NOT NULL,
    created_at timestamp with time zone NOT NULL,
    updated_at timestamp with time zone NOT NULL
);



CREATE TABLE public.chat_histories (
    id character varying(36) NOT NULL,
    seq bigint NOT NULL,
    conversation_id character varying(36) NOT NULL,
    raw_content text,
    retrieval_result json,
    content text,
    result text,
    feed_back bigint DEFAULT 0,
    reason character varying(255),
    expected_answer text,
    ext json,
    version character varying(128) DEFAULT '2.3'::character varying,
    create_time timestamp with time zone NOT NULL,
    update_time timestamp with time zone NOT NULL
);



CREATE TABLE public.conversations (
    id character varying(36) NOT NULL,
    display_name character varying(255),
    channel_id character varying(36) DEFAULT 'default'::character varying NOT NULL,
    search_config json,
    application_id character varying(64) DEFAULT ''::character varying,
    ext json,
    model character varying(64) DEFAULT ''::character varying,
    models json,
    chat_times integer DEFAULT 0 NOT NULL,
    create_user_id character varying(255) NOT NULL,
    create_user_name character varying(255) NOT NULL,
    created_at timestamp with time zone NOT NULL,
    updated_at timestamp with time zone NOT NULL,
    deleted_at timestamp with time zone
);



CREATE TABLE public.dataset_members (
    id character varying(36) NOT NULL,
    dataset_id character varying(36) NOT NULL,
    tenant_member_id character varying(36) NOT NULL,
    role boolean NOT NULL,
    resource_id character varying(36) NOT NULL,
    name character varying(64) NOT NULL,
    create_time timestamp with time zone NOT NULL,
    update_time timestamp with time zone NOT NULL
);



CREATE TABLE public.datasets (
    id character varying(255) NOT NULL,
    kb_id character varying(255) NOT NULL,
    display_name character varying(255) NOT NULL,
    "desc" text NOT NULL,
    cover_image character varying(255) NOT NULL,
    resource_uid character varying(36) NOT NULL,
    bucket_name character varying(255) NOT NULL,
    oss_path character varying(255) NOT NULL,
    dataset_info json,
    dataset_state smallint NOT NULL,
    embedding_model character varying(255) NOT NULL,
    embedding_model_provider character varying(255) NOT NULL,
    share_type smallint NOT NULL,
    shared_at timestamp with time zone,
    tenant_id character varying(36) NOT NULL,
    is_demonstrate boolean DEFAULT false NOT NULL,
    type smallint DEFAULT 1 NOT NULL,
    ext json,
    create_user_id character varying(255) NOT NULL,
    create_user_name character varying(255) NOT NULL,
    created_at timestamp with time zone NOT NULL,
    updated_at timestamp with time zone NOT NULL,
    deleted_at timestamp with time zone
);



CREATE TABLE public.default_datasets (
    id bigint NOT NULL,
    dataset_id character varying(64) NOT NULL,
    dataset_name character varying(255) NOT NULL,
    create_user_id character varying(255) NOT NULL,
    create_user_name character varying(255) NOT NULL,
    created_at timestamp with time zone NOT NULL,
    updated_at timestamp with time zone NOT NULL,
    deleted_at timestamp with time zone
);



CREATE SEQUENCE public.default_datasets_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;



ALTER SEQUENCE public.default_datasets_id_seq OWNED BY public.default_datasets.id;



CREATE TABLE public.default_model_providers (
    id character varying(64) NOT NULL,
    name character varying(255) NOT NULL,
    description text NOT NULL,
    base_url character varying(1024) DEFAULT ''::character varying NOT NULL,
    created_at timestamp with time zone NOT NULL,
    updated_at timestamp with time zone NOT NULL,
    deleted_at timestamp with time zone
);



CREATE TABLE public.default_models (
    id character varying(64) NOT NULL,
    default_model_provider_id character varying(64) NOT NULL,
    provider_name character varying(255) DEFAULT ''::character varying NOT NULL,
    name character varying(512) NOT NULL,
    model_type character varying(64) NOT NULL,
    base_url character varying(1024) DEFAULT ''::character varying NOT NULL,
    created_at timestamp with time zone NOT NULL,
    updated_at timestamp with time zone NOT NULL,
    deleted_at timestamp with time zone
);



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



CREATE SEQUENCE public.default_prompts_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;



ALTER SEQUENCE public.default_prompts_id_seq OWNED BY public.default_prompts.id;



CREATE TABLE public.documents (
    id character varying(128) NOT NULL,
    lazyllm_doc_id character varying(128) DEFAULT ''::character varying NOT NULL,
    dataset_id character varying(255) NOT NULL,
    display_name character varying(512) DEFAULT ''::character varying NOT NULL,
    p_id character varying(255) DEFAULT ''::character varying NOT NULL,
    tags json,
    file_id character varying(128) DEFAULT ''::character varying NOT NULL,
    pdf_convert_result character varying(64) DEFAULT ''::character varying NOT NULL,
    ext json,
    create_user_id character varying(255) NOT NULL,
    create_user_name character varying(255) NOT NULL,
    created_at timestamp with time zone NOT NULL,
    updated_at timestamp with time zone NOT NULL,
    deleted_at timestamp with time zone
);



CREATE TABLE public.multi_answers_chat_histories (
    id character varying(36) NOT NULL,
    seq bigint NOT NULL,
    conversation_id character varying(36) NOT NULL,
    raw_content text,
    retrieval_result json,
    content text,
    result text,
    feed_back bigint DEFAULT 0,
    reason character varying(255),
    ext json,
    endpoint character varying(512),
    create_time timestamp with time zone NOT NULL,
    update_time timestamp with time zone NOT NULL
);



CREATE TABLE public.multi_answers_switches (
    id integer NOT NULL,
    status integer DEFAULT 0 NOT NULL,
    create_user_id character varying(255) NOT NULL,
    create_user_name character varying(255) NOT NULL,
    created_at timestamp with time zone NOT NULL,
    updated_at timestamp with time zone NOT NULL,
    deleted_at timestamp with time zone
);



CREATE SEQUENCE public.multi_answers_switches_id_seq
    AS integer
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;



ALTER SEQUENCE public.multi_answers_switches_id_seq OWNED BY public.multi_answers_switches.id;



CREATE TABLE public.prompts (
    id character varying(64) NOT NULL,
    name character varying(255) NOT NULL,
    content text NOT NULL,
    create_user_id character varying(255) NOT NULL,
    create_user_name character varying(255) NOT NULL,
    created_at timestamp with time zone NOT NULL,
    updated_at timestamp with time zone NOT NULL,
    deleted_at timestamp with time zone
);



CREATE TABLE public.resource_session_snapshots (
    id character varying(36) NOT NULL,
    session_id character varying(128) NOT NULL,
    user_id character varying(255) DEFAULT ''::character varying NOT NULL,
    resource_type character varying(32) NOT NULL,
    resource_key character varying(1024) NOT NULL,
    category character varying(128) DEFAULT ''::character varying NOT NULL,
    parent_skill_name character varying(255) DEFAULT ''::character varying NOT NULL,
    skill_name character varying(255) DEFAULT ''::character varying NOT NULL,
    file_ext character varying(32) DEFAULT ''::character varying NOT NULL,
    relative_path character varying(1024) DEFAULT ''::character varying NOT NULL,
    snapshot_hash character varying(64) DEFAULT ''::character varying NOT NULL,
    storage_path text DEFAULT ''::text NOT NULL,
    created_at timestamp with time zone NOT NULL
);



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



CREATE TABLE public.skill_share_items (
    id character varying(36) NOT NULL,
    share_task_id character varying(36) NOT NULL,
    target_user_id character varying(255) NOT NULL,
    target_user_name character varying(255) DEFAULT ''::character varying NOT NULL,
    status character varying(32) NOT NULL,
    target_relative_root character varying(1024) DEFAULT ''::character varying NOT NULL,
    target_storage_path text DEFAULT ''::text NOT NULL,
    accepted_at timestamp with time zone,
    rejected_at timestamp with time zone,
    target_root_skill_id character varying(36) DEFAULT ''::character varying NOT NULL,
    error_message text,
    created_at timestamp with time zone NOT NULL,
    updated_at timestamp with time zone NOT NULL
);



CREATE TABLE public.skill_share_tasks (
    id character varying(36) NOT NULL,
    source_user_id character varying(255) NOT NULL,
    source_user_name character varying(255) DEFAULT ''::character varying NOT NULL,
    source_skill_id character varying(36) NOT NULL,
    source_category character varying(128) DEFAULT ''::character varying NOT NULL,
    source_parent_skill_name character varying(255) DEFAULT ''::character varying NOT NULL,
    source_relative_root character varying(1024) DEFAULT ''::character varying NOT NULL,
    source_storage_root text DEFAULT ''::text NOT NULL,
    message text,
    created_at timestamp with time zone NOT NULL,
    updated_at timestamp with time zone NOT NULL
);



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



CREATE TABLE public.system_user_preferences (
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



CREATE TABLE public.tasks (
    id character varying(128) NOT NULL,
    lazyllm_task_id character varying(128) DEFAULT ''::character varying NOT NULL,
    doc_id character varying(128),
    kb_id character varying(255),
    algo_id character varying(255),
    dataset_id character varying(255) NOT NULL,
    task_type character varying(128) DEFAULT ''::character varying NOT NULL,
    document_pid character varying(255) DEFAULT ''::character varying NOT NULL,
    target_pid character varying(255) DEFAULT ''::character varying NOT NULL,
    target_dataset_id character varying(255) DEFAULT ''::character varying NOT NULL,
    display_name character varying(512) DEFAULT ''::character varying NOT NULL,
    ext json,
    create_user_id character varying(255) NOT NULL,
    create_user_name character varying(255) NOT NULL,
    created_at timestamp with time zone NOT NULL,
    updated_at timestamp with time zone NOT NULL,
    deleted_at timestamp with time zone
);



CREATE TABLE public.upload_sessions (
    id bigint NOT NULL,
    upload_id character varying(128) NOT NULL,
    task_id character varying(128) NOT NULL,
    dataset_id character varying(255) NOT NULL,
    tenant_id character varying(36) NOT NULL,
    document_id character varying(128) NOT NULL,
    upload_state character varying(64) DEFAULT ''::character varying NOT NULL,
    ext json,
    create_user_id character varying(255) NOT NULL,
    create_user_name character varying(255) NOT NULL,
    created_at timestamp with time zone NOT NULL,
    updated_at timestamp with time zone NOT NULL,
    deleted_at timestamp with time zone
);



CREATE SEQUENCE public.upload_sessions_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;



ALTER SEQUENCE public.upload_sessions_id_seq OWNED BY public.upload_sessions.id;



CREATE TABLE public.uploaded_files (
    id bigint NOT NULL,
    upload_file_id character varying(128) NOT NULL,
    dataset_id character varying(255) NOT NULL,
    tenant_id character varying(36) NOT NULL,
    task_id character varying(128) DEFAULT ''::character varying NOT NULL,
    document_id character varying(128) DEFAULT ''::character varying NOT NULL,
    status character varying(64) DEFAULT ''::character varying NOT NULL,
    ext json,
    create_user_id character varying(255) NOT NULL,
    create_user_name character varying(255) NOT NULL,
    created_at timestamp with time zone NOT NULL,
    updated_at timestamp with time zone NOT NULL,
    deleted_at timestamp with time zone
);



CREATE SEQUENCE public.uploaded_files_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;



ALTER SEQUENCE public.uploaded_files_id_seq OWNED BY public.uploaded_files.id;



CREATE TABLE public.user_model_provider_group_models (
    id character varying(64) NOT NULL,
    user_model_provider_id character varying(64) NOT NULL,
    user_model_provider_group_id character varying(64) NOT NULL,
    provider_name character varying(255) DEFAULT ''::character varying NOT NULL,
    name character varying(512) NOT NULL,
    model_type character varying(64) NOT NULL,
    base_url character varying(1024) DEFAULT ''::character varying NOT NULL,
    is_default boolean DEFAULT false NOT NULL,
    create_user_id character varying(255) NOT NULL,
    create_user_name character varying(255) NOT NULL,
    created_at timestamp with time zone NOT NULL,
    updated_at timestamp with time zone NOT NULL,
    deleted_at timestamp with time zone
);



CREATE TABLE public.user_model_provider_groups (
    id character varying(64) NOT NULL,
    user_model_provider_id character varying(64) NOT NULL,
    name character varying(255) NOT NULL,
    base_url character varying(1024) NOT NULL,
    api_key text NOT NULL,
    create_user_id character varying(255) NOT NULL,
    create_user_name character varying(255) NOT NULL,
    created_at timestamp with time zone NOT NULL,
    updated_at timestamp with time zone NOT NULL,
    deleted_at timestamp with time zone,
    is_verified boolean DEFAULT false NOT NULL
);



CREATE TABLE public.user_model_providers (
    id character varying(64) NOT NULL,
    default_model_provider_id character varying(64) NOT NULL,
    name character varying(255) NOT NULL,
    description text NOT NULL,
    base_url character varying(1024) DEFAULT ''::character varying NOT NULL,
    create_user_id character varying(255) NOT NULL,
    create_user_name character varying(255) NOT NULL,
    created_at timestamp with time zone NOT NULL,
    updated_at timestamp with time zone NOT NULL,
    deleted_at timestamp with time zone
);



CREATE TABLE public.user_personalization_settings (
    id bigint NOT NULL,
    user_id character varying(255) NOT NULL,
    enabled boolean DEFAULT true NOT NULL,
    updated_by character varying(255) DEFAULT ''::character varying NOT NULL,
    updated_by_name character varying(255) DEFAULT ''::character varying NOT NULL,
    created_at timestamp with time zone NOT NULL,
    updated_at timestamp with time zone NOT NULL
);



CREATE SEQUENCE public.user_personalization_settings_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;



ALTER SEQUENCE public.user_personalization_settings_id_seq OWNED BY public.user_personalization_settings.id;



CREATE TABLE public.user_selected_models (
    id bigint NOT NULL,
    user_id character varying(255) NOT NULL,
    user_name character varying(255) DEFAULT ''::character varying NOT NULL,
    model_type character varying(64) NOT NULL,
    user_model_provider_group_model_id character varying(64) NOT NULL,
    created_at timestamp with time zone NOT NULL,
    updated_at timestamp with time zone NOT NULL
);



CREATE SEQUENCE public.user_selected_models_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;



ALTER SEQUENCE public.user_selected_models_id_seq OWNED BY public.user_selected_models.id;



CREATE TABLE public.word_group_conflicts (
    id character varying(64) NOT NULL,
    reason text DEFAULT ''::text NOT NULL,
    word text DEFAULT ''::text NOT NULL,
    description text DEFAULT ''::text NOT NULL,
    group_ids text DEFAULT '[]'::text NOT NULL,
    create_user_id character varying(255) NOT NULL,
    message_ids text DEFAULT '[]'::text NOT NULL,
    created_at timestamp with time zone NOT NULL,
    updated_at timestamp with time zone NOT NULL,
    deleted_at timestamp with time zone
);



CREATE TABLE public.words (
    id character varying(64) NOT NULL,
    word character varying(512) NOT NULL,
    group_id character varying(64) NOT NULL,
    description character varying(512) DEFAULT ''::character varying NOT NULL,
    source character varying(32) DEFAULT 'user'::character varying NOT NULL,
    reference_info text DEFAULT ''::text NOT NULL,
    locked boolean DEFAULT false NOT NULL,
    word_kind character varying(32) DEFAULT 'term'::character varying NOT NULL,
    create_user_id character varying(255) NOT NULL,
    create_user_name character varying(255) NOT NULL,
    created_at timestamp with time zone NOT NULL,
    updated_at timestamp with time zone NOT NULL,
    deleted_at timestamp with time zone
);



ALTER TABLE ONLY public.acl_rows ALTER COLUMN id SET DEFAULT nextval('public.acl_rows_id_seq'::regclass);



ALTER TABLE ONLY public.acl_visibility ALTER COLUMN id SET DEFAULT nextval('public.acl_visibility_id_seq'::regclass);



ALTER TABLE ONLY public.default_datasets ALTER COLUMN id SET DEFAULT nextval('public.default_datasets_id_seq'::regclass);



ALTER TABLE ONLY public.default_prompts ALTER COLUMN id SET DEFAULT nextval('public.default_prompts_id_seq'::regclass);



ALTER TABLE ONLY public.multi_answers_switches ALTER COLUMN id SET DEFAULT nextval('public.multi_answers_switches_id_seq'::regclass);



ALTER TABLE ONLY public.upload_sessions ALTER COLUMN id SET DEFAULT nextval('public.upload_sessions_id_seq'::regclass);



ALTER TABLE ONLY public.uploaded_files ALTER COLUMN id SET DEFAULT nextval('public.uploaded_files_id_seq'::regclass);



ALTER TABLE ONLY public.user_personalization_settings ALTER COLUMN id SET DEFAULT nextval('public.user_personalization_settings_id_seq'::regclass);



ALTER TABLE ONLY public.user_selected_models ALTER COLUMN id SET DEFAULT nextval('public.user_selected_models_id_seq'::regclass);



ALTER TABLE ONLY public.acl_groups
    ADD CONSTRAINT acl_groups_pkey PRIMARY KEY (id);



ALTER TABLE ONLY public.acl_kbs
    ADD CONSTRAINT acl_kbs_pkey PRIMARY KEY (id);



ALTER TABLE ONLY public.acl_rows
    ADD CONSTRAINT acl_rows_pkey PRIMARY KEY (id);



ALTER TABLE ONLY public.acl_user_groups
    ADD CONSTRAINT acl_user_groups_pkey PRIMARY KEY (user_id, group_id);



ALTER TABLE ONLY public.acl_visibility
    ADD CONSTRAINT acl_visibility_pkey PRIMARY KEY (id);



ALTER TABLE ONLY public.agent_thread_records
    ADD CONSTRAINT agent_thread_records_pkey PRIMARY KEY (id);



ALTER TABLE ONLY public.agent_thread_rounds
    ADD CONSTRAINT agent_thread_rounds_pkey PRIMARY KEY (round_id);



ALTER TABLE ONLY public.agent_threads
    ADD CONSTRAINT agent_threads_pkey PRIMARY KEY (thread_id);



ALTER TABLE ONLY public.agent_user_active_threads
    ADD CONSTRAINT agent_user_active_threads_pkey PRIMARY KEY (user_id);



ALTER TABLE ONLY public.chat_histories
    ADD CONSTRAINT chat_histories_pkey PRIMARY KEY (id);



ALTER TABLE ONLY public.conversations
    ADD CONSTRAINT conversations_pkey PRIMARY KEY (id);



ALTER TABLE ONLY public.dataset_members
    ADD CONSTRAINT dataset_members_pkey PRIMARY KEY (id);



ALTER TABLE ONLY public.datasets
    ADD CONSTRAINT datasets_pkey PRIMARY KEY (id);



ALTER TABLE ONLY public.default_datasets
    ADD CONSTRAINT default_datasets_pkey PRIMARY KEY (id);



ALTER TABLE ONLY public.default_model_providers
    ADD CONSTRAINT default_model_providers_pkey PRIMARY KEY (id);



ALTER TABLE ONLY public.default_models
    ADD CONSTRAINT default_models_pkey PRIMARY KEY (id);



ALTER TABLE ONLY public.default_prompts
    ADD CONSTRAINT default_prompts_pkey PRIMARY KEY (id);



ALTER TABLE ONLY public.documents
    ADD CONSTRAINT documents_pkey PRIMARY KEY (id);



ALTER TABLE ONLY public.multi_answers_chat_histories
    ADD CONSTRAINT multi_answers_chat_histories_pkey PRIMARY KEY (id);



ALTER TABLE ONLY public.multi_answers_switches
    ADD CONSTRAINT multi_answers_switches_pkey PRIMARY KEY (id);



ALTER TABLE ONLY public.prompts
    ADD CONSTRAINT prompts_pkey PRIMARY KEY (id);



ALTER TABLE ONLY public.resource_session_snapshots
    ADD CONSTRAINT resource_session_snapshots_pkey PRIMARY KEY (id);



ALTER TABLE ONLY public.resource_suggestions
    ADD CONSTRAINT resource_suggestions_pkey PRIMARY KEY (id);



ALTER TABLE ONLY public.skill_resources
    ADD CONSTRAINT skill_resources_pkey PRIMARY KEY (id);



ALTER TABLE ONLY public.skill_share_items
    ADD CONSTRAINT skill_share_items_pkey PRIMARY KEY (id);



ALTER TABLE ONLY public.skill_share_tasks
    ADD CONSTRAINT skill_share_tasks_pkey PRIMARY KEY (id);



ALTER TABLE ONLY public.system_memories
    ADD CONSTRAINT system_memories_pkey PRIMARY KEY (id);



ALTER TABLE ONLY public.system_user_preferences
    ADD CONSTRAINT system_user_preferences_pkey PRIMARY KEY (id);



ALTER TABLE ONLY public.tasks
    ADD CONSTRAINT tasks_pkey PRIMARY KEY (id);



ALTER TABLE ONLY public.upload_sessions
    ADD CONSTRAINT upload_sessions_pkey PRIMARY KEY (id);



ALTER TABLE ONLY public.uploaded_files
    ADD CONSTRAINT uploaded_files_pkey PRIMARY KEY (id);



ALTER TABLE ONLY public.user_model_provider_group_models
    ADD CONSTRAINT user_model_provider_group_models_pkey PRIMARY KEY (id);



ALTER TABLE ONLY public.user_model_provider_groups
    ADD CONSTRAINT user_model_provider_groups_pkey PRIMARY KEY (id);



ALTER TABLE ONLY public.user_model_providers
    ADD CONSTRAINT user_model_providers_pkey PRIMARY KEY (id);



ALTER TABLE ONLY public.user_personalization_settings
    ADD CONSTRAINT user_personalization_settings_pkey PRIMARY KEY (id);



ALTER TABLE ONLY public.user_selected_models
    ADD CONSTRAINT user_selected_models_pkey PRIMARY KEY (id);



ALTER TABLE ONLY public.word_group_conflicts
    ADD CONSTRAINT word_group_conflicts_pkey PRIMARY KEY (id);



ALTER TABLE ONLY public.words
    ADD CONSTRAINT words_pkey PRIMARY KEY (id);



CREATE UNIQUE INDEX datasetmember_dataset_id_tenant_member_id_role ON public.dataset_members USING btree (dataset_id, tenant_member_id, role);



CREATE INDEX datasetmember_name ON public.dataset_members USING btree (name);



CREATE INDEX datasetmember_resource_id ON public.dataset_members USING btree (resource_id);



CREATE INDEX datasetmember_tenant_member_id ON public.dataset_members USING btree (tenant_member_id);



CREATE INDEX idx_acl_resource ON public.acl_rows USING btree (resource_type, resource_id);



CREATE INDEX idx_acl_visibility_resource_id ON public.acl_visibility USING btree (resource_id);



CREATE INDEX idx_agent_thread_records_round_stream_id ON public.agent_thread_records USING btree (round_id, stream_kind, id);



CREATE INDEX idx_agent_thread_records_thread_round_id ON public.agent_thread_records USING btree (thread_id, round_id);



CREATE INDEX idx_agent_thread_records_thread_stream_id ON public.agent_thread_records USING btree (thread_id, stream_kind, id);



CREATE INDEX idx_agent_thread_rounds_thread_id ON public.agent_thread_rounds USING btree (thread_id, created_at);



CREATE INDEX idx_agent_thread_rounds_thread_request_hash ON public.agent_thread_rounds USING btree (thread_id, request_hash);



CREATE INDEX idx_agent_threads_current_task_id ON public.agent_threads USING btree (current_task_id);



CREATE INDEX idx_agent_user_active_threads_status_lease ON public.agent_user_active_threads USING btree (status, lease_until);



CREATE INDEX idx_agent_user_active_threads_thread_id ON public.agent_user_active_threads USING btree (thread_id);



CREATE INDEX idx_chat_histories_conversation_id ON public.chat_histories USING btree (conversation_id);



CREATE INDEX idx_create_user_id ON public.datasets USING btree (create_user_id);



CREATE INDEX idx_datasets_kb_id ON public.datasets USING btree (kb_id);



CREATE INDEX idx_documents_dataset_id ON public.documents USING btree (dataset_id);



CREATE INDEX idx_documents_lazyllm_doc_id ON public.documents USING btree (lazyllm_doc_id);



CREATE INDEX idx_documents_p_id ON public.documents USING btree (p_id);



CREATE INDEX idx_multi_answers_chat_histories_conversation_id ON public.multi_answers_chat_histories USING btree (conversation_id);



CREATE INDEX idx_resource_session_snapshots_session_id ON public.resource_session_snapshots USING btree (session_id);



CREATE INDEX idx_resource_suggestions_list ON public.resource_suggestions USING btree (user_id, resource_type, status);



CREATE INDEX idx_resource_suggestions_session_id ON public.resource_suggestions USING btree (session_id);



CREATE INDEX idx_resource_uid ON public.datasets USING btree (resource_uid);



CREATE INDEX idx_skill_resources_owner_node_enabled ON public.skill_resources USING btree (owner_user_id, node_type, is_enabled, category);



CREATE INDEX idx_skill_share_items_target_user ON public.skill_share_items USING btree (share_task_id, target_user_id, status);



CREATE INDEX idx_skill_share_tasks_source_user ON public.skill_share_tasks USING btree (source_user_id);



CREATE INDEX idx_tasks_algo_id ON public.tasks USING btree (algo_id);



CREATE INDEX idx_tasks_dataset_id ON public.tasks USING btree (dataset_id);



CREATE INDEX idx_tasks_doc_id ON public.tasks USING btree (doc_id);



CREATE INDEX idx_tasks_document_pid ON public.tasks USING btree (document_pid);



CREATE INDEX idx_tasks_kb_id ON public.tasks USING btree (kb_id);



CREATE INDEX idx_tasks_lazyllm_task_id ON public.tasks USING btree (lazyllm_task_id);



CREATE INDEX idx_tasks_target_dataset_id ON public.tasks USING btree (target_dataset_id);



CREATE INDEX idx_tasks_task_type ON public.tasks USING btree (task_type);



CREATE INDEX idx_upload_sessions_dataset_id ON public.upload_sessions USING btree (dataset_id);



CREATE INDEX idx_upload_sessions_document_id ON public.upload_sessions USING btree (document_id);



CREATE INDEX idx_upload_sessions_task_id ON public.upload_sessions USING btree (task_id);



CREATE INDEX idx_upload_sessions_upload_state ON public.upload_sessions USING btree (upload_state);



CREATE INDEX idx_uploaded_files_dataset_id ON public.uploaded_files USING btree (dataset_id);



CREATE INDEX idx_uploaded_files_document_id ON public.uploaded_files USING btree (document_id);



CREATE INDEX idx_uploaded_files_status ON public.uploaded_files USING btree (status);



CREATE INDEX idx_uploaded_files_task_id ON public.uploaded_files USING btree (task_id);



CREATE INDEX idx_uploaded_files_tenant_id ON public.uploaded_files USING btree (tenant_id);



CREATE INDEX idx_user_model_provider_group_models_create_user_id ON public.user_model_provider_group_models USING btree (create_user_id);



CREATE INDEX idx_user_model_provider_group_models_provider ON public.user_model_provider_group_models USING btree (user_model_provider_id);



CREATE INDEX idx_user_model_provider_groups_create_user_id ON public.user_model_provider_groups USING btree (create_user_id);



CREATE INDEX idx_user_model_provider_groups_parent ON public.user_model_provider_groups USING btree (user_model_provider_id);



CREATE INDEX idx_user_model_providers_create_user_id ON public.user_model_providers USING btree (create_user_id);



CREATE INDEX idx_user_selected_models_user_id ON public.user_selected_models USING btree (user_id);



CREATE INDEX idx_word_column ON public.words USING btree (create_user_id, word);



CREATE INDEX idx_word_create_user_group_id ON public.words USING btree (create_user_id, group_id);



CREATE INDEX idx_word_group_conflict_user_updated ON public.word_group_conflicts USING btree (create_user_id, updated_at DESC) WHERE (deleted_at IS NULL);



CREATE UNIQUE INDEX uk_agent_thread_records_record_key ON public.agent_thread_records USING btree (thread_id, round_id, stream_kind, record_key);



CREATE UNIQUE INDEX uk_default_model_providers_name ON public.default_model_providers USING btree (name);



CREATE UNIQUE INDEX uk_default_models_provider_name ON public.default_models USING btree (default_model_provider_id, name);



CREATE UNIQUE INDEX uk_prompts_user_name ON public.prompts USING btree (create_user_id, name);



CREATE UNIQUE INDEX uk_resource_session_snapshots ON public.resource_session_snapshots USING btree (session_id, resource_type, resource_key);



CREATE UNIQUE INDEX uk_skill_resources_owner_relative_path ON public.skill_resources USING btree (owner_user_id, relative_path);



CREATE UNIQUE INDEX uk_system_memories_user_id ON public.system_memories USING btree (user_id);



CREATE UNIQUE INDEX uk_system_user_preferences_user_id ON public.system_user_preferences USING btree (user_id);



CREATE UNIQUE INDEX uk_upload_sessions_upload_id ON public.upload_sessions USING btree (upload_id);



CREATE UNIQUE INDEX uk_uploaded_files_upload_file_id ON public.uploaded_files USING btree (upload_file_id);



CREATE UNIQUE INDEX uk_user_model_provider_group_models_group_name ON public.user_model_provider_group_models USING btree (user_model_provider_group_id, name);



CREATE UNIQUE INDEX uk_user_model_providers_user_default_provider ON public.user_model_providers USING btree (create_user_id, default_model_provider_id);



CREATE UNIQUE INDEX uk_user_personalization_settings_user_id ON public.user_personalization_settings USING btree (user_id);



CREATE UNIQUE INDEX uk_user_selected_models_user_type ON public.user_selected_models USING btree (user_id, model_type);



CREATE UNIQUE INDEX ukx_create_user_id_dataset_id ON public.default_datasets USING btree (create_user_id, dataset_id);




