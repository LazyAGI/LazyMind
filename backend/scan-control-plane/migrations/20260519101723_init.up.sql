CREATE TABLE public.sources (
    source_id text PRIMARY KEY,
    tenant_id text,
    created_by text NOT NULL,
    name text NOT NULL,
    dataset_id text NOT NULL,
    status text NOT NULL,
    source_options_json jsonb,
    include_extensions_json jsonb,
    exclude_extensions_json jsonb,
    config_version bigint NOT NULL DEFAULT 1,
    deleted_at timestamp with time zone,
    created_at timestamp with time zone NOT NULL,
    updated_at timestamp with time zone NOT NULL
);

CREATE INDEX idx_sources_user_updated
    ON public.sources (created_by, updated_at DESC, source_id);
CREATE INDEX idx_sources_name
    ON public.sources (name);

CREATE TABLE public.source_bindings (
    binding_id text PRIMARY KEY,
    source_id text NOT NULL REFERENCES public.sources(source_id),
    binding_type text NOT NULL,
    connector_type text NOT NULL,
    target_type text NOT NULL,
    target_ref text NOT NULL,
    target_fingerprint text NOT NULL,
    agent_id text,
    auth_connection_id text,
    provider_options_json jsonb,
    tree_key text NOT NULL,
    binding_generation bigint NOT NULL DEFAULT 1,
    core_parent_document_id text NOT NULL,
    core_parent_document_name text NOT NULL,
    sync_mode text NOT NULL,
    schedule_policy_json jsonb,
    next_sync_at timestamp with time zone,
    include_extensions_json jsonb,
    exclude_extensions_json jsonb,
    status text NOT NULL,
    last_error jsonb,
    deleted_at timestamp with time zone,
    created_at timestamp with time zone NOT NULL,
    updated_at timestamp with time zone NOT NULL,
    chat_enabled boolean NOT NULL DEFAULT TRUE
);

CREATE INDEX idx_source_bindings_source
    ON public.source_bindings (source_id, status);
CREATE UNIQUE INDEX uk_source_binding_current_target
    ON public.source_bindings (source_id, connector_type, target_type, target_fingerprint)
    WHERE status <> 'DELETING';

CREATE TABLE public.source_object_index (
    source_id text NOT NULL REFERENCES public.sources(source_id),
    binding_id text NOT NULL REFERENCES public.source_bindings(binding_id),
    tree_key text NOT NULL,
    object_key text NOT NULL,
    parent_key text,
    display_name text NOT NULL,
    search_name text NOT NULL,
    object_type text NOT NULL,
    is_document boolean NOT NULL,
    is_container boolean NOT NULL,
    has_children boolean NOT NULL,
    source_version text,
    size_bytes bigint,
    mime_type text,
    file_extension text,
    modified_at timestamp with time zone,
    deleted_at_source timestamp with time zone,
    depth bigint NOT NULL,
    provider_meta_json jsonb,
    last_seen_run_id text,
    created_at timestamp with time zone NOT NULL,
    updated_at timestamp with time zone NOT NULL,
    PRIMARY KEY (binding_id, object_key)
);

CREATE INDEX idx_source_object_children
    ON public.source_object_index (source_id, binding_id, tree_key, parent_key, display_name);
CREATE INDEX idx_source_object_search
    ON public.source_object_index (source_id, binding_id, search_name);

CREATE TABLE public.source_document_states (
    source_id text NOT NULL REFERENCES public.sources(source_id),
    binding_id text NOT NULL REFERENCES public.source_bindings(binding_id),
    binding_generation bigint NOT NULL,
    object_key text NOT NULL,
    source_version text,
    baseline_version text,
    deleted_at_source timestamp with time zone,
    source_state text NOT NULL,
    sync_state text NOT NULL,
    pending_action text,
    document_list_visible boolean NOT NULL,
    selectable boolean NOT NULL,
    parse_queue_state text,
    document_id text,
    active_task_id text,
    last_detected_at timestamp with time zone,
    last_synced_at timestamp with time zone,
    last_error jsonb,
    created_at timestamp with time zone NOT NULL,
    updated_at timestamp with time zone NOT NULL,
    PRIMARY KEY (source_id, binding_id, object_key)
);

CREATE INDEX idx_source_document_states_binding_state
    ON public.source_document_states (source_id, binding_id, source_state, sync_state);

CREATE TABLE public.documents (
    document_id text PRIMARY KEY,
    tenant_id text,
    source_id text NOT NULL REFERENCES public.sources(source_id),
    binding_id text NOT NULL REFERENCES public.source_bindings(binding_id),
    object_key text NOT NULL,
    core_document_id text,
    current_version_id text,
    desired_version_id text,
    source_version text,
    display_name text NOT NULL,
    mime_type text,
    file_extension text,
    parse_status text NOT NULL,
    created_at timestamp with time zone NOT NULL,
    updated_at timestamp with time zone NOT NULL
);

CREATE UNIQUE INDEX uk_documents_object
    ON public.documents (source_id, binding_id, object_key);
CREATE INDEX idx_documents_binding
    ON public.documents (source_id, binding_id, parse_status);

CREATE TABLE public.parse_tasks (
    task_id text PRIMARY KEY,
    tenant_id text,
    source_id text NOT NULL REFERENCES public.sources(source_id),
    binding_id text NOT NULL REFERENCES public.source_bindings(binding_id),
    binding_generation bigint NOT NULL,
    object_key text NOT NULL,
    document_id text NOT NULL REFERENCES public.documents(document_id),
    task_action text NOT NULL,
    target_version_id text NOT NULL,
    source_version text NOT NULL,
    core_parent_document_id text NOT NULL,
    idempotency_key text NOT NULL,
    status text NOT NULL,
    core_task_id text,
    core_document_id text,
    lease_owner text,
    lease_until timestamp with time zone,
    retry_count bigint NOT NULL DEFAULT 0,
    next_run_at timestamp with time zone NOT NULL,
    last_error jsonb,
    created_at timestamp with time zone NOT NULL,
    updated_at timestamp with time zone NOT NULL
);

