-- Saved pools are retained when reverting the selection preference.
-- +migrate Dialect postgres,sqlite
ALTER TABLE user_model_providers DROP COLUMN active_key_revision;
ALTER TABLE user_model_providers DROP COLUMN active_key_initialized;
ALTER TABLE user_model_providers DROP COLUMN active_key_id;
ALTER TABLE user_model_providers DROP COLUMN active_key_group_id;
