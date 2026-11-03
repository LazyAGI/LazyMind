-- +migrate Dialect postgres,sqlite
-- Only a supplier's custom logo is stored here. Model identities and runtime
-- metadata remain unchanged; an empty value preserves automatic branding.
ALTER TABLE user_model_providers ADD COLUMN logo_data_url TEXT NOT NULL DEFAULT '';
