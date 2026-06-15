-- 20260609120000_add_resource_update_schema
-- +migrate Up

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
    CONSTRAINT resource_update_tasks_pkey PRIMARY KEY (id),
    CONSTRAINT chk_resource_update_tasks_task_type CHECK ((task_type)::text IN ('generate_review', 'auto_apply_review')),
    CONSTRAINT chk_resource_update_tasks_resource_type CHECK ((resource_type)::text IN ('skill', 'memory', 'user_preference')),
    CONSTRAINT chk_resource_update_tasks_trigger_type CHECK ((trigger_type)::text IN ('scheduled', 'conversation_idle', 'review_result', 'auto_evo_enabled')),
    CONSTRAINT chk_resource_update_tasks_status CHECK ((status)::text IN ('pending', 'running', 'done', 'failed', 'skipped')),
    CONSTRAINT chk_resource_update_tasks_attempt_count_non_negative CHECK (attempt_count >= 0)
);

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
    CONSTRAINT skill_review_scheduler_state_pkey PRIMARY KEY (user_id),
    CONSTRAINT chk_skill_review_scheduler_stage_index_non_negative CHECK (stage_index >= 0),
    CONSTRAINT chk_skill_review_scheduler_stage_success_count_non_negative CHECK (stage_success_count >= 0),
    CONSTRAINT chk_skill_review_scheduler_total_success_count_non_negative CHECK (total_success_count >= 0)
);

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
    CONSTRAINT conversation_idle_events_pkey PRIMARY KEY (id),
    CONSTRAINT chk_conversation_idle_events_status CHECK ((status)::text IN ('waiting', 'processing', 'triggered', 'skipped', 'failed'))
);

ALTER TABLE public.chat_histories
    ADD COLUMN tool_call_turns integer DEFAULT 0 NOT NULL;

ALTER TABLE public.chat_histories
    ADD CONSTRAINT chk_chat_histories_tool_call_turns_non_negative CHECK (tool_call_turns >= 0);

CREATE INDEX idx_resource_update_tasks_task_type ON public.resource_update_tasks(task_type);
CREATE INDEX idx_resource_update_tasks_resource_type ON public.resource_update_tasks(resource_type);
CREATE INDEX idx_resource_update_tasks_user_id ON public.resource_update_tasks(user_id);
CREATE INDEX idx_resource_update_tasks_resource_id ON public.resource_update_tasks(resource_id);
CREATE INDEX idx_resource_update_tasks_trigger_type ON public.resource_update_tasks(trigger_type);
CREATE INDEX idx_resource_update_tasks_trigger_id ON public.resource_update_tasks(trigger_id);
CREATE INDEX idx_resource_update_tasks_status ON public.resource_update_tasks(status);
CREATE INDEX idx_resource_update_tasks_review_result_id ON public.resource_update_tasks(review_result_id);
CREATE INDEX idx_resource_update_tasks_result_id ON public.resource_update_tasks(result_id);
CREATE UNIQUE INDEX uniq_resource_update_task_trigger
    ON public.resource_update_tasks(task_type, resource_type, trigger_type, trigger_id);
CREATE UNIQUE INDEX uniq_resource_update_active_auto_apply_result
    ON public.resource_update_tasks(resource_type, review_result_id)
    WHERE task_type = 'auto_apply_review'
      AND status IN ('pending', 'running');

CREATE UNIQUE INDEX uk_conversation_idle_events_event_id ON public.conversation_idle_events(event_id);
CREATE INDEX idx_conversation_idle_events_session_id ON public.conversation_idle_events(session_id);
CREATE INDEX idx_conversation_idle_events_user_id ON public.conversation_idle_events(user_id);
CREATE INDEX idx_conversation_idle_events_due_at ON public.conversation_idle_events(due_at);
CREATE INDEX idx_conversation_idle_events_status ON public.conversation_idle_events(status);

CREATE UNIQUE INDEX uniq_skill_resources_owner_parent_skill_name
    ON public.skill_resources(owner_user_id, skill_name)
    WHERE node_type = 'parent';

CREATE INDEX idx_resource_update_tasks_pending
    ON public.resource_update_tasks(status, next_run_at, created_at)
    WHERE status = 'pending';
CREATE INDEX idx_resource_update_tasks_running_lock
    ON public.resource_update_tasks(status, locked_until)
    WHERE status = 'running';
CREATE INDEX idx_resource_update_tasks_user_created
    ON public.resource_update_tasks(user_id, created_at DESC);
CREATE INDEX idx_skill_review_scheduler_state_scan
    ON public.skill_review_scheduler_state(locked_until, next_run_at, last_quantity_check_at);
CREATE INDEX idx_conversation_idle_events_due
    ON public.conversation_idle_events(status, due_at)
    WHERE status = 'waiting';
CREATE INDEX idx_conversation_idle_events_session_waiting
    ON public.conversation_idle_events(session_id, status, due_at DESC);
CREATE INDEX idx_chat_histories_conversation_create_time
    ON public.chat_histories(conversation_id, create_time);
CREATE INDEX idx_conversations_user_not_deleted
    ON public.conversations(create_user_id, id)
    WHERE deleted_at IS NULL;
