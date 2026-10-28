-- +migrate Dialect postgres,sqlite
ALTER TABLE user_model_provider_group_models DROP COLUMN enabled;
