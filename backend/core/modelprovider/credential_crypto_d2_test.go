package modelprovider

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"

	"lazymind/core/common/orm"
)

func TestModelProviderRuntimeCredentialCryptoDoesNotReadEnvironmentOrDefaultSecret(t *testing.T) {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate model-provider D2 test")
	}
	payload, err := os.ReadFile(filepath.Join(filepath.Dir(file), "credential_crypto.go"))
	if err != nil {
		t.Fatal(err)
	}
	source := strings.ToLower(string(payload))
	for _, forbidden := range []string{
		"lazymind_model_provider_secret_key",
		"lazymind-core-model-provider-default-secret",
		"os.getenv",
	} {
		if strings.Contains(source, forbidden) {
			t.Errorf("runtime provider credential crypto still contains forbidden key source %q", forbidden)
		}
	}
}

func TestLegacyPlaintextMigrationFailsClosedWithoutOSProtectedRootKey(t *testing.T) {
	t.Setenv("LAZYMIND_MODEL_PROVIDER_SECRET_KEY", "")
	restore := SetCredentialKeyManager(nil)
	t.Cleanup(restore)
	db, err := gorm.Open(sqlite.Open("file:d2-fail-closed?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&orm.UserModelProviderGroup{}); err != nil {
		t.Fatal(err)
	}
	row := orm.UserModelProviderGroup{
		ID: "d2-legacy-group", UserModelProviderID: "d2-provider", Name: "default", BaseURL: "https://example.test",
		APIKey: "d2-legacy-plaintext-canary",
	}
	if err := db.Create(&row).Error; err != nil {
		t.Fatal(err)
	}
	if err := MigrateLegacyAPIKeys(db); err == nil {
		t.Fatal("legacy plaintext migration succeeded without an OS-protected local root key")
	}
	var stored orm.UserModelProviderGroup
	if err := db.Take(&stored, "id = ?", row.ID).Error; err != nil {
		t.Fatal(err)
	}
	if stored.APIKey != row.APIKey || stored.APIKeyCiphertext != "" || stored.CredentialVersion != 0 {
		t.Fatalf("failed secure-store migration changed the local credential row: %#v", stored)
	}
}
