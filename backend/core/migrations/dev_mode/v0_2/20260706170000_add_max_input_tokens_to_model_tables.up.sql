-- +migrate Dialect postgres
ALTER TABLE default_models ADD COLUMN IF NOT EXISTS max_input_tokens BIGINT;
ALTER TABLE user_model_provider_group_models ADD COLUMN IF NOT EXISTS max_input_tokens BIGINT;

-- +migrate Dialect sqlite
SELECT 1; -- Historical SQLite change is included by the first v0.2 dev migration.
