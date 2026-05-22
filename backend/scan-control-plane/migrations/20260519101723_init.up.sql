-- 20260519101723_init
-- +migrate Up

CREATE TABLE public.agent_commands (
    id bigint NOT NULL,
    agent_id text NOT NULL,
    type text NOT NULL,
    payload text NOT NULL,
    status text NOT NULL,
    attempt_count bigint DEFAULT 0 NOT NULL,
    next_retry_at timestamp with time zone,
    acked_at timestamp with time zone,
    last_error text,
    result_json text,
    created_at timestamp with time zone NOT NULL,
    dispatched_at timestamp with time zone
);



CREATE SEQUENCE public.agent_commands_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;



ALTER SEQUENCE public.agent_commands_id_seq OWNED BY public.agent_commands.id;



CREATE TABLE public.agents (
    agent_id text NOT NULL,
    tenant_id text NOT NULL,
    hostname text NOT NULL,
    version text NOT NULL,
    status text NOT NULL,
    listen_addr text NOT NULL,
    last_heartbeat_at timestamp with time zone NOT NULL,
    active_source_count bigint DEFAULT 0 NOT NULL,
    active_watch_count bigint DEFAULT 0 NOT NULL,
    active_task_count bigint DEFAULT 0 NOT NULL,
    updated_at timestamp with time zone NOT NULL
);



CREATE TABLE public.cloud_object_index (
    id bigint NOT NULL,
    source_id text NOT NULL,
    provider text NOT NULL,
    external_object_id text NOT NULL,
    external_parent_id text,
    external_path text,
    external_name text,
    external_kind text,
    external_version text,
    external_modified_at timestamp with time zone,
    local_rel_path text,
    local_abs_path text,
    checksum text,
    size_bytes bigint DEFAULT 0 NOT NULL,
    is_deleted boolean DEFAULT false NOT NULL,
    last_synced_at timestamp with time zone,
    provider_meta_json text,
    created_at timestamp with time zone NOT NULL,
    updated_at timestamp with time zone NOT NULL
);



CREATE SEQUENCE public.cloud_object_index_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;



ALTER SEQUENCE public.cloud_object_index_id_seq OWNED BY public.cloud_object_index.id;



CREATE TABLE public.cloud_source_bindings (
    source_id text NOT NULL,
    tenant_id text NOT NULL,
    provider text NOT NULL,
    enabled boolean DEFAULT true NOT NULL,
    status text NOT NULL,
    auth_connection_id text NOT NULL,
    target_type text,
    target_ref text,
    schedule_expr text NOT NULL,
    schedule_tz text NOT NULL,
    reconcile_after_sync boolean DEFAULT true NOT NULL,
    reconcile_delay_minutes bigint DEFAULT 10 NOT NULL,
    include_patterns_json text,
    exclude_patterns_json text,
    max_object_size_bytes bigint DEFAULT 0 NOT NULL,
    provider_options_json text,
    last_error text,
    created_at timestamp with time zone NOT NULL,
    updated_at timestamp with time zone NOT NULL
);



CREATE TABLE public.cloud_sync_checkpoints (
    source_id text NOT NULL,
    provider text NOT NULL,
    next_sync_at timestamp with time zone,
    last_sync_at timestamp with time zone,
    last_success_at timestamp with time zone,
    last_run_id text,
    remote_cursor text,
    lock_owner text,
    lock_until timestamp with time zone,
    updated_at timestamp with time zone NOT NULL
);



CREATE TABLE public.cloud_sync_runs (
    run_id text NOT NULL,
    source_id text NOT NULL,
    tenant_id text NOT NULL,
    provider text NOT NULL,
    trigger_type text NOT NULL,
    requested_paths_json text,
    status text NOT NULL,
    started_at timestamp with time zone,
    finished_at timestamp with time zone,
    remote_total bigint DEFAULT 0 NOT NULL,
    created_count bigint DEFAULT 0 NOT NULL,
    updated_count bigint DEFAULT 0 NOT NULL,
    deleted_count bigint DEFAULT 0 NOT NULL,
    skipped_count bigint DEFAULT 0 NOT NULL,
    failed_count bigint DEFAULT 0 NOT NULL,
    error_code text,
    error_message text,
    created_at timestamp with time zone NOT NULL,
    updated_at timestamp with time zone NOT NULL
);



