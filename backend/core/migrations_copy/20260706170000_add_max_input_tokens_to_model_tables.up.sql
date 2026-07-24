ALTER TABLE default_models ADD COLUMN IF NOT EXISTS max_input_tokens BIGINT;
ALTER TABLE user_model_provider_group_models ADD COLUMN IF NOT EXISTS max_input_tokens BIGINT;
