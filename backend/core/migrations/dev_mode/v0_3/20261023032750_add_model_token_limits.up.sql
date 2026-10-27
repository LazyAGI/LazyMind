-- +migrate Dialect postgres,sqlite
-- Directional limits are optional; existing context-window values stay unchanged.
ALTER TABLE user_model_provider_group_models ADD COLUMN input_token_limit VARCHAR(16);
ALTER TABLE user_model_provider_group_models ADD COLUMN max_output_tokens VARCHAR(16);
