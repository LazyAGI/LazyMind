-- +migrate Down

DROP INDEX IF EXISTS uk_user_selected_models_shared_model;

-- Restore the old (incorrect) index on user_model_provider_group_model_id.
-- Note: this may fail if multiple share=true rows with the same
-- user_model_provider_group_model_id exist after the Up migration ran.
CREATE UNIQUE INDEX uk_user_selected_models_shared_model
  ON user_selected_models (user_model_provider_group_model_id)
  WHERE share = TRUE;
