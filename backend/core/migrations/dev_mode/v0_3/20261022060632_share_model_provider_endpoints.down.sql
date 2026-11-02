-- +migrate Dialect postgres,sqlite
UPDATE user_model_providers SET base_url = (
    SELECT b.base_url FROM model_provider_endpoint_backups b WHERE b.row_kind = 'provider' AND b.row_id = user_model_providers.id
) WHERE id IN (SELECT row_id FROM model_provider_endpoint_backups WHERE row_kind = 'provider');
UPDATE user_model_provider_groups
SET base_url = (SELECT b.base_url FROM model_provider_endpoint_backups b WHERE b.row_kind = 'connection' AND b.row_id = user_model_provider_groups.id),
    is_verified = (SELECT CASE WHEN b.credential_revision = user_model_provider_groups.credential_revision THEN b.is_verified ELSE FALSE END FROM model_provider_endpoint_backups b WHERE b.row_kind = 'connection' AND b.row_id = user_model_provider_groups.id)
WHERE id IN (SELECT row_id FROM model_provider_endpoint_backups WHERE row_kind = 'connection');
-- Re-upload restored URLs when cloud backup is enabled.
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
WHERE a.backup_enabled = TRUE
ON CONFLICT (cloud_issuer, cloud_account_id, local_provider_group_id) DO UPDATE SET
    local_credential_revision = excluded.local_credential_revision,
    operation = 'upsert', backup_state = 'pending', attempt_count = 0,
    next_attempt_at = CURRENT_TIMESTAMP, last_error_code = 0, updated_at = CURRENT_TIMESTAMP;
DROP TABLE model_provider_endpoint_backups;
