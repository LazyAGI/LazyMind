-- Supplier-wide selection references opaque key IDs; saved credential pools
-- and ciphertext are preserved. Go initializes legacy pools after decryption.
-- +migrate Dialect postgres,sqlite
ALTER TABLE user_model_providers ADD COLUMN active_key_group_id VARCHAR(64) NOT NULL DEFAULT '';
ALTER TABLE user_model_providers ADD COLUMN active_key_id VARCHAR(64) NOT NULL DEFAULT '';
ALTER TABLE user_model_providers ADD COLUMN active_key_initialized BOOLEAN NOT NULL DEFAULT FALSE;
ALTER TABLE user_model_providers ADD COLUMN active_key_revision BIGINT NOT NULL DEFAULT 0;
