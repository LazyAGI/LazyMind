-- A supplier owns one private credential/model store. Legacy customized or
-- additional stores become independent custom suppliers without changing any
-- group/model IDs or encrypted credential AAD. Keep metadata for safe rollback.
-- +migrate Dialect postgres,sqlite
ALTER TABLE user_model_providers ADD COLUMN is_custom BOOLEAN NOT NULL DEFAULT FALSE;
ALTER TABLE user_model_providers ADD COLUMN protocol_name VARCHAR(255) NOT NULL DEFAULT '';
DROP INDEX IF EXISTS uk_user_model_providers_user_default_provider;
CREATE UNIQUE INDEX uk_user_model_providers_user_default_provider
ON user_model_providers (create_user_id, default_model_provider_id) WHERE is_custom = FALSE;

CREATE TABLE model_provider_custom_group_backups (
    group_id VARCHAR(64) PRIMARY KEY,
    provider_id VARCHAR(64) NOT NULL,
    custom_provider_id VARCHAR(64) NOT NULL,
    name VARCHAR(255) NOT NULL,
    base_url VARCHAR(1024) NOT NULL,
    is_verified BOOLEAN NOT NULL,
    credential_revision BIGINT NOT NULL,
    migrated_name VARCHAR(255) NOT NULL,
    migrated_base_url VARCHAR(1024) NOT NULL
);
WITH source_rows AS (
    SELECT g.id AS group_id, g.user_model_provider_id AS provider_id,
           g.name, g.base_url, g.is_verified, g.credential_revision, g.created_at,
           p.name AS provider_name, d.name AS catalog_name, d.base_url AS catalog_url,
           CASE WHEN old.base_url IS NOT NULL AND RTRIM(p.base_url, '/') = RTRIM((
               SELECT first_old.base_url
               FROM user_model_provider_groups first_group
               JOIN model_provider_endpoint_backups first_old ON first_old.row_kind = 'connection' AND first_old.row_id = first_group.id
               WHERE first_group.user_model_provider_id = p.id AND first_group.create_user_id = p.create_user_id
               ORDER BY first_group.created_at ASC, first_group.id ASC LIMIT 1
           ), '/') THEN old.base_url ELSE g.base_url END AS effective_url
    FROM user_model_provider_groups g
    JOIN user_model_providers p ON p.id = g.user_model_provider_id AND p.create_user_id = g.create_user_id
    JOIN default_model_providers d ON d.id = p.default_model_provider_id
    LEFT JOIN model_provider_endpoint_backups old ON old.row_kind = 'connection' AND old.row_id = g.id
    WHERE p.deleted_at IS NULL AND g.deleted_at IS NULL AND LOWER(TRIM(p.category)) = 'model'
), classified AS (
    SELECT source_rows.*,
           CASE WHEN TRIM(provider_name) = TRIM(catalog_name) AND TRIM(name) = TRIM(catalog_name)
                     AND RTRIM(effective_url, '/') = RTRIM(catalog_url, '/') THEN 1 ELSE 0 END AS standard
    FROM source_rows
), numbered AS (
    SELECT classified.*,
           SUM(standard) OVER (PARTITION BY provider_id ORDER BY created_at ASC, group_id ASC ROWS UNBOUNDED PRECEDING) AS standard_position,
           ROW_NUMBER() OVER (ORDER BY group_id ASC) AS migration_position
    FROM classified
)
INSERT INTO model_provider_custom_group_backups
(group_id, provider_id, custom_provider_id, name, base_url, is_verified, credential_revision, migrated_name, migrated_base_url)
SELECT group_id, provider_id,
       CASE WHEN standard = 1 AND standard_position = 1 THEN ''
            ELSE 'mpc20261022072824-' || CAST(migration_position AS VARCHAR(20)) || '-' || SUBSTR(group_id, 1, 24) END,
       name, base_url, is_verified, credential_revision,
       CASE WHEN standard = 1 AND standard_position = 1 THEN name
            WHEN TRIM(provider_name) <> TRIM(catalog_name) AND (TRIM(name) = TRIM(provider_name) OR TRIM(name) = TRIM(catalog_name) OR TRIM(name) = '') THEN provider_name
            WHEN TRIM(name) = '' THEN catalog_name || ' (' || SUBSTR(group_id, 1, 8) || ')'
            WHEN TRIM(name) = TRIM(catalog_name) OR TRIM(name) = TRIM(provider_name)
                 THEN SUBSTR(name, 1, 242) || ' (' || SUBSTR(group_id, 1, 8) || ')'
            ELSE name END,
       effective_url
FROM numbered;

CREATE TABLE model_provider_custom_parent_backups (
    provider_id VARCHAR(64) PRIMARY KEY,
    name VARCHAR(255) NOT NULL,
    migrated_name VARCHAR(255) NOT NULL,
    base_url VARCHAR(1024) NOT NULL,
    active_key_group_id VARCHAR(64) NOT NULL,
    active_key_id VARCHAR(64) NOT NULL,
    active_key_initialized BOOLEAN NOT NULL,
    active_key_revision BIGINT NOT NULL,
    migrated_base_url VARCHAR(1024) NOT NULL,
    migrated_key_revision BIGINT NOT NULL
);
INSERT INTO model_provider_custom_parent_backups
(provider_id, name, migrated_name, base_url, active_key_group_id, active_key_id, active_key_initialized, active_key_revision, migrated_base_url, migrated_key_revision)
SELECT p.id, p.name, d.name, p.base_url, p.active_key_group_id, p.active_key_id, p.active_key_initialized, p.active_key_revision,
       CASE WHEN EXISTS (SELECT 1 FROM model_provider_custom_group_backups b WHERE b.provider_id = p.id AND b.custom_provider_id <> '') THEN d.base_url ELSE p.base_url END,
       p.active_key_revision + CASE WHEN EXISTS (SELECT 1 FROM model_provider_custom_group_backups b WHERE b.provider_id = p.id AND b.custom_provider_id <> '') THEN 1 ELSE 0 END
