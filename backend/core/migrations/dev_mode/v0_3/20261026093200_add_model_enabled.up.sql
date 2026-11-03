-- +migrate Dialect postgres,sqlite
-- Disabled models stay in the supplier and can be enabled again.
ALTER TABLE user_model_provider_group_models ADD COLUMN enabled BOOLEAN NOT NULL DEFAULT TRUE;
