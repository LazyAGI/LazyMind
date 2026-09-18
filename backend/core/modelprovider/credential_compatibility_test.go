package modelprovider

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"github.com/gorilla/mux"
	"gorm.io/gorm"

	"lazymind/core/common/orm"
	"lazymind/core/common/secretcrypto"
	"lazymind/core/store"
)

func TestNeverSignedInCredentialsDoNotRequireCloudSecureStore(t *testing.T) {
	t.Setenv("LAZYMIND_CLOUD_BASE_URL", "")
	t.Setenv("LAZYMIND_MODEL_PROVIDER_SECRET_KEY", "existing-local-key")
	restore := SetCredentialKeyManager(nil)
	t.Cleanup(restore)
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "local.db")), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&orm.UserModelProviderGroup{}); err != nil {
		t.Fatal(err)
	}
	legacy, err := secretcrypto.EncodeAESGCM([]byte("existing-key"), legacyModelProviderEncryptionKey())
	if err != nil {
		t.Fatal(err)
	}
	rows := []orm.UserModelProviderGroup{
		{ID: "plaintext", APIKey: "older-key", BaseModel: orm.BaseModel{CreateUserID: "local-user"}},
		{ID: "encrypted", APIKeyCiphertext: string(legacy), CredentialVersion: 1, BaseModel: orm.BaseModel{CreateUserID: "local-user"}},
	}
	for i := range rows {
		if err := db.Create(&rows[i]).Error; err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 2; i++ {
		if err := MigrateLegacyAPIKeys(db); err != nil {
			t.Fatalf("local upgrade requires unavailable secure store: %v", err)
		}
	}
	for id, want := range map[string]string{"plaintext": "older-key", "encrypted": "existing-key"} {
		var row orm.UserModelProviderGroup
		if err := db.First(&row, "id = ?", id).Error; err != nil {
			t.Fatal(err)
		}
		if row.APIKey != "" || row.CredentialVersion != 1 || row.CredentialRevision != 1 {
			t.Fatal("legacy storage contract was not preserved")
		}
		if id == "encrypted" && row.APIKeyCiphertext != string(legacy) {
			t.Fatal("startup rewrote existing encrypted credentials")
		}
		if got, err := apiKeyForGroup(db, &row); err != nil || got != want {
			t.Fatalf("local key is unavailable: %v", err)
		}
	}
}

func TestPersonalCredentialCreateEditAndRemoveWithoutCloudSecureStore(t *testing.T) {
	t.Setenv("LAZYMIND_CLOUD_BASE_URL", "")
	t.Setenv("LAZYMIND_MODEL_PROVIDER_SECRET_KEY", "existing-local-key")
	restore := SetCredentialKeyManager(nil)
	t.Cleanup(restore)
	db := orm.MigrateAllModelsForTest(t)
	store.Init(db.DB, db.DB, nil)
	t.Cleanup(func() { store.Init(nil, nil, nil) })
	now := time.Now()
	provider := orm.UserModelProvider{ID: "local-provider", Name: "OpenAI", Category: defaultProviderCategory, Capabilities: "multi_group,custom_base_url,has_models", BaseModel: orm.BaseModel{CreateUserID: "local-user", CreatedAt: now, UpdatedAt: now}}
	if err := db.DB.Create(&provider).Error; err != nil {
		t.Fatal(err)
	}
	checker := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"success":true,"message":"accepted"}`))
	}))
	defer checker.Close()
	t.Setenv("LAZYMIND_CHAT_SERVICE_URL", checker.URL)
	call := func(method, groupID, body string, handler http.HandlerFunc) *httptest.ResponseRecorder {
		t.Helper()
		request := httptest.NewRequest(method, "/groups", strings.NewReader(body))
		request.Header.Set("X-User-Id", "local-user")
		request = mux.SetURLVars(request, map[string]string{"model_provider_id": provider.ID, "group_id": groupID})
		recorder := httptest.NewRecorder()
		handler(recorder, request)
		if recorder.Code != http.StatusOK {
			t.Fatalf("local credential operation failed: %d %s", recorder.Code, recorder.Body.String())
		}
		return recorder
	}
	created := call(http.MethodPost, "", `{"name":"Personal","base_url":"https://personal.example/v1","api_key":"key-one"}`, CreateGroup)
	var response struct {
		Data createGroupResponse `json:"data"`
	}
	if err := json.Unmarshal(created.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.Data.ID == "" {
		t.Fatal("group was not created")
	}
	assertKeys := func(want string) {
		t.Helper()
		var row orm.UserModelProviderGroup
		if err := db.DB.First(&row, "id = ?", response.Data.ID).Error; err != nil {
			t.Fatal(err)
		}
		if row.CredentialVersion != 1 || row.APIKey != "" {
			t.Fatal("personal credential format changed")
		}
		if got, err := ResolveAPIKey(row.APIKey, row.APIKeyCiphertext); err != nil || got != want {
			t.Fatalf("personal credentials could not be read: %v", err)
		}
	}
	assertKeys("key-one")
	call(http.MethodPost, response.Data.ID, `{"api_key":"key-two"}`, AddKey)
	assertKeys("key-one\nkey-two")
	call(http.MethodDelete, response.Data.ID, `{"api_key":"key-one"}`, RemoveKey)
	assertKeys("key-two")
	call(http.MethodPatch, response.Data.ID, `{"name":"Personal","base_url":"https://personal.example/v1","api_key":"key-three"}`, UpdateGroup)
	assertKeys("key-three")
	call(http.MethodDelete, response.Data.ID, `{"api_key":"key-three"}`, RemoveKey)
	assertKeys("")
}
