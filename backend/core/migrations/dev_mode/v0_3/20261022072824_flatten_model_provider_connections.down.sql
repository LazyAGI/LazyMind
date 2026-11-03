-- Restore only migration-owned associations. New custom suppliers remain as
-- independent legacy providers; their technical name is restored for old code,
-- with their display name retained on the private credential/model store.
-- +migrate Dialect postgres,sqlite
UPDATE user_model_provider_groups
SET user_model_provider_id = (SELECT b.provider_id FROM model_provider_custom_group_backups b WHERE b.group_id = user_model_provider_groups.id),
    name = (SELECT CASE WHEN user_model_provider_groups.name = b.migrated_name THEN b.name ELSE user_model_provider_groups.name END FROM model_provider_custom_group_backups b WHERE b.group_id = user_model_provider_groups.id),
    base_url = (SELECT CASE WHEN RTRIM(user_model_provider_groups.base_url, '/') = RTRIM(b.migrated_base_url, '/') THEN b.base_url ELSE user_model_provider_groups.base_url END FROM model_provider_custom_group_backups b WHERE b.group_id = user_model_provider_groups.id),
    is_verified = (SELECT CASE WHEN user_model_provider_groups.credential_revision = b.credential_revision AND RTRIM(user_model_provider_groups.base_url, '/') = RTRIM(b.migrated_base_url, '/') THEN b.is_verified ELSE FALSE END FROM model_provider_custom_group_backups b WHERE b.group_id = user_model_provider_groups.id)
WHERE EXISTS (SELECT 1 FROM model_provider_custom_group_backups b
    WHERE b.group_id = user_model_provider_groups.id
      AND (user_model_provider_groups.user_model_provider_id = b.custom_provider_id OR user_model_provider_groups.user_model_provider_id = b.provider_id));

UPDATE user_model_provider_group_models
SET user_model_provider_id = (SELECT g.user_model_provider_id FROM user_model_provider_groups g WHERE g.id = user_model_provider_group_models.user_model_provider_group_id)
WHERE user_model_provider_group_id IN (SELECT group_id FROM model_provider_custom_group_backups)
  AND EXISTS (SELECT 1 FROM user_model_provider_groups g WHERE g.id = user_model_provider_group_models.user_model_provider_group_id AND g.create_user_id = user_model_provider_group_models.create_user_id);

-- Do not revive a stale pre-migration choice after credentials or custom choice
-- changed. IDs and pools survive, allowing manual selection after rollback.
UPDATE user_model_providers
SET name = (SELECT CASE WHEN user_model_providers.name = b.migrated_name THEN b.name ELSE user_model_providers.name END FROM model_provider_custom_parent_backups b WHERE b.provider_id = user_model_providers.id),
    base_url = (SELECT CASE WHEN RTRIM(user_model_providers.base_url, '/') = RTRIM(b.migrated_base_url, '/') THEN b.base_url ELSE user_model_providers.base_url END FROM model_provider_custom_parent_backups b WHERE b.provider_id = user_model_providers.id),
    active_key_group_id = (SELECT CASE WHEN user_model_providers.active_key_revision = b.migrated_key_revision AND NOT EXISTS (
        SELECT 1 FROM model_provider_custom_group_backups gb
        LEFT JOIN user_model_provider_groups g ON g.id = gb.group_id
        LEFT JOIN user_model_providers cp ON cp.id = gb.custom_provider_id
        WHERE gb.provider_id = b.provider_id AND (g.credential_revision <> gb.credential_revision OR COALESCE(cp.active_key_revision, 0) <> 0)
    ) THEN b.active_key_group_id ELSE '' END FROM model_provider_custom_parent_backups b WHERE b.provider_id = user_model_providers.id),
    active_key_id = (SELECT CASE WHEN user_model_providers.active_key_revision = b.migrated_key_revision AND NOT EXISTS (
        SELECT 1 FROM model_provider_custom_group_backups gb
        LEFT JOIN user_model_provider_groups g ON g.id = gb.group_id
        LEFT JOIN user_model_providers cp ON cp.id = gb.custom_provider_id
        WHERE gb.provider_id = b.provider_id AND (g.credential_revision <> gb.credential_revision OR COALESCE(cp.active_key_revision, 0) <> 0)
    ) THEN b.active_key_id ELSE '' END FROM model_provider_custom_parent_backups b WHERE b.provider_id = user_model_providers.id),
    active_key_initialized = (SELECT CASE WHEN user_model_providers.active_key_revision = b.migrated_key_revision AND NOT EXISTS (
        SELECT 1 FROM model_provider_custom_group_backups gb
        LEFT JOIN user_model_provider_groups g ON g.id = gb.group_id
        LEFT JOIN user_model_providers cp ON cp.id = gb.custom_provider_id
        WHERE gb.provider_id = b.provider_id AND (g.credential_revision <> gb.credential_revision OR COALESCE(cp.active_key_revision, 0) <> 0)
    ) THEN b.active_key_initialized ELSE TRUE END FROM model_provider_custom_parent_backups b WHERE b.provider_id = user_model_providers.id),
    active_key_revision = (SELECT CASE WHEN user_model_providers.active_key_revision = b.migrated_key_revision THEN b.active_key_revision ELSE user_model_providers.active_key_revision END FROM model_provider_custom_parent_backups b WHERE b.provider_id = user_model_providers.id)
