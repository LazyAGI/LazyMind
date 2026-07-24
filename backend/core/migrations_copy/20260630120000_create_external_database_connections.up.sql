-- 20260630120000_create_external_database_connections
-- +migrate Up

CREATE TABLE IF NOT EXISTS public.external_database_connections (
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
    deleted_at timestamp with time zone,
    CONSTRAINT external_database_connections_pkey PRIMARY KEY (id)
);

CREATE INDEX IF NOT EXISTS idx_external_database_connections_user
    ON public.external_database_connections USING btree (create_user_id, deleted_at, updated_at);