CREATE INDEX idx_parse_tasks_due
    ON public.parse_tasks (status, next_run_at);
CREATE UNIQUE INDEX uk_parse_task_idempotency
    ON public.parse_tasks (idempotency_key);
CREATE UNIQUE INDEX uk_parse_task_active
    ON public.parse_tasks (source_id, binding_id, object_key, target_version_id, task_action)
    WHERE status IN ('PENDING', 'RUNNING', 'SUBMITTED');

CREATE TABLE public.source_sync_checkpoints (
    source_id text NOT NULL REFERENCES public.sources(source_id),
    binding_id text PRIMARY KEY REFERENCES public.source_bindings(binding_id),
    binding_generation bigint NOT NULL,
    cursor text,
    next_sync_at timestamp with time zone,
    last_sync_at timestamp with time zone,
    last_success_at timestamp with time zone,
    lock_owner text,
    lock_until timestamp with time zone,
    retry_count bigint NOT NULL DEFAULT 0,
    last_error jsonb,
    created_at timestamp with time zone NOT NULL,
    updated_at timestamp with time zone NOT NULL
);

CREATE INDEX idx_source_sync_due
    ON public.source_sync_checkpoints (next_sync_at, lock_until);

CREATE TABLE public.source_sync_runs (
    run_id text PRIMARY KEY,
    source_id text NOT NULL REFERENCES public.sources(source_id),
    binding_id text NOT NULL REFERENCES public.source_bindings(binding_id),
    binding_generation bigint NOT NULL,
    trigger_type text NOT NULL,
    scheduled_fire_at timestamp with time zone,
    scope_type text NOT NULL,
    scope_ref_json jsonb,
    coverage_json jsonb,
    status text NOT NULL,
    seen_count bigint NOT NULL DEFAULT 0,
    new_count bigint NOT NULL DEFAULT 0,
    modified_count bigint NOT NULL DEFAULT 0,
    deleted_count bigint NOT NULL DEFAULT 0,
    unchanged_count bigint NOT NULL DEFAULT 0,
    error_code text,
    error_message text,
    started_at timestamp with time zone NOT NULL,
    finished_at timestamp with time zone
);

CREATE INDEX idx_source_sync_runs_binding_started
    ON public.source_sync_runs (binding_id, started_at DESC);
CREATE UNIQUE INDEX uk_source_sync_runs_scheduled_fire
    ON public.source_sync_runs (binding_id, binding_generation, scheduled_fire_at)
    WHERE trigger_type = 'scheduled'
      AND scheduled_fire_at IS NOT NULL
      AND status IN ('PENDING', 'RUNNING');

CREATE TABLE public.data_source_create_operations (
    operation_id text PRIMARY KEY,
    caller_id text NOT NULL,
    request_id text NOT NULL,
    request_hash text NOT NULL,
    source_id text,
    dataset_id text,
    created_core_parent_document_ids_json jsonb,
    created_binding_ids_json jsonb,
    warning_json jsonb,
    status text NOT NULL,
    compensation_status text NOT NULL,
    compensation_error jsonb,
    created_at timestamp with time zone NOT NULL,
    updated_at timestamp with time zone NOT NULL
);

CREATE UNIQUE INDEX uk_create_operation
    ON public.data_source_create_operations (caller_id, request_id);

CREATE TABLE public.agents (
    agent_id text PRIMARY KEY,
    tenant_id text NOT NULL,
    hostname text NOT NULL,
    version text NOT NULL,
    status text NOT NULL,
    listen_addr text NOT NULL,
    last_heartbeat_at timestamp with time zone NOT NULL,
    active_source_count bigint NOT NULL DEFAULT 0,
    active_watch_count bigint NOT NULL DEFAULT 0,
    active_task_count bigint NOT NULL DEFAULT 0,
    updated_at timestamp with time zone NOT NULL
);

CREATE INDEX idx_agents_tenant_status
    ON public.agents (tenant_id, status, updated_at DESC);

CREATE TABLE public.agent_commands (
    command_id text PRIMARY KEY,
    agent_id text NOT NULL REFERENCES public.agents(agent_id),
    command_type text NOT NULL,
    payload_json jsonb NOT NULL,
    status text NOT NULL,
    attempt_count bigint NOT NULL DEFAULT 0,
    next_retry_at timestamp with time zone,
    acked_at timestamp with time zone,
    last_error jsonb,
    result_json jsonb,
    created_at timestamp with time zone NOT NULL,
    dispatched_at timestamp with time zone
);

CREATE INDEX idx_agent_commands_pending
    ON public.agent_commands (agent_id, status, next_retry_at, created_at);

CREATE TABLE public.parse_task_dead_letters (
    dead_letter_id text PRIMARY KEY,
    task_id text NOT NULL,
    tenant_id text,
    source_id text NOT NULL,
    binding_id text NOT NULL,
    binding_generation bigint NOT NULL,
    object_key text NOT NULL,
    document_id text NOT NULL,
    task_action text NOT NULL,
    target_version_id text NOT NULL,
    retry_count bigint NOT NULL,
    error_code text,
    last_error jsonb,
    failed_at timestamp with time zone NOT NULL,
    created_at timestamp with time zone NOT NULL
);

CREATE INDEX idx_parse_task_dead_letters_task
    ON public.parse_task_dead_letters (task_id);
CREATE INDEX idx_parse_task_dead_letters_failed_at
    ON public.parse_task_dead_letters (failed_at DESC);
