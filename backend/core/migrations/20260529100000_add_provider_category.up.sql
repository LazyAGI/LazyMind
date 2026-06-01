-- Add category and capabilities columns to default_model_providers and user_model_providers.
-- Create user_selected_providers table for OCR/search group selection (symmetric to user_selected_models).

ALTER TABLE default_model_providers
  ADD COLUMN category     VARCHAR(64)  NOT NULL DEFAULT 'model',
  ADD COLUMN capabilities VARCHAR(512) NOT NULL DEFAULT 'multi_group,custom_base_url,has_models';

ALTER TABLE user_model_providers
  ADD COLUMN category     VARCHAR(64)  NOT NULL DEFAULT 'model',
  ADD COLUMN capabilities VARCHAR(512) NOT NULL DEFAULT 'multi_group,custom_base_url,has_models';

CREATE SEQUENCE public.user_selected_providers_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;

CREATE TABLE public.user_selected_providers (
  id                            BIGINT       NOT NULL DEFAULT nextval('public.user_selected_providers_id_seq'),
  user_id                       VARCHAR(255) NOT NULL,
  user_name                     VARCHAR(255) NOT NULL DEFAULT '',
  category                      VARCHAR(64)  NOT NULL,
  user_model_provider_group_id  VARCHAR(64)  NOT NULL,
  share                         BOOLEAN      NOT NULL DEFAULT FALSE,
  created_at                    TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
  updated_at                    TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
  PRIMARY KEY (id),
  CONSTRAINT uk_user_selected_providers_user_category UNIQUE (user_id, category)
);

ALTER SEQUENCE public.user_selected_providers_id_seq OWNED BY public.user_selected_providers.id;
