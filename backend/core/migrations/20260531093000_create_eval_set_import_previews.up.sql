-- 20260531093000_create_eval_set_import_previews
-- +migrate Up

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
    CONSTRAINT eval_set_import_previews_pkey PRIMARY KEY (token),
    CONSTRAINT chk_eval_set_import_previews_status CHECK ((status)::text IN ('ready', 'consumed', 'expired'))
);

CREATE INDEX idx_eval_set_import_previews_status ON public.eval_set_import_previews(status);
CREATE INDEX idx_eval_set_import_previews_expires_at ON public.eval_set_import_previews(expires_at);
CREATE INDEX idx_eval_set_import_previews_user ON public.eval_set_import_previews(create_user_id);
