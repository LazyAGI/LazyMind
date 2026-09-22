package chat

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"lazymind/core/common/orm"
	"lazymind/core/common/secretcrypto"
	"lazymind/core/modelprovider"
	"lazymind/core/userenv"
)

func userEnvFixture() orm.UserEnvironmentVariable {
	return orm.UserEnvironmentVariable{ID: "env_crypto", UserID: "user-crypto", Name: "TEST_API_KEY",
		CredentialVersion: userenv.CredentialVersion, CredentialRevision: 1, Enabled: true,
		CreatedAt: time.Now().UTC(), UpdatedAt: time.Now().UTC()}
}

func TestUserEnvCipherBindsRecord(t *testing.T) {
	setupUserEnvTest(t)
	row := userEnvFixture()
	var err error
	row.ValueCiphertext, err = userenv.EncryptValue(row, "private-test-value")
	if err != nil {
		t.Fatal(err)
	}
	for _, mutate := range []func(*orm.UserEnvironmentVariable){
		func(r *orm.UserEnvironmentVariable) { r.UserID = "other-user" },
		func(r *orm.UserEnvironmentVariable) { r.ID = "other-id" },
		func(r *orm.UserEnvironmentVariable) { r.Name = "OTHER_TOKEN" },
		func(r *orm.UserEnvironmentVariable) { r.CredentialRevision++ },
		func(r *orm.UserEnvironmentVariable) { r.CredentialVersion++ },
		func(r *orm.UserEnvironmentVariable) {
			r.ValueCiphertext = `{"version":2,"key_source":"server","nonce":"AA==","ciphertext":"AA=="}`
		},
	} {
		changed := row
		mutate(&changed)
		if _, err := userenv.DecryptValue(changed); err == nil {
			t.Fatal("tampered credential was accepted")
		}
	}
}

func TestUserEnvMissingKeyFailsClosed(t *testing.T) {
	setupUserEnvTest(t)
	t.Setenv("LAZYMIND_USER_ENV_SECRET_KEY", "")
	restore := modelprovider.SetCredentialKeyManager(nil)
	defer restore()
	if _, err := userenv.EncryptValue(userEnvFixture(), "private-test-value"); err == nil {
		t.Fatal("missing secure store must not fall back to a public key")
	}
}

