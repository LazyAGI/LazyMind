ALTER TABLE default_models ADD COLUMN model_settings TEXT NOT NULL DEFAULT '{}';
ALTER TABLE user_model_provider_group_models ADD COLUMN model_settings TEXT NOT NULL DEFAULT '{}';
