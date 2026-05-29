DROP TABLE IF EXISTS user_selected_providers;

ALTER TABLE user_model_providers
  DROP COLUMN capabilities,
  DROP COLUMN category;

ALTER TABLE default_model_providers
  DROP COLUMN capabilities,
  DROP COLUMN category;
