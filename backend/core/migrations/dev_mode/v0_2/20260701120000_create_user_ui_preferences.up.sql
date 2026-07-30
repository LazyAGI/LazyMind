-- 20260701120000_create_user_ui_preferences
-- +migrate Up
-- +migrate Dialect postgres

CREATE TABLE IF NOT EXISTS public.user_ui_preferences (
    user_id VARCHAR(255) NOT NULL,
    chat_preference_notice_dismissed BOOLEAN NOT NULL DEFAULT FALSE,
    developer_mode_active BOOLEAN NOT NULL DEFAULT FALSE,
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
    CONSTRAINT user_ui_preferences_pkey PRIMARY KEY (user_id)
);

-- +migrate Dialect sqlite
SELECT 1; -- Historical SQLite change is included by the first v0.2 dev migration.
