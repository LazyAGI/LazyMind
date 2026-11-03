-- +migrate Dialect postgres,sqlite
-- The effective protocol/base_url remain usable by the previous application;
-- removing inactive overrides must not roll back later user endpoint edits.
ALTER TABLE user_model_providers DROP COLUMN request_config;
