DROP INDEX IF EXISTS uk_user_selected_models_shared_model;

ALTER TABLE user_selected_models DROP COLUMN IF EXISTS share;
