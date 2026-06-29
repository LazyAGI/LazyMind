-- 20260622120000_create_local_fs_chat_settings
-- +migrate Up

CREATE TABLE IF NOT EXISTS public.local_fs_chat_settings (
    id bigserial NOT NULL,
    create_user_id character varying(255) NOT NULL,
    create_user_name character varying(255) DEFAULT ''::character varying NOT NULL,
    enabled boolean DEFAULT false NOT NULL,
    created_at timestamp with time zone NOT NULL,
    updated_at timestamp with time zone NOT NULL,
    CONSTRAINT local_fs_chat_settings_pkey PRIMARY KEY (id)
);

CREATE UNIQUE INDEX IF NOT EXISTS uk_local_fs_chat_settings_user
    ON public.local_fs_chat_settings USING btree (create_user_id);