CREATE TABLE public.documents (
    id bigint NOT NULL,
    tenant_id text NOT NULL,
    source_id text NOT NULL,
    source_object_id text NOT NULL,
    core_document_id text,
    current_version_id text,
    desired_version_id text,
    last_modified_at timestamp with time zone,
    next_parse_at timestamp with time zone,
    parse_status text NOT NULL,
    origin_type text DEFAULT 'LOCAL_FS'::text NOT NULL,
    origin_platform text DEFAULT 'LOCAL'::text NOT NULL,
    origin_ref text,
    trigger_policy text DEFAULT 'IDLE_WINDOW'::text NOT NULL,
    updated_at timestamp with time zone NOT NULL
);



CREATE SEQUENCE public.documents_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;



ALTER SEQUENCE public.documents_id_seq OWNED BY public.documents.id;



CREATE TABLE public.manual_pull_jobs (
    job_id text NOT NULL,
    tenant_id text NOT NULL,
    source_id text NOT NULL,
    status text NOT NULL,
    mode text NOT NULL,
    trigger_policy text,
    selection_token text,
    updated_only boolean DEFAULT false NOT NULL,
    requested_count bigint DEFAULT 0 NOT NULL,
    accepted_count bigint DEFAULT 0 NOT NULL,
    skipped_count bigint DEFAULT 0 NOT NULL,
    ignored_unchanged_count bigint DEFAULT 0 NOT NULL,
    error_message text,
    created_at timestamp with time zone NOT NULL,
    updated_at timestamp with time zone NOT NULL,
    finished_at timestamp with time zone
);



CREATE TABLE public.parse_task_dead_letters (
    id bigint NOT NULL,
    task_id bigint NOT NULL,
    tenant_id text NOT NULL,
    document_id bigint NOT NULL,
    target_version_id text NOT NULL,
    retry_count bigint NOT NULL,
    origin_type text NOT NULL,
    origin_platform text NOT NULL,
    trigger_policy text NOT NULL,
    last_error text NOT NULL,
    failed_at timestamp with time zone NOT NULL,
    created_at timestamp with time zone NOT NULL
);



CREATE SEQUENCE public.parse_task_dead_letters_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;



ALTER SEQUENCE public.parse_task_dead_letters_id_seq OWNED BY public.parse_task_dead_letters.id;



CREATE TABLE public.parse_tasks (
    id bigint NOT NULL,
    tenant_id text NOT NULL,
    document_id bigint NOT NULL,
    task_action text DEFAULT 'CREATE'::text NOT NULL,
    target_version_id text NOT NULL,
    origin_type text DEFAULT 'LOCAL_FS'::text NOT NULL,
    origin_platform text DEFAULT 'LOCAL'::text NOT NULL,
    trigger_policy text DEFAULT 'IDLE_WINDOW'::text NOT NULL,
    status text NOT NULL,
    core_dataset_id text,
    core_document_id text,
    core_task_id text,
    scan_orchestration_status text,
    submit_error_message text,
    submit_at timestamp with time zone,
    idempotency_key text,
    selection_token text,
    next_run_at timestamp with time zone NOT NULL,
    retry_count bigint DEFAULT 0 NOT NULL,
    max_retry_count bigint DEFAULT 8 NOT NULL,
    lease_owner text,
    lease_until timestamp with time zone,
    started_at timestamp with time zone,
    finished_at timestamp with time zone,
    last_error text,
    created_at timestamp with time zone NOT NULL,
    updated_at timestamp with time zone NOT NULL
);



CREATE SEQUENCE public.parse_tasks_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;



ALTER SEQUENCE public.parse_tasks_id_seq OWNED BY public.parse_tasks.id;



CREATE TABLE public.reconcile_snapshots (
    source_id text NOT NULL,
    snapshot_ref text NOT NULL,
    file_count bigint DEFAULT 0 NOT NULL,
    taken_at timestamp with time zone NOT NULL,
    updated_at timestamp with time zone NOT NULL
);



