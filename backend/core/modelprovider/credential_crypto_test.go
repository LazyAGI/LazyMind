package modelprovider

import (
	"strings"
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"

	"lazymind/core/common/orm"
)

func TestAPIKeyForGroupMigratesLegacyPlaintext(t *testing.T) {
	t.Setenv("LAZYMIND_MODEL_PROVIDER_SECRET_KEY", "device-derived-test-key")
	db, err := gorm.Open(sqlite.Open("file:credential-migration?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&orm.UserModelProviderGroup{}); err != nil {
		t.Fatal(err)
	}
	row := orm.UserModelProviderGroup{
		ID: "group-1", UserModelProviderID: "provider-1", Name: "default", BaseURL: "https://example.test",
		APIKey: "secret-api-key",
	}
	if err := db.Create(&row).Error; err != nil {
		t.Fatal(err)
	}

	got, err := apiKeyForGroup(db, &row)
	if err != nil {
		t.Fatal(err)
	}
	if got != "secret-api-key" {
		t.Fatalf("api key = %q", got)
	}
	var stored orm.UserModelProviderGroup
	if err := db.Take(&stored, "id = ?", row.ID).Error; err != nil {
		t.Fatal(err)
	}
	if stored.APIKey != "" || stored.CredentialVersion != modelProviderCredentialVersion {
		t.Fatalf("legacy plaintext was not cleared: %#v", stored)
	}
	if !strings.Contains(stored.APIKeyCiphertext, `"enc":"aes-gcm"`) || strings.Contains(stored.APIKeyCiphertext, got) {
		t.Fatalf("credential was not encrypted: %q", stored.APIKeyCiphertext)
	}
	decrypted, err := ResolveAPIKey(stored.APIKey, stored.APIKeyCiphertext)
	if err != nil || decrypted != got {
		t.Fatalf("ResolveAPIKey() = %q, %v", decrypted, err)
	}
}