WHERE id IN (SELECT provider_id FROM model_provider_custom_parent_backups);

UPDATE user_model_provider_groups SET is_verified = FALSE
WHERE user_model_provider_id IN (SELECT p.id FROM user_model_providers p JOIN model_provider_custom_parent_backups b ON b.provider_id = p.id WHERE p.active_key_initialized = TRUE AND p.active_key_id = '' AND b.active_key_id <> '');

INSERT INTO credential_backup_outbox
(id, cloud_issuer, cloud_account_id, local_provider_group_id, local_credential_revision,
 operation, backup_state, attempt_count, next_attempt_at, last_error_code, created_at, updated_at)
SELECT b.id, b.cloud_issuer, b.cloud_account_id, g.id,
       CASE WHEN g.credential_revision < 1 THEN 1 ELSE g.credential_revision END,
       'upsert', 'pending', 0, CURRENT_TIMESTAMP, 0, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP
FROM cloud_credential_bindings b
JOIN cloud_credential_vault_accounts a ON a.cloud_issuer = b.cloud_issuer AND a.cloud_account_id = b.cloud_account_id
JOIN user_model_provider_groups g ON g.id = b.local_provider_group_id
JOIN model_provider_custom_group_backups old ON old.group_id = g.id
WHERE a.backup_enabled = TRUE AND old.custom_provider_id <> ''
ON CONFLICT (cloud_issuer, cloud_account_id, local_provider_group_id) DO UPDATE SET
    local_credential_revision = excluded.local_credential_revision,
    operation = 'upsert', backup_state = 'pending', attempt_count = 0,
    next_attempt_at = CURRENT_TIMESTAMP, last_error_code = 0, updated_at = CURRENT_TIMESTAMP;

DELETE FROM user_model_providers
WHERE id IN (SELECT custom_provider_id FROM model_provider_custom_group_backups WHERE custom_provider_id <> '')
  AND NOT EXISTS (SELECT 1 FROM user_model_provider_groups g WHERE g.user_model_provider_id = user_model_providers.id)
  AND NOT EXISTS (SELECT 1 FROM user_model_provider_group_models m WHERE m.user_model_provider_id = user_model_providers.id);

-- Synthetic source IDs allow post-up custom suppliers to survive the old
-- one-catalog-copy unique index. Group display names and all runtime data remain.
UPDATE user_model_providers SET default_model_provider_id = id,
    name = CASE WHEN protocol_name <> '' THEN protocol_name ELSE name END
WHERE is_custom = TRUE;
DROP TABLE model_provider_custom_group_backups;
DROP TABLE model_provider_custom_parent_backups;
DROP INDEX IF EXISTS uk_user_model_providers_user_default_provider;
ALTER TABLE user_model_providers DROP COLUMN protocol_name;
ALTER TABLE user_model_providers DROP COLUMN is_custom;
-- +migrate Dialect postgres
CREATE UNIQUE INDEX uk_user_model_providers_user_default_provider ON user_model_providers (create_user_id, default_model_provider_id);