FROM user_model_providers p JOIN default_model_providers d ON d.id = p.default_model_provider_id
WHERE p.deleted_at IS NULL AND LOWER(TRIM(p.category)) = 'model';

INSERT INTO user_model_providers
(id, default_model_provider_id, name, description, base_url, active_key_group_id, active_key_id,
 active_key_initialized, active_key_revision, category, capabilities, create_user_id, create_user_name,
 created_at, updated_at, deleted_at, is_custom, protocol_name)
SELECT b.custom_provider_id, p.default_model_provider_id, b.migrated_name, p.description, b.migrated_base_url,
       CASE WHEN p.active_key_group_id = g.id AND RTRIM(b.migrated_base_url, '/') = RTRIM(g.base_url, '/') THEN p.active_key_group_id ELSE '' END,
       CASE WHEN p.active_key_group_id = g.id AND RTRIM(b.migrated_base_url, '/') = RTRIM(g.base_url, '/') THEN p.active_key_id ELSE '' END,
       CASE WHEN p.active_key_initialized = TRUE AND p.active_key_id = '' THEN TRUE
            WHEN p.active_key_group_id = g.id AND RTRIM(b.migrated_base_url, '/') = RTRIM(g.base_url, '/') THEN p.active_key_initialized ELSE FALSE END,
       0, p.category, REPLACE(REPLACE(REPLACE(p.capabilities, 'multi_group,', ''), ',multi_group', ''), 'multi_group', ''),
       p.create_user_id, p.create_user_name, g.created_at, g.updated_at, NULL, TRUE, d.name
FROM model_provider_custom_group_backups b
JOIN user_model_provider_groups g ON g.id = b.group_id
JOIN user_model_providers p ON p.id = b.provider_id
JOIN default_model_providers d ON d.id = p.default_model_provider_id
WHERE b.custom_provider_id <> '';

UPDATE user_model_provider_groups
SET user_model_provider_id = (SELECT CASE WHEN b.custom_provider_id <> '' THEN b.custom_provider_id ELSE b.provider_id END FROM model_provider_custom_group_backups b WHERE b.group_id = user_model_provider_groups.id),
    name = (SELECT b.migrated_name FROM model_provider_custom_group_backups b WHERE b.group_id = user_model_provider_groups.id),
    base_url = (SELECT b.migrated_base_url FROM model_provider_custom_group_backups b WHERE b.group_id = user_model_provider_groups.id),
    is_verified = (SELECT CASE
        WHEN RTRIM(b.base_url, '/') <> RTRIM(b.migrated_base_url, '/') THEN FALSE
        WHEN b.custom_provider_id = '' THEN CASE WHEN p.active_key_group_id = '' OR p.active_key_group_id = b.group_id THEN b.is_verified ELSE FALSE END
        WHEN p.active_key_group_id = b.group_id AND p.active_key_id <> '' THEN b.is_verified
        WHEN p.active_key_id = '' AND p.active_key_initialized = FALSE AND TRIM(user_model_provider_groups.api_key) = '' AND TRIM(user_model_provider_groups.api_key_ciphertext) = '' THEN b.is_verified
        ELSE FALSE END
        FROM model_provider_custom_group_backups b JOIN user_model_providers p ON p.id = b.provider_id
        WHERE b.group_id = user_model_provider_groups.id)
WHERE id IN (SELECT group_id FROM model_provider_custom_group_backups);

UPDATE user_model_provider_group_models
SET user_model_provider_id = (SELECT g.user_model_provider_id FROM user_model_provider_groups g WHERE g.id = user_model_provider_group_models.user_model_provider_group_id)
WHERE user_model_provider_group_id IN (SELECT group_id FROM model_provider_custom_group_backups)
  AND EXISTS (SELECT 1 FROM user_model_provider_groups g WHERE g.id = user_model_provider_group_models.user_model_provider_group_id AND g.create_user_id = user_model_provider_group_models.create_user_id);

UPDATE user_model_providers
SET name = (SELECT b.migrated_name FROM model_provider_custom_parent_backups b WHERE b.provider_id = user_model_providers.id),
    base_url = (SELECT b.migrated_base_url FROM model_provider_custom_parent_backups b WHERE b.provider_id = user_model_providers.id),
    protocol_name = (SELECT d.name FROM default_model_providers d WHERE d.id = user_model_providers.default_model_provider_id),
    active_key_group_id = CASE WHEN EXISTS (SELECT 1 FROM model_provider_custom_group_backups g WHERE g.provider_id = user_model_providers.id AND g.group_id = user_model_providers.active_key_group_id AND g.custom_provider_id <> '') THEN '' ELSE active_key_group_id END,
    active_key_id = CASE WHEN EXISTS (SELECT 1 FROM model_provider_custom_group_backups g WHERE g.provider_id = user_model_providers.id AND g.group_id = user_model_providers.active_key_group_id AND g.custom_provider_id <> '') THEN '' ELSE active_key_id END,
    active_key_initialized = CASE WHEN active_key_group_id <> '' AND EXISTS (SELECT 1 FROM model_provider_custom_group_backups g WHERE g.provider_id = user_model_providers.id AND g.group_id = user_model_providers.active_key_group_id AND g.custom_provider_id <> '') THEN TRUE ELSE active_key_initialized END,
    active_key_revision = (SELECT b.migrated_key_revision FROM model_provider_custom_parent_backups b WHERE b.provider_id = user_model_providers.id)
WHERE id IN (SELECT provider_id FROM model_provider_custom_parent_backups);

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
