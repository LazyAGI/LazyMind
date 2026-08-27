-- 20260826190000_add_free_auto_select_metadata
-- +migrate Down
-- +migrate Dialect postgres

ALTER TABLE public.user_model_provider_group_models
    DROP COLUMN IF EXISTS free_auto_select_base_urls;
ALTER TABLE public.user_model_provider_group_models
    DROP COLUMN IF EXISTS free_auto_select_priority;
ALTER TABLE public.default_models
    DROP COLUMN IF EXISTS free_auto_select_base_urls;
ALTER TABLE public.default_models
    DROP COLUMN IF EXISTS free_auto_select_priority;

-- +migrate Dialect sqlite

ALTER TABLE user_model_provider_group_models
    DROP COLUMN free_auto_select_base_urls;
ALTER TABLE user_model_provider_group_models
    DROP COLUMN free_auto_select_priority;
ALTER TABLE default_models
    DROP COLUMN free_auto_select_base_urls;
ALTER TABLE default_models
    DROP COLUMN free_auto_select_priority;
