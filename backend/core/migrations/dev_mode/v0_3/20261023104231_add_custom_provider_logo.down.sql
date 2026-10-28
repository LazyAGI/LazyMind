-- +migrate Dialect postgres,sqlite
-- Remove only the supplier logo override, preserving supplier/group/model IDs,
-- credentials, runtime parameters and selected-model references.
ALTER TABLE user_model_providers DROP COLUMN logo_data_url;