CREATE TABLE public.source_baseline_snapshots (
    source_id text NOT NULL,
    snapshot_ref text NOT NULL,
    file_count bigint DEFAULT 0 NOT NULL,
    taken_at timestamp with time zone NOT NULL,
    reason text NOT NULL,
    updated_at timestamp with time zone NOT NULL
);



CREATE TABLE public.source_document_states (
    id bigint NOT NULL,
    tenant_id text NOT NULL,
    source_id text NOT NULL,
    object_key text NOT NULL,
    path text NOT NULL,
    name text,
    is_dir boolean DEFAULT false NOT NULL,
    source_exists boolean DEFAULT true NOT NULL,
    origin_type text,
    origin_platform text,
    origin_ref text,
    source_version text,
    baseline_version text,
    source_checksum text,
    source_size_bytes bigint DEFAULT 0 NOT NULL,
    source_modified_at timestamp with time zone,
    source_state text NOT NULL,
    sync_state text NOT NULL,
    pending_action text NOT NULL,
    next_sync_at timestamp with time zone,
    document_id bigint,
    core_document_id text,
    active_task_id bigint,
    last_detected_at timestamp with time zone NOT NULL,
    last_synced_at timestamp with time zone,
    last_error text,
    deleted_at_source timestamp with time zone,
    knowledge_base_seen boolean DEFAULT false NOT NULL,
    created_at timestamp with time zone NOT NULL,
    updated_at timestamp with time zone NOT NULL
);



CREATE SEQUENCE public.source_document_states_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;



ALTER SEQUENCE public.source_document_states_id_seq OWNED BY public.source_document_states.id;



CREATE TABLE public.source_file_snapshot_items (
    id bigint NOT NULL,
    snapshot_id text NOT NULL,
    path text NOT NULL,
    is_dir boolean DEFAULT false NOT NULL,
    size_bytes bigint DEFAULT 0 NOT NULL,
    mod_time timestamp with time zone,
    checksum text,
    external_file_id text
);



CREATE SEQUENCE public.source_file_snapshot_items_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;



ALTER SEQUENCE public.source_file_snapshot_items_id_seq OWNED BY public.source_file_snapshot_items.id;



CREATE TABLE public.source_file_snapshots (
    snapshot_id text NOT NULL,
    source_id text NOT NULL,
    tenant_id text NOT NULL,
    snapshot_type text NOT NULL,
    base_snapshot_id text,
    selection_token text,
    expires_at timestamp with time zone,
    consumed_at timestamp with time zone,
    file_count bigint DEFAULT 0 NOT NULL,
    created_at timestamp with time zone NOT NULL
);



CREATE TABLE public.source_snapshot_relations (
    source_id text NOT NULL,
    last_preview_snapshot_id text,
    last_committed_snapshot_id text,
    updated_at timestamp with time zone NOT NULL
);



CREATE TABLE public.sources (
    id text NOT NULL,
    tenant_id text NOT NULL,
    create_user_id text DEFAULT ''::text NOT NULL,
    name text NOT NULL,
    source_type text NOT NULL,
    root_path text NOT NULL,
    status text NOT NULL,
    watch_enabled boolean DEFAULT false NOT NULL,
    watch_updated_at timestamp with time zone,
    idle_window_seconds bigint NOT NULL,
    reconcile_seconds bigint NOT NULL,
    reconcile_schedule text,
    agent_id text NOT NULL,
    dataset_id text,
    default_origin_type text DEFAULT 'LOCAL_FS'::text NOT NULL,
    default_origin_platform text DEFAULT 'LOCAL'::text NOT NULL,
    default_trigger_policy text DEFAULT 'IDLE_WINDOW'::text NOT NULL,
    created_at timestamp with time zone NOT NULL,
    updated_at timestamp with time zone NOT NULL
);



ALTER TABLE ONLY public.agent_commands ALTER COLUMN id SET DEFAULT nextval('public.agent_commands_id_seq'::regclass);



ALTER TABLE ONLY public.cloud_object_index ALTER COLUMN id SET DEFAULT nextval('public.cloud_object_index_id_seq'::regclass);



ALTER TABLE ONLY public.documents ALTER COLUMN id SET DEFAULT nextval('public.documents_id_seq'::regclass);



ALTER TABLE ONLY public.parse_task_dead_letters ALTER COLUMN id SET DEFAULT nextval('public.parse_task_dead_letters_id_seq'::regclass);



