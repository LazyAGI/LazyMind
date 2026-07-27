ALTER TABLE user_selected_models ADD COLUMN share BOOLEAN NOT NULL DEFAULT FALSE;

-- Only one share=true row per model_key (model_type column) globally.
-- This allows llm and evo_llm to share the same underlying model_id while each
-- having their own independent share flag.
CREATE UNIQUE INDEX uk_user_selected_models_shared_model
  ON user_selected_models (model_type)
  WHERE share = TRUE;
