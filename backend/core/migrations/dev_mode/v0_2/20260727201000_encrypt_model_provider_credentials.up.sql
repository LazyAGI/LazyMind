ALTER TABLE user_model_provider_groups
    ADD COLUMN api_key_ciphertext text NOT NULL DEFAULT '';

ALTER TABLE user_model_provider_groups
    ADD COLUMN credential_version integer NOT NULL DEFAULT 0;
