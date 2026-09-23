package modelprovider

import (
	"fmt"
	"strings"
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"

	"lazymind/core/common/orm"
	"lazymind/core/common/secretcrypto"
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
		APIKey: "secret-api-key", BaseModel: orm.BaseModel{CreateUserID: "user-1"},
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
	if stored.APIKey != "" || stored.CredentialVersion != legacyModelProviderCredentialVersion {
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

func TestLegacyMultiKeyMigrationPreservesLongCredentials(t *testing.T) {
	for _, size := range []int{512, 513, 542, 20 * 1024} {
		t.Run(fmt.Sprint(size), func(t *testing.T) {
			keys := strings.Repeat("k", size)
			if size == 542 {
				keys = strings.Repeat("a", 180) + "\n" + strings.Repeat("b", 180) + "\n" + strings.Repeat("c", 180)
			}
			legacy, err := secretcrypto.EncodeAESGCM([]byte(keys), legacyModelProviderEncryptionKey())
			if err != nil {
				t.Fatal(err)
			}
			db, err := gorm.Open(sqlite.Open("file:long-credential-"+fmt.Sprint(size)+"?mode=memory&cache=shared"), &gorm.Config{})
			if err != nil {
				t.Fatal(err)
			}
			if err := db.AutoMigrate(&orm.UserModelProviderGroup{}); err != nil {
				t.Fatal(err)
			}
			row := orm.UserModelProviderGroup{ID: "long-keys", APIKeyCiphertext: string(legacy), CredentialVersion: 1, IsVerified: true, BaseModel: orm.BaseModel{CreateUserID: "local-owner"}}
			if err := db.Create(&row).Error; err != nil {
				t.Fatal(err)
			}
			if err := MigrateLegacyAPIKeys(db); err != nil {
				t.Fatalf("migrate %d-byte local credential: %v", size, err)
			}
			var stored orm.UserModelProviderGroup
			if err := db.First(&stored, "id = ?", row.ID).Error; err != nil {
				t.Fatal(err)
			}
			if stored.APIKeyCiphertext != string(legacy) || stored.CredentialVersion != 1 || stored.CredentialRevision != 1 {
				t.Fatal("startup changed the legacy ciphertext")
			}
			if got, err := ResolveAPIKey(stored.APIKey, stored.APIKeyCiphertext); err != nil || got != keys {
				t.Fatal("legacy multi-key data changed")
			}
			if err := MigrateLegacyAPIKeys(db); err != nil {
				t.Fatal(err)
			}
		})
	}
}
