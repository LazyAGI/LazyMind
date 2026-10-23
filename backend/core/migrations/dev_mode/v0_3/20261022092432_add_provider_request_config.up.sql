-- +migrate Dialect postgres,sqlite
-- Keep the currently effective protocol and endpoint unchanged. Protocol
-- overrides are only created by an explicit request-configuration save.
ALTER TABLE user_model_providers ADD COLUMN request_config TEXT NOT NULL DEFAULT '';
