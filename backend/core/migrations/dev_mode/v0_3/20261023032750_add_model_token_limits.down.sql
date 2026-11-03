-- +migrate Dialect postgres,sqlite
ALTER TABLE user_model_provider_group_models DROP COLUMN max_output_tokens;
ALTER TABLE user_model_provider_group_models DROP COLUMN input_token_limit;
