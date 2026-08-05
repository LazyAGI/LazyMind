-- +migrate Dialect postgres
ALTER TABLE default_models DROP COLUMN base_url;
ALTER TABLE user_model_provider_group_models DROP COLUMN base_url;

-- +migrate Dialect sqlite
SELECT 1; -- Historical SQLite change is included by the first v0.2 dev migration.
