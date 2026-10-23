-- Promote the oldest active connection endpoint once. Keep rollback metadata
-- without storing API keys; connection/model IDs and ciphertext stay intact.
-- +migrate Dialect postgres,sqlite
CREATE TABLE model_provider_endpoint_backups (
    row_kind VARCHAR(16) NOT NULL,
    row_id VARCHAR(64) NOT NULL,
    base_url VARCHAR(1024) NOT NULL,
    is_verified BOOLEAN NOT NULL DEFAULT FALSE,
    credential_revision BIGINT NOT NULL DEFAULT 0,
    PRIMARY KEY (row_kind, row_id)
);
INSERT INTO model_provider_endpoint_backups (row_kind, row_id, base_url, is_verified, credential_revision)
SELECT 'provider', p.id, p.base_url, FALSE, 0
FROM user_model_providers p
WHERE p.deleted_at IS NULL AND LOWER(TRIM(p.category)) = 'model';
INSERT INTO model_provider_endpoint_backups (row_kind, row_id, base_url, is_verified, credential_revision)
SELECT 'connection', g.id, g.base_url, g.is_verified, g.credential_revision
FROM user_model_provider_groups g
JOIN user_model_providers p ON p.id = g.user_model_provider_id AND p.create_user_id = g.create_user_id
WHERE p.deleted_at IS NULL AND g.deleted_at IS NULL AND LOWER(TRIM(p.category)) = 'model';
UPDATE user_model_providers
SET base_url = COALESCE((
    SELECT g.base_url FROM user_model_provider_groups g
    WHERE g.user_model_provider_id = user_model_providers.id
      AND g.create_user_id = user_model_providers.create_user_id AND g.deleted_at IS NULL
    ORDER BY g.created_at ASC, g.id ASC LIMIT 1
), base_url)
WHERE deleted_at IS NULL AND LOWER(TRIM(category)) = 'model';
UPDATE user_model_provider_groups
SET is_verified = CASE WHEN base_url <> (
        SELECT p.base_url FROM user_model_providers p WHERE p.id = user_model_provider_groups.user_model_provider_id
    ) THEN FALSE ELSE is_verified END,
    base_url = (SELECT p.base_url FROM user_model_providers p WHERE p.id = user_model_provider_groups.user_model_provider_id)
WHERE deleted_at IS NULL AND EXISTS (
    SELECT 1 FROM user_model_providers p
    WHERE p.id = user_model_provider_groups.user_model_provider_id
      AND p.create_user_id = user_model_provider_groups.create_user_id
      AND p.deleted_at IS NULL AND LOWER(TRIM(p.category)) = 'model'
);
-- Keep the local credential revision/ciphertext unchanged: V2 authentication
-- binds to that revision. Cloud revisions are independent; refresh their payload.
INSERT INTO credential_backup_outbox
(id, cloud_issuer, cloud_account_id, local_provider_group_id, local_credential_revision,
 operation, backup_state, attempt_count, next_attempt_at, last_error_code, created_at, updated_at)
SELECT b.id, b.cloud_issuer, b.cloud_account_id, g.id,
       CASE WHEN g.credential_revision < 1 THEN 1 ELSE g.credential_revision END,
       'upsert', 'pending', 0, CURRENT_TIMESTAMP, 0, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP
FROM cloud_credential_bindings b
JOIN cloud_credential_vault_accounts a ON a.cloud_issuer = b.cloud_issuer AND a.cloud_account_id = b.cloud_account_id
JOIN user_model_provider_groups g ON g.id = b.local_provider_group_id
JOIN model_provider_endpoint_backups old ON old.row_kind = 'connection' AND old.row_id = g.id
WHERE a.backup_enabled = TRUE AND old.base_url <> g.base_url
ON CONFLICT (cloud_issuer, cloud_account_id, local_provider_group_id) DO UPDATE SET
    local_credential_revision = excluded.local_credential_revision,
    operation = 'upsert', backup_state = 'pending', attempt_count = 0,
    next_attempt_at = CURRENT_TIMESTAMP, last_error_code = 0, updated_at = CURRENT_TIMESTAMP;
