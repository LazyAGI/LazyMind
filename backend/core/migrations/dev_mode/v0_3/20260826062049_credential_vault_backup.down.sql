-- +migrate Dialect postgres
DROP INDEX IF EXISTS idx_credential_backup_outbox_due;
DROP TABLE IF EXISTS credential_backup_outbox;
DROP TABLE IF EXISTS cloud_credential_bindings;
DROP TABLE IF EXISTS cloud_credential_vault_accounts;
ALTER TABLE user_model_provider_groups DROP COLUMN IF EXISTS credential_revision;

-- +migrate Dialect sqlite
DROP INDEX IF EXISTS idx_credential_backup_outbox_due;
DROP TABLE IF EXISTS credential_backup_outbox;
DROP TABLE IF EXISTS cloud_credential_bindings;
DROP TABLE IF EXISTS cloud_credential_vault_accounts;
ALTER TABLE user_model_provider_groups DROP COLUMN credential_revision;
