-- +migrate Dialect postgres
ALTER TABLE user_model_provider_groups DROP COLUMN credential_version;
ALTER TABLE user_model_provider_groups DROP COLUMN api_key_ciphertext;

-- +migrate Dialect sqlite
SELECT 1; -- Historical SQLite change is included by the first v0.2 dev migration.
