-- 20260626100000_create_user_chat_settings
-- +migrate Up

CREATE TABLE IF NOT EXISTS public.user_chat_settings (
    user_id      VARCHAR(255) NOT NULL,
    enable_plugin   BOOLEAN      NOT NULL DEFAULT TRUE,
    plugin_mode     VARCHAR(16)  NOT NULL DEFAULT 'dynamic',
    enable_subagent BOOLEAN      NOT NULL DEFAULT TRUE,
    updated_at      TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    CONSTRAINT user_chat_settings_pkey PRIMARY KEY (user_id)
);
