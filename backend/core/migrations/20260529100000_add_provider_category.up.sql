-- Add category and capabilities columns to default_model_providers and user_model_providers.
-- Create user_selected_providers table for OCR/search group selection (symmetric to user_selected_models).

ALTER TABLE default_model_providers
  ADD COLUMN category     VARCHAR(64)  NOT NULL DEFAULT 'model',
  ADD COLUMN capabilities VARCHAR(512) NOT NULL DEFAULT 'multi_group,custom_base_url,has_models';

ALTER TABLE user_model_providers
  ADD COLUMN category     VARCHAR(64)  NOT NULL DEFAULT 'model',
  ADD COLUMN capabilities VARCHAR(512) NOT NULL DEFAULT 'multi_group,custom_base_url,has_models';

CREATE TABLE user_selected_providers (
  id                            BIGINT       NOT NULL AUTO_INCREMENT,
  user_id                       VARCHAR(255) NOT NULL,
  user_name                     VARCHAR(255) NOT NULL DEFAULT '',
  category                      VARCHAR(64)  NOT NULL,
  user_model_provider_group_id  VARCHAR(64)  NOT NULL,
  share                         BOOLEAN      NOT NULL DEFAULT FALSE,
  created_at                    TIMESTAMP    NOT NULL,
  updated_at                    TIMESTAMP    NOT NULL,
  PRIMARY KEY (id),
  UNIQUE KEY uk_user_selected_providers_user_category (user_id, category)
);
