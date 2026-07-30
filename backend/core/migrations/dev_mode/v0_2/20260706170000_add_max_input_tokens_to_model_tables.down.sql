-- +migrate Dialect postgres
ALTER TABLE user_model_provider_group_models DROP COLUMN IF EXISTS max_input_tokens;
ALTER TABLE default_models DROP COLUMN IF EXISTS max_input_tokens;

-- +migrate Dialect sqlite
SELECT 1; -- Historical SQLite change is included by the first v0.2 dev migration.
