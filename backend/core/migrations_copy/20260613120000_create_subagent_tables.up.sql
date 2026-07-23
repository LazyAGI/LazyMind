-- 20260613120000_create_subagent_tables
-- +migrate Up

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
    input_artifact_keys json DEFAULT '[]'::json NOT NULL,
    output_artifact_keys json DEFAULT '[]'::json NOT NULL,
    create_user_id character varying(255) DEFAULT ''::character varying NOT NULL,
    created_at timestamp with time zone NOT NULL,
    updated_at timestamp with time zone NOT NULL,
    CONSTRAINT sub_agent_tasks_pkey PRIMARY KEY (id),
    CONSTRAINT chk_sub_agent_tasks_mode CHECK ((mode)::text IN ('auto', 'manual')),
    CONSTRAINT chk_sub_agent_tasks_status CHECK ((status)::text IN ('pending', 'running', 'succeeded', 'failed', 'interrupted', 'canceled'))
);

CREATE INDEX idx_sat_trigger ON public.sub_agent_tasks(trigger_history_id);
CREATE INDEX idx_sat_status ON public.sub_agent_tasks(status, last_heartbeat);
CREATE UNIQUE INDEX uq_sat_conv_seq ON public.sub_agent_tasks(conversation_id, seq_in_conversation);

CREATE TABLE public.sub_agent_steps (
    id character varying(36) NOT NULL,
    task_id character varying(36) NOT NULL,
    seq integer NOT NULL,
    role character varying(16) NOT NULL,
    content json NOT NULL,
    created_at timestamp with time zone NOT NULL,
    CONSTRAINT sub_agent_steps_pkey PRIMARY KEY (id)
);

CREATE INDEX idx_sas_task ON public.sub_agent_steps(task_id, seq);

CREATE TABLE public.sub_agent_artifacts (
    id character varying(36) NOT NULL,
    task_id character varying(36) NOT NULL,
    artifact_key character varying(64) NOT NULL,
    content_type character varying(32) NOT NULL,
    value json NOT NULL,
    seq integer DEFAULT 1 NOT NULL,
    created_at timestamp with time zone NOT NULL,
    CONSTRAINT sub_agent_artifacts_pkey PRIMARY KEY (id)
);

CREATE INDEX idx_saa_task_key ON public.sub_agent_artifacts(task_id, artifact_key, seq);
