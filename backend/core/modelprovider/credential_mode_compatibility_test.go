package modelprovider

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"lazymind/core/common/orm"
	"lazymind/core/common/secretcrypto"
)

func TestLegacyDefaultModelCredentialsSurviveDeviceKeyEnable(t *testing.T) {
	db, _, group := setupEncryptedGroupKeyTest(t, "fixture-original-key")
	ciphertext, err := secretcrypto.EncodeAESGCM([]byte("fixture-legacy-key"), legacyModelProviderDefaultSecret)
	if err != nil {
		t.Fatal(err)
	}
	group.APIKey, group.APIKeyCiphertext, group.CredentialVersion = "", string(ciphertext), 1
	if err := db.Save(&group).Error; err != nil {
		t.Fatal(err)
	}
	t.Setenv("LAZYMIND_MODEL_PROVIDER_SECRET_KEY", "fixture-device-derived-key")
	for i := 0; i < 2; i++ {
		key, err := apiKeyForGroup(db, &group)
		if err != nil || key != "fixture-legacy-key" {
			t.Fatalf("old local credential cannot be used after switching modes: %v", err)
		}
	}
	var stored orm.UserModelProviderGroup
	if err := db.Take(&stored, "id = ?", group.ID).Error; err != nil {
		t.Fatal(err)
	}
	if stored.APIKey != "" || stored.APIKeyCiphertext != string(ciphertext) || stored.CredentialVersion != 1 {
		t.Fatal("reading historical credentials rewrote their persisted representation")
	}
}

func TestSelectedProvidersPreserveUnreadableMetadataWithoutFailingHealthySelections(t *testing.T) {
	db := setupSelectedProviderTestDB(t)
	seedProviderGroup(t, db, "user-1", "healthy-provider", "healthy-group", "Search", "search")
	seedProviderGroup(t, db, "user-1", "unreadable-provider", "unreadable-group", "OCR", "ocr")
	seedProviderGroup(t, db, "other-user", "foreign-provider", "foreign-group", "Foreign", "translation")
	const unreadable = `{"enc":"aes-gcm","nonce":"invalid","v":"invalid"}`
	if err := db.Model(&orm.UserModelProviderGroup{}).Where("id = ?", "unreadable-group").Updates(map[string]any{
		"api_key": "", "api_key_ciphertext": unreadable, "credential_version": 1,
	}).Error; err != nil {
		t.Fatal(err)
	}
	for category, groupID := range map[string]string{"search": "healthy-group", "ocr": "unreadable-group", "translation": "foreign-group"} {
		if err := db.Create(&orm.UserSelectedProvider{
			UserID: "user-1", Category: category, UserModelProviderGroupID: groupID,
			CreatedAt: time.Now(), UpdatedAt: time.Now(),
		}).Error; err != nil {
			t.Fatal(err)
		}
	}
	req := httptest.NewRequest(http.MethodGet, "/api/core/model_providers/selected_providers", nil)
	req.Header.Set("X-User-Id", "user-1")
	rec := httptest.NewRecorder()
	GetSelectedProviders(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("one unreadable credential failed the entire default configuration: HTTP %d", rec.Code)
	}
	var response struct {
		Data selectedProvidersResponse `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if len(response.Data.Selections) != 2 {
		t.Fatalf("must preserve both owned selections, got %d", len(response.Data.Selections))
	}
	for _, selection := range response.Data.Selections {
		if selection.GroupID != "healthy-group" && selection.GroupID != "unreadable-group" {
			t.Fatal("another user's group was exposed")
		}
	}
	if strings.Contains(rec.Body.String(), "secret") || strings.Contains(rec.Body.String(), "aes-gcm") {
		t.Fatal("selected metadata exposed credential material")
	}
	var stored orm.UserModelProviderGroup
	if err := db.Take(&stored, "id = ?", "unreadable-group").Error; err != nil || stored.APIKeyCiphertext != unreadable || !stored.IsVerified {
		t.Fatal("reading metadata changed the saved credential or verification state")
	}
}
