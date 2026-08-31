-- 20260826190000_add_free_auto_select_metadata
-- +migrate Up
-- +migrate Dialect postgres

ALTER TABLE public.default_models
    ADD COLUMN IF NOT EXISTS free_auto_select_priority INTEGER NOT NULL DEFAULT 0;
ALTER TABLE public.default_models
    ADD COLUMN IF NOT EXISTS free_auto_select_base_urls TEXT NOT NULL DEFAULT '';
ALTER TABLE public.user_model_provider_group_models
    ADD COLUMN IF NOT EXISTS free_auto_select_priority INTEGER NOT NULL DEFAULT 0;
ALTER TABLE public.user_model_provider_group_models
    ADD COLUMN IF NOT EXISTS free_auto_select_base_urls TEXT NOT NULL DEFAULT '';

-- +migrate Dialect sqlite

ALTER TABLE default_models
    ADD COLUMN free_auto_select_priority INTEGER NOT NULL DEFAULT 0;
ALTER TABLE default_models
    ADD COLUMN free_auto_select_base_urls TEXT NOT NULL DEFAULT '';
ALTER TABLE user_model_provider_group_models
    ADD COLUMN free_auto_select_priority INTEGER NOT NULL DEFAULT 0;
ALTER TABLE user_model_provider_group_models
    ADD COLUMN free_auto_select_base_urls TEXT NOT NULL DEFAULT '';
