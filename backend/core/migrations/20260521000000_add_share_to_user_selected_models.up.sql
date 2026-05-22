ALTER TABLE user_selected_models ADD COLUMN share BOOLEAN NOT NULL DEFAULT FALSE;

-- Only one share=true row per model_type globally
CREATE UNIQUE INDEX uk_user_selected_models_shared_model
  ON user_selected_models (model_type)
  WHERE share = TRUE;