func TestUserEnvPrivateServerKeyFile(t *testing.T) {
	setupUserEnvTest(t)
	t.Setenv("LAZYMIND_USER_ENV_SECRET_KEY", "")
	path := filepath.Join(t.TempDir(), "user-env.key")
	if err := os.WriteFile(path, []byte("test-server-key-file-with-32-bytes-minimum"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("LAZYMIND_USER_ENV_SECRET_KEY_FILE", path)
	row := userEnvFixture()
	var err error
	row.ValueCiphertext, err = userenv.EncryptValue(row, "private-test-value")
	if err != nil {
		t.Fatal(err)
	}
	if value, err := userenv.DecryptValue(row); err != nil || value != "private-test-value" {
		t.Fatalf("key-file round trip failed: %v", err)
	}
	if err := os.Chmod(path, 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := userenv.DecryptValue(row); err == nil {
		t.Fatal("publicly readable key must be rejected")
	}
}

func TestUserEnvLegacyCredentialUpgradePreservesData(t *testing.T) {
	db := setupUserEnvTest(t)
	row := userEnvFixture()
	row.CredentialVersion = 1
	row.Description = "existing note"
	raw, err := secretcrypto.EncodeAESGCM([]byte("legacy-private-value"), "lazymind-core-user-env-default-secret")
	if err != nil {
		t.Fatal(err)
	}
	row.ValueCiphertext = string(raw)
	if err := db.Create(&row).Error; err != nil {
		t.Fatal(err)
	}
	env, err := userenv.LoadEnabled(context.Background(), db.DB, row.UserID)
	if err != nil || env[row.Name] != "legacy-private-value" {
		t.Fatalf("upgrade failed: %v", err)
	}
	var updated orm.UserEnvironmentVariable
	if err := db.First(&updated, "id = ?", row.ID).Error; err != nil {
		t.Fatal(err)
	}
	if updated.CredentialVersion != 2 || updated.CredentialRevision != 2 || updated.Description != row.Description || !updated.Enabled {
		t.Fatal("upgrade lost metadata")
	}
	if updated.ValueCiphertext == string(raw) {
		t.Fatal("legacy ciphertext was not replaced")
	}
	if _, err := userenv.DecryptValue(updated); err != nil {
		t.Fatal(err)
	}
	if _, err := userenv.LoadEnabled(context.Background(), db.DB, row.UserID); err != nil {
		t.Fatal(err)
	}
}

func TestUserEnvLegacyUpgradeWithoutNewKeyPreservesOriginalRow(t *testing.T) {
	db := setupUserEnvTest(t)
	t.Setenv("LAZYMIND_USER_ENV_SECRET_KEY", "")
	restore := modelprovider.SetCredentialKeyManager(nil)
	defer restore()
	row := userEnvFixture()
	row.CredentialVersion = 1
	raw, err := secretcrypto.EncodeAESGCM([]byte("legacy-value"), "lazymind-core-user-env-default-secret")
	if err != nil {
		t.Fatal(err)
	}
	row.ValueCiphertext = string(raw)
	if err := db.Create(&row).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := userenv.LoadEnabled(context.Background(), db.DB, row.UserID); err == nil {
		t.Fatal("missing new key should fail closed")
	}
	copy := row
	if _, err := userenv.UpgradeCredential(db.DB, &copy); err == nil {
		t.Fatal("missing new key should fail upgrade")
	}
	if copy.ValueCiphertext != row.ValueCiphertext || copy.CredentialVersion != row.CredentialVersion || copy.CredentialRevision != row.CredentialRevision {
		t.Fatal("failed upgrade modified the in-memory row")
	}
	var stored orm.UserEnvironmentVariable
	if err := db.First(&stored, "id = ?", row.ID).Error; err != nil {
		t.Fatal(err)
	}
	if stored.ValueCiphertext != row.ValueCiphertext || stored.CredentialVersion != 1 || stored.CredentialRevision != 1 {
		t.Fatal("failed upgrade modified the old row")
	}
}

func TestUserEnvPatchUpgradesLegacyCredential(t *testing.T) {
	db := setupUserEnvTest(t)
	row := userEnvFixture()
	row.CredentialVersion = 1
	raw, err := secretcrypto.EncodeAESGCM([]byte("legacy-value"), "lazymind-core-user-env-default-secret")
	if err != nil {
		t.Fatal(err)
	}
	row.ValueCiphertext = string(raw)
	if err := db.Create(&row).Error; err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	PatchUserEnvironmentVariable(rec, newUserEnvRequest(http.MethodPatch, "/user/env-vars/"+row.ID, `{"description":"updated note"}`, row.UserID, map[string]string{"id": row.ID}))
	if rec.Code != http.StatusOK {
		t.Fatalf("first edit of legacy credential failed: %s", rec.Body.String())
	}
	var stored orm.UserEnvironmentVariable
	if err := db.First(&stored, "id = ?", row.ID).Error; err != nil {
		t.Fatal(err)
	}
	if stored.CredentialVersion != 2 || stored.Description != "updated note" {
		t.Fatal("legacy edit lost metadata")
	}
	if value, err := userenv.DecryptValue(stored); err != nil || value != "legacy-value" {
		t.Fatalf("legacy edit lost value: %v", err)
	}
}

func TestUserEnvRevisionQueryRejectsStaleAndDeletedRows(t *testing.T) {
	db := setupUserEnvTest(t)
	row := userEnvFixture()
	var err error
	row.ValueCiphertext, err = userenv.EncryptValue(row, "old-value")
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&row).Error; err != nil {
		t.Fatal(err)
	}
	updated := row
	updated.CredentialRevision++
	updated.ValueCiphertext, err = userenv.EncryptValue(updated, "new-value")
	if err != nil {
		t.Fatal(err)
	}
	tx := userenv.RevisionQuery(db.DB, row).Updates(map[string]any{"value_ciphertext": updated.ValueCiphertext, "credential_revision": updated.CredentialRevision})
	if tx.Error != nil || tx.RowsAffected != 1 {
		t.Fatalf("first update failed: %v", tx.Error)
	}
	tx = userenv.RevisionQuery(db.DB, row).Updates(map[string]any{"enabled": false})
	if tx.Error != nil || tx.RowsAffected != 0 {
		t.Fatal("stale update overwrote current row")
	}
	if err := db.Delete(&row).Error; err != nil {
		t.Fatal(err)
	}
	tx = userenv.RevisionQuery(db.DB, updated).Updates(map[string]any{"enabled": true})
	if tx.Error != nil || tx.RowsAffected != 0 {
		t.Fatal("deleted row was restored")
	}
}

func TestUserEnvManagementIsolationDisableDeleteAndRecreate(t *testing.T) {
	db := setupUserEnvTest(t)
	create := func() *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		CreateUserEnvironmentVariable(rec, newUserEnvRequest(http.MethodPost, "/user/env-vars", `{"name":"TEST_API_KEY","value":"private-test-value"}`, "owner", nil))
		return rec
	}
	if rec := create(); rec.Code != 200 {
		t.Fatal(rec.Body.String())
	}
	var row orm.UserEnvironmentVariable
	if err := db.First(&row, "user_id = ?", "owner").Error; err != nil {
		t.Fatal(err)
	}
	for _, handler := range []http.HandlerFunc{PatchUserEnvironmentVariable, DeleteUserEnvironmentVariable} {
		rec := httptest.NewRecorder()
		handler(rec, newUserEnvRequest(http.MethodPatch, "/user/env-vars/"+row.ID, `{"enabled":false}`, "other-user", map[string]string{"id": row.ID}))
		if rec.Code != http.StatusNotFound {
			t.Fatalf("cross-user mutation status=%d", rec.Code)
		}
	}
	rec := httptest.NewRecorder()
	PatchUserEnvironmentVariable(rec, newUserEnvRequest(http.MethodPatch, "/user/env-vars/"+row.ID, `{"enabled":false}`, "owner", map[string]string{"id": row.ID}))
	if rec.Code != 200 {
		t.Fatal(rec.Body.String())
	}
	env, err := userenv.LoadEnabled(context.Background(), db.DB, "owner")
	if err != nil || len(env) != 0 {
		t.Fatal("disabled variable still injected")
	}
	rec = httptest.NewRecorder()
	DeleteUserEnvironmentVariable(rec, newUserEnvRequest(http.MethodDelete, "/user/env-vars/"+row.ID, "", "owner", map[string]string{"id": row.ID}))
	if rec.Code != 200 {
		t.Fatal(rec.Body.String())
	}
	if rec := create(); rec.Code != 200 {
		t.Fatalf("recreate after soft deletion: %s", rec.Body.String())
	}
}

func TestUserEnvFailuresDoNotMasqueradeAsMigrations(t *testing.T) {
	rec := httptest.NewRecorder()
	replyUserEnvError(rec, userenv.ErrConflict)
	if rec.Code != 409 || strings.Contains(rec.Body.String(), "migration") {
		t.Fatal(rec.Body.String())
	}
	setupUserEnvTest(t)
	payload, _ := json.Marshal(map[string]string{"name": "TEST_API_KEY", "value": "invalid\x00value"})
	rec = httptest.NewRecorder()
	CreateUserEnvironmentVariable(rec, newUserEnvRequest(http.MethodPost, "/user/env-vars", string(payload), "owner", nil))
	if rec.Code != http.StatusBadRequest {
		t.Fatal("NUL value accepted")
	}
}
