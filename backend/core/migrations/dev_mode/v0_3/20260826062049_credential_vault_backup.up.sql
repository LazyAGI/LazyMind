-- +migrate Dialect postgres
ALTER TABLE user_model_provider_groups
    ADD COLUMN credential_revision BIGINT NOT NULL DEFAULT 0 CHECK (credential_revision >= 0);

CREATE TABLE IF NOT EXISTS cloud_credential_vault_accounts (
    id VARCHAR(36) PRIMARY KEY,
    cloud_issuer VARCHAR(512) NOT NULL,
    cloud_account_id VARCHAR(255) NOT NULL,
    vault_id VARCHAR(36) NOT NULL,
    vault_member_id VARCHAR(36) NOT NULL,
    client_member_key VARCHAR(128) NOT NULL,
    signing_key_version INTEGER NOT NULL CHECK (signing_key_version > 0),
    key_shard_id INTEGER NOT NULL CHECK (key_shard_id BETWEEN 0 AND 63),
    active_key_id VARCHAR(128) NOT NULL,
    backup_enabled BOOLEAN NOT NULL DEFAULT FALSE,
    vault_etag VARCHAR(64) NOT NULL DEFAULT '',
    record_count BIGINT NOT NULL DEFAULT 0 CHECK (record_count >= 0),
    last_backup_at TIMESTAMP,
    last_succeeded_at TIMESTAMP,
    created_at TIMESTAMP NOT NULL,
    updated_at TIMESTAMP NOT NULL,
    CONSTRAINT uk_credential_vault_account UNIQUE (cloud_issuer, cloud_account_id)
);

CREATE TABLE IF NOT EXISTS cloud_credential_bindings (
    id VARCHAR(36) PRIMARY KEY,
    cloud_issuer VARCHAR(512) NOT NULL,
    cloud_account_id VARCHAR(255) NOT NULL,
    vault_id VARCHAR(36) NOT NULL,
    cloud_record_id VARCHAR(36) NOT NULL,
    local_provider_group_id VARCHAR(64) NOT NULL,
    last_cloud_revision BIGINT NOT NULL DEFAULT 0 CHECK (last_cloud_revision >= 0),
    last_local_credential_revision BIGINT NOT NULL DEFAULT 0 CHECK (last_local_credential_revision >= 0),
    last_etag VARCHAR(64) NOT NULL DEFAULT '',
    backup_state VARCHAR(16) NOT NULL CHECK (backup_state IN ('pending','running','failed','conflict','succeeded')),
    created_at TIMESTAMP NOT NULL,
    updated_at TIMESTAMP NOT NULL,
    CONSTRAINT uk_credential_binding_record UNIQUE (cloud_issuer, cloud_account_id, vault_id, cloud_record_id),
    CONSTRAINT uk_credential_binding_local UNIQUE (cloud_issuer, cloud_account_id, local_provider_group_id)
);

CREATE TABLE IF NOT EXISTS credential_backup_outbox (
    id VARCHAR(36) PRIMARY KEY,
    cloud_issuer VARCHAR(512) NOT NULL,
    cloud_account_id VARCHAR(255) NOT NULL,
    local_provider_group_id VARCHAR(64) NOT NULL,
    local_credential_revision BIGINT NOT NULL CHECK (local_credential_revision > 0),
    operation VARCHAR(16) NOT NULL CHECK (operation IN ('upsert','delete')),
    backup_state VARCHAR(16) NOT NULL CHECK (backup_state IN ('pending','running','failed','conflict')),
    attempt_count INTEGER NOT NULL DEFAULT 0 CHECK (attempt_count >= 0),
    next_attempt_at TIMESTAMP NOT NULL,
    last_error_code INTEGER NOT NULL DEFAULT 0,
    created_at TIMESTAMP NOT NULL,
    updated_at TIMESTAMP NOT NULL,
    CONSTRAINT uk_credential_outbox_local UNIQUE (cloud_issuer, cloud_account_id, local_provider_group_id)
);

