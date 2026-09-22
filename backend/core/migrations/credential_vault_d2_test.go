package migrations

import (
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
)

const d2CredentialVaultMigration = "20260826062049_credential_vault_backup"

func d2MigrationsRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate D2 migration test")
	}
	return filepath.Dir(file)
}

func readD2CredentialVaultMigration(t *testing.T, mode, direction string) string {
	t.Helper()
	name := d2CredentialVaultMigration
	if mode == "version_mode" {
		name = "20260805000000_workflow_runtime_release"
	}
	path := filepath.Join(d2MigrationsRoot(t), mode, "v0_3", name+"."+direction+".sql")
	payload, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read D2 Credential Vault %s migration %s: %v", mode, path, err)
	}
	return regexp.MustCompile(`\s+`).ReplaceAllString(strings.ToLower(string(payload)), " ")
}

func TestD2CredentialVaultMigrationCreatesAccountBindingAndReferenceOnlyOutbox(t *testing.T) {
	sql := readD2CredentialVaultMigration(t, "dev_mode", "up")
	for _, dialect := range []string{"+migrate dialect postgres", "+migrate dialect sqlite"} {
		if !strings.Contains(sql, dialect) {
			t.Errorf("D2 migration omitted %s", dialect)
		}
	}
	for _, table := range []string{"cloud_credential_vault_accounts", "cloud_credential_bindings", "credential_backup_outbox"} {
		if !regexp.MustCompile(`create table( if not exists)? ` + table + `\b`).MatchString(sql) {
			t.Errorf("D2 migration omitted table %s", table)
		}
	}
	for _, marker := range []string{
		"cloud_issuer", "cloud_account_id", "vault_id", "vault_member_id", "client_member_key", "signing_key_version",
		"backup_enabled", "key_shard_id", "active_key_id", "cloud_record_id", "local_provider_group_id",
		"last_cloud_revision", "last_local_credential_revision", "last_etag", "backup_state",
		"operation", "attempt_count", "next_attempt_at", "last_error_code", "credential_revision",
	} {
		if !strings.Contains(sql, marker) {
			t.Errorf("D2 migration omitted %q", marker)
		}
	}
	for _, invariant := range []string{
		"unique (cloud_issuer, cloud_account_id)",
		"unique (cloud_issuer, cloud_account_id, vault_id, cloud_record_id)",
		"unique (cloud_issuer, cloud_account_id, local_provider_group_id)",
		"operation in ('upsert','delete')",
		"backup_state in ('pending','running','failed','conflict')",
		"key_shard_id between 0 and 63",
		"attempt_count >= 0",
		"credential_revision >= 0",
	} {
		if !strings.Contains(sql, invariant) {
			t.Errorf("D2 migration omitted invariant %q", invariant)
		}
	}
	for _, forbidden := range []string{
		"api_key", "plaintext", "ciphertext", "wrapped_dek", "private_key", "access_token", "refresh_token", "password", "payload json",
	} {
		if strings.Contains(sql, forbidden) {
			t.Errorf("D2 local metadata/outbox migration contains forbidden secret material field %q", forbidden)
		}
	}
}

func TestD2CredentialVaultMigrationIsConsolidatedIntoV03Aggregate(t *testing.T) {
	dev := readD2CredentialVaultMigration(t, "dev_mode", "up")
	aggregate := readD2CredentialVaultMigration(t, "version_mode", "up")
	for _, marker := range []string{
		"cloud_resource_bindings", "cloud_credential_vault_accounts", "cloud_credential_bindings", "credential_backup_outbox", "credential_revision",
	} {
		if !strings.Contains(dev, marker) && marker != "cloud_resource_bindings" {
			t.Fatalf("D2 dev migration fixture omitted %q", marker)
		}
		if !strings.Contains(aggregate, marker) {
			t.Errorf("v0_3 aggregate omitted final-state marker %q", marker)
		}
	}
}

func TestD2CredentialVaultMigrationHasPortableDevelopmentRollback(t *testing.T) {
	sql := readD2CredentialVaultMigration(t, "dev_mode", "down")
	for _, dialect := range []string{"+migrate dialect postgres", "+migrate dialect sqlite"} {
		if !strings.Contains(sql, dialect) {
			t.Errorf("D2 rollback omitted %s", dialect)
		}
	}
	for _, marker := range []string{
		"drop table", "credential_backup_outbox", "cloud_credential_bindings", "cloud_credential_vault_accounts", "credential_revision",
	} {
		if !strings.Contains(sql, marker) {
			t.Errorf("D2 rollback omitted %q", marker)
		}
	}
}