ALTER TABLE ONLY public.parse_tasks ALTER COLUMN id SET DEFAULT nextval('public.parse_tasks_id_seq'::regclass);



ALTER TABLE ONLY public.source_document_states ALTER COLUMN id SET DEFAULT nextval('public.source_document_states_id_seq'::regclass);



ALTER TABLE ONLY public.source_file_snapshot_items ALTER COLUMN id SET DEFAULT nextval('public.source_file_snapshot_items_id_seq'::regclass);



ALTER TABLE ONLY public.agent_commands
    ADD CONSTRAINT agent_commands_pkey PRIMARY KEY (id);



ALTER TABLE ONLY public.agents
    ADD CONSTRAINT agents_pkey PRIMARY KEY (agent_id);



ALTER TABLE ONLY public.cloud_object_index
    ADD CONSTRAINT cloud_object_index_pkey PRIMARY KEY (id);



ALTER TABLE ONLY public.cloud_source_bindings
    ADD CONSTRAINT cloud_source_bindings_pkey PRIMARY KEY (source_id);



ALTER TABLE ONLY public.cloud_sync_checkpoints
    ADD CONSTRAINT cloud_sync_checkpoints_pkey PRIMARY KEY (source_id);



ALTER TABLE ONLY public.cloud_sync_runs
    ADD CONSTRAINT cloud_sync_runs_pkey PRIMARY KEY (run_id);



ALTER TABLE ONLY public.documents
    ADD CONSTRAINT documents_pkey PRIMARY KEY (id);



ALTER TABLE ONLY public.manual_pull_jobs
    ADD CONSTRAINT manual_pull_jobs_pkey PRIMARY KEY (job_id);



ALTER TABLE ONLY public.parse_task_dead_letters
    ADD CONSTRAINT parse_task_dead_letters_pkey PRIMARY KEY (id);



ALTER TABLE ONLY public.parse_tasks
    ADD CONSTRAINT parse_tasks_pkey PRIMARY KEY (id);



ALTER TABLE ONLY public.reconcile_snapshots
    ADD CONSTRAINT reconcile_snapshots_pkey PRIMARY KEY (source_id);



ALTER TABLE ONLY public.source_baseline_snapshots
    ADD CONSTRAINT source_baseline_snapshots_pkey PRIMARY KEY (source_id);



ALTER TABLE ONLY public.source_document_states
    ADD CONSTRAINT source_document_states_pkey PRIMARY KEY (id);



ALTER TABLE ONLY public.source_file_snapshot_items
    ADD CONSTRAINT source_file_snapshot_items_pkey PRIMARY KEY (id);



ALTER TABLE ONLY public.source_file_snapshots
    ADD CONSTRAINT source_file_snapshots_pkey PRIMARY KEY (snapshot_id);



ALTER TABLE ONLY public.source_snapshot_relations
    ADD CONSTRAINT source_snapshot_relations_pkey PRIMARY KEY (source_id);



ALTER TABLE ONLY public.sources
    ADD CONSTRAINT sources_pkey PRIMARY KEY (id);



CREATE INDEX idx_agent_commands_pending ON public.agent_commands USING btree (agent_id, status, next_retry_at);



CREATE INDEX idx_cloud_bindings_provider_status ON public.cloud_source_bindings USING btree (provider, status);



CREATE INDEX idx_cloud_bindings_tenant_status ON public.cloud_source_bindings USING btree (tenant_id, status);



CREATE INDEX idx_cloud_object_provider ON public.cloud_object_index USING btree (provider);



CREATE INDEX idx_cloud_object_source ON public.cloud_object_index USING btree (source_id);



CREATE INDEX idx_cloud_sync_checkpoints_lock_until ON public.cloud_sync_checkpoints USING btree (lock_until);



CREATE INDEX idx_cloud_sync_checkpoints_next_sync_at ON public.cloud_sync_checkpoints USING btree (next_sync_at);



CREATE INDEX idx_cloud_sync_runs_provider ON public.cloud_sync_runs USING btree (provider);



CREATE INDEX idx_cloud_sync_runs_source_started ON public.cloud_sync_runs USING btree (source_id, started_at);



