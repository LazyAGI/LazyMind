ALTER TABLE default_models ADD COLUMN base_url varchar(1024) NOT NULL DEFAULT '';
ALTER TABLE user_model_provider_group_models ADD COLUMN base_url varchar(1024) NOT NULL DEFAULT '';