CREATE INDEX IF NOT EXISTS idx_credential_backup_outbox_due
    ON credential_backup_outbox (backup_state, next_attempt_at, updated_at);

-- +migrate Dialect sqlite
ALTER TABLE user_model_provider_groups
    ADD COLUMN credential_revision INTEGER NOT NULL DEFAULT 0 CHECK (credential_revision >= 0);

CREATE TABLE IF NOT EXISTS cloud_credential_vault_accounts (
    id VARCHAR(36) PRIMARY KEY,
    cloud_issuer VARCHAR(512) NOT NULL,
    cloud_account_id VARCHAR(255) NOT NULL,
    vault_id VARCHAR(36) NOT NULL,
    vault_member_id VARCHAR(36) NOT NULL,
    client_member_key VARCHAR(128) NOT NULL,
    signing_key_version INTEGER NOT NULL CHECK (signing_key_version > 0),
    key_shard_id INTEGER NOT NULL CHECK (key_shard_id BETWEEN 0 AND 63),
    active_key_id VARCHAR(128) NOT NULL,
    backup_enabled BOOLEAN NOT NULL DEFAULT FALSE,
    vault_etag VARCHAR(64) NOT NULL DEFAULT '',
    record_count INTEGER NOT NULL DEFAULT 0 CHECK (record_count >= 0),
    last_backup_at DATETIME,
    last_succeeded_at DATETIME,
    created_at DATETIME NOT NULL,
    updated_at DATETIME NOT NULL,
    UNIQUE (cloud_issuer, cloud_account_id)
);

CREATE TABLE IF NOT EXISTS cloud_credential_bindings (
    id VARCHAR(36) PRIMARY KEY,
    cloud_issuer VARCHAR(512) NOT NULL,
    cloud_account_id VARCHAR(255) NOT NULL,
    vault_id VARCHAR(36) NOT NULL,
    cloud_record_id VARCHAR(36) NOT NULL,
    local_provider_group_id VARCHAR(64) NOT NULL,
    last_cloud_revision INTEGER NOT NULL DEFAULT 0 CHECK (last_cloud_revision >= 0),
    last_local_credential_revision INTEGER NOT NULL DEFAULT 0 CHECK (last_local_credential_revision >= 0),
    last_etag VARCHAR(64) NOT NULL DEFAULT '',
    backup_state VARCHAR(16) NOT NULL CHECK (backup_state IN ('pending','running','failed','conflict','succeeded')),
    created_at DATETIME NOT NULL,
    updated_at DATETIME NOT NULL,
    UNIQUE (cloud_issuer, cloud_account_id, vault_id, cloud_record_id),
    UNIQUE (cloud_issuer, cloud_account_id, local_provider_group_id)
);

CREATE TABLE IF NOT EXISTS credential_backup_outbox (
    id VARCHAR(36) PRIMARY KEY,
    cloud_issuer VARCHAR(512) NOT NULL,
    cloud_account_id VARCHAR(255) NOT NULL,
    local_provider_group_id VARCHAR(64) NOT NULL,
    local_credential_revision INTEGER NOT NULL CHECK (local_credential_revision > 0),
    operation VARCHAR(16) NOT NULL CHECK (operation IN ('upsert','delete')),
    backup_state VARCHAR(16) NOT NULL CHECK (backup_state IN ('pending','running','failed','conflict')),
    attempt_count INTEGER NOT NULL DEFAULT 0 CHECK (attempt_count >= 0),
    next_attempt_at DATETIME NOT NULL,
    last_error_code INTEGER NOT NULL DEFAULT 0,
    created_at DATETIME NOT NULL,
    updated_at DATETIME NOT NULL,
    UNIQUE (cloud_issuer, cloud_account_id, local_provider_group_id)
);

CREATE INDEX IF NOT EXISTS idx_credential_backup_outbox_due
    ON credential_backup_outbox (backup_state, next_attempt_at, updated_at);