CREATE INDEX idx_cloud_sync_runs_status ON public.cloud_sync_runs USING btree (status);



CREATE INDEX idx_cloud_sync_runs_tenant ON public.cloud_sync_runs USING btree (tenant_id);



CREATE INDEX idx_documents_core_document ON public.documents USING btree (core_document_id);



CREATE INDEX idx_documents_next_parse ON public.documents USING btree (next_parse_at);



CREATE INDEX idx_manual_pull_jobs_source_created ON public.manual_pull_jobs USING btree (tenant_id, source_id, created_at);



CREATE INDEX idx_manual_pull_jobs_source_status ON public.manual_pull_jobs USING btree (status);



CREATE INDEX idx_parse_task_dead_letters_failed_at ON public.parse_task_dead_letters USING btree (failed_at);



CREATE INDEX idx_parse_task_dead_letters_task ON public.parse_task_dead_letters USING btree (task_id);



CREATE INDEX idx_parse_tasks_core_document ON public.parse_tasks USING btree (core_document_id);



CREATE INDEX idx_parse_tasks_core_task ON public.parse_tasks USING btree (core_task_id);



CREATE INDEX idx_parse_tasks_document ON public.parse_tasks USING btree (document_id);



CREATE INDEX idx_parse_tasks_idempotency ON public.parse_tasks USING btree (idempotency_key);



CREATE INDEX idx_parse_tasks_lease ON public.parse_tasks USING btree (lease_owner, lease_until);



CREATE INDEX idx_parse_tasks_orchestration_status ON public.parse_tasks USING btree (scan_orchestration_status);



CREATE INDEX idx_parse_tasks_tenant_status_updated ON public.parse_tasks USING btree (tenant_id, status, updated_at);



CREATE INDEX idx_source_document_states_active_task ON public.source_document_states USING btree (active_task_id);



CREATE INDEX idx_source_document_states_document ON public.source_document_states USING btree (document_id);



CREATE INDEX idx_source_document_states_next_sync ON public.source_document_states USING btree (source_id, next_sync_at);



CREATE INDEX idx_source_document_states_path ON public.source_document_states USING btree (source_id, path);



CREATE INDEX idx_source_document_states_source_state ON public.source_document_states USING btree (source_id, source_state, sync_state);



CREATE INDEX idx_source_document_states_tenant_source ON public.source_document_states USING btree (tenant_id, source_id);



CREATE INDEX idx_source_file_snapshot_items_path ON public.source_file_snapshot_items USING btree (path);



CREATE INDEX idx_source_file_snapshot_items_snapshot ON public.source_file_snapshot_items USING btree (snapshot_id);



CREATE INDEX idx_source_file_snapshots_expires_at ON public.source_file_snapshots USING btree (expires_at);



CREATE UNIQUE INDEX idx_source_file_snapshots_selection_token ON public.source_file_snapshots USING btree (selection_token) WHERE (selection_token <> ''::text);



CREATE INDEX idx_source_file_snapshots_source_created ON public.source_file_snapshots USING btree (source_id, created_at);



CREATE INDEX idx_source_file_snapshots_tenant_created ON public.source_file_snapshots USING btree (tenant_id, created_at);



CREATE INDEX idx_sources_tenant ON public.sources USING btree (tenant_id);



CREATE INDEX idx_sources_tenant_creator ON public.sources USING btree (tenant_id, create_user_id);



CREATE UNIQUE INDEX uk_cloud_object ON public.cloud_object_index USING btree (source_id, external_object_id);



CREATE UNIQUE INDEX uk_documents_scope ON public.documents USING btree (tenant_id, source_id, source_object_id);



CREATE UNIQUE INDEX uk_parse_task_document_pending ON public.parse_tasks USING btree (document_id) WHERE (status = ANY (ARRAY['PENDING'::text, 'RETRY_WAITING'::text]));



CREATE UNIQUE INDEX uk_source_document_state_object ON public.source_document_states USING btree (source_id, object_key);



CREATE UNIQUE INDEX uk_source_snapshot_item_path ON public.source_file_snapshot_items USING btree (snapshot_id, path);



CREATE UNIQUE INDEX uk_sources_tenant_agent_root ON public.sources USING btree (tenant_id, create_user_id, agent_id, root_path);




