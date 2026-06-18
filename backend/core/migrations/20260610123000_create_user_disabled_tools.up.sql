-- 20260610123000_create_user_disabled_tools
-- +migrate Up

CREATE TABLE IF NOT EXISTS public.user_disabled_tools (
    id bigserial NOT NULL,
    tool_name character varying(255) NOT NULL,
    create_user_id character varying(255) NOT NULL,
    create_user_name character varying(255) DEFAULT ''::character varying NOT NULL,
    created_at timestamp with time zone NOT NULL,
    updated_at timestamp with time zone NOT NULL,
    deleted_at timestamp with time zone,
    CONSTRAINT user_disabled_tools_pkey PRIMARY KEY (id)
);

CREATE UNIQUE INDEX IF NOT EXISTS uk_user_disabled_tools_user_tool
    ON public.user_disabled_tools USING btree (create_user_id, tool_name);
