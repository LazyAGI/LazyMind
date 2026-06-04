-- +migrate Up

-- The previous index was incorrectly created on user_model_provider_group_model_id
-- instead of model_type. This prevented two different model_keys (e.g. llm and
-- evo_llm) from sharing the same underlying model because the constraint fired on
-- the shared model_id. Replace it with the correct per-model_key constraint.

DROP INDEX IF EXISTS uk_user_selected_models_shared_model;

-- Ensure at most one share=true row exists per model_key before creating the
-- corrected index. Keep only the most recently updated row per model_type.
DELETE FROM user_selected_models
WHERE id IN (
    SELECT id FROM (
        SELECT id,
               ROW_NUMBER() OVER (
                   PARTITION BY model_type
                   ORDER BY updated_at DESC, id DESC
               ) AS rn
        FROM user_selected_models
        WHERE share = TRUE
    ) ranked
    WHERE rn > 1
);

-- Only one share=true row per model_type (model_key) globally.
CREATE UNIQUE INDEX uk_user_selected_models_shared_model
  ON user_selected_models (model_type)
  WHERE share = TRUE;
