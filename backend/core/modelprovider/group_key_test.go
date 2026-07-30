package modelprovider

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"github.com/gorilla/mux"
	"gorm.io/gorm"

	"lazymind/core/common/orm"
	"lazymind/core/store"
)

func TestAddKeyReadsAndWritesEncryptedCredentials(t *testing.T) {
	tests := []struct {
		name              string
		newKey            string
		wantStatus        int
		wantKeys          string
		wantUpstreamCalls int32
	}{
		{
			name:              "rejects duplicate encrypted key",
			newKey:            "key-one",
			wantStatus:        http.StatusConflict,
			wantKeys:          "key-one\nkey-two",
			wantUpstreamCalls: 0,
		},
		{
			name:              "appends new key as ciphertext",
			newKey:            "key-three",
			wantStatus:        http.StatusOK,
			wantKeys:          "key-one\nkey-two\nkey-three",
			wantUpstreamCalls: 1,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			db, parent, group := setupEncryptedGroupKeyTest(t, "key-one\nkey-two")

			var upstreamCalls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				upstreamCalls.Add(1)
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"success":true,"message":"accepted"}`))
			}))
			defer server.Close()
			t.Setenv("LAZYMIND_CHAT_SERVICE_URL", server.URL)

			body, err := json.Marshal(addKeyRequest{APIKey: tc.newKey})
			if err != nil {
				t.Fatalf("marshal request: %v", err)
			}
			req := httptest.NewRequest(http.MethodPost, "/keys", strings.NewReader(string(body)))
			req.Header.Set("X-User-Id", "user-1")
			req = mux.SetURLVars(req, map[string]string{
				"model_provider_id": parent.ID,
				"group_id":          group.ID,
			})
			rec := httptest.NewRecorder()

			AddKey(rec, req)

			if rec.Code != tc.wantStatus {
				t.Fatalf("status = %d, want %d: %s", rec.Code, tc.wantStatus, rec.Body.String())
			}
			if got := upstreamCalls.Load(); got != tc.wantUpstreamCalls {
				t.Fatalf("upstream calls = %d, want %d", got, tc.wantUpstreamCalls)
			}
			assertStoredEncryptedAPIKeys(t, db, group.ID, tc.wantKeys, true)
		})
	}
}

func TestRemoveKeyReadsAndWritesEncryptedCredentials(t *testing.T) {
	tests := []struct {
		name         string
		storedKeys   string
		removeKey    string
		wantKeys     string
		wantVerified bool
	}{
		{
			name:         "removes key from encrypted list",
			storedKeys:   "key-one\nkey-two",
			removeKey:    "key-one",
			wantKeys:     "key-two",
			wantVerified: true,
		},
		{
			name:         "clears ciphertext after removing last key",
			storedKeys:   "key-one",
			removeKey:    "key-one",
			wantKeys:     "",
			wantVerified: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			db, parent, group := setupEncryptedGroupKeyTest(t, tc.storedKeys)
			body, err := json.Marshal(removeKeyRequest{APIKey: tc.removeKey})
			if err != nil {
				t.Fatalf("marshal request: %v", err)
			}
			req := httptest.NewRequest(http.MethodDelete, "/keys", strings.NewReader(string(body)))
			req.Header.Set("X-User-Id", "user-1")
			req = mux.SetURLVars(req, map[string]string{
				"model_provider_id": parent.ID,
				"group_id":          group.ID,
			})
			rec := httptest.NewRecorder()

			RemoveKey(rec, req)

			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
			}
			assertStoredEncryptedAPIKeys(t, db, group.ID, tc.wantKeys, tc.wantVerified)
		})
	}
}

func setupEncryptedGroupKeyTest(t *testing.T, apiKeys string) (*gorm.DB, orm.UserModelProvider, orm.UserModelProviderGroup) {
	t.Helper()
	t.Setenv("LAZYMIND_MODEL_PROVIDER_SECRET_KEY", "group-key-test-secret")

	dbName := "group_key_" + strings.NewReplacer("/", "_", " ", "_").Replace(t.Name())
	db, err := gorm.Open(sqlite.Open("file:"+dbName+"?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(&orm.UserModelProvider{}, &orm.UserModelProviderGroup{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	now := time.Now()
	parent := orm.UserModelProvider{
		ID:           "provider-1",
		Name:         "Qwen",
		BaseURL:      "https://dashscope.aliyuncs.com/",
		Category:     defaultProviderCategory,
		Capabilities: "multi_group,custom_base_url,has_models",
		BaseModel: orm.BaseModel{
			CreateUserID:   "user-1",
			CreateUserName: "User 1",
			CreatedAt:      now,
			UpdatedAt:      now,
		},
	}
	ciphertext, err := encryptModelProviderAPIKey(apiKeys)
	if err != nil {
		t.Fatalf("encrypt API keys: %v", err)
	}
	group := orm.UserModelProviderGroup{
		ID:                  "group-1",
		UserModelProviderID: parent.ID,
		Name:                "Qwen",
		BaseURL:             parent.BaseURL,
		APIKeyCiphertext:    ciphertext,
		CredentialVersion:   modelProviderCredentialVersion,
		IsVerified:          true,
		BaseModel: orm.BaseModel{
			CreateUserID:   "user-1",
			CreateUserName: "User 1",
			CreatedAt:      now,
			UpdatedAt:      now,
		},
	}
	if err := db.Create(&parent).Error; err != nil {
		t.Fatalf("create provider: %v", err)
	}
	if err := db.Create(&group).Error; err != nil {
		t.Fatalf("create group: %v", err)
	}
	store.Init(db, db, nil)
	return db, parent, group
}

func assertStoredEncryptedAPIKeys(t *testing.T, db *gorm.DB, groupID, wantKeys string, wantVerified bool) {
	t.Helper()
	var stored orm.UserModelProviderGroup
	if err := db.Take(&stored, "id = ?", groupID).Error; err != nil {
		t.Fatalf("reload group: %v", err)
	}
	if stored.APIKey != "" {
		t.Fatalf("plaintext api_key was not cleared: %q", stored.APIKey)
	}
	if stored.CredentialVersion != modelProviderCredentialVersion {
		t.Fatalf("credential version = %d, want %d", stored.CredentialVersion, modelProviderCredentialVersion)
	}
	if stored.IsVerified != wantVerified {
		t.Fatalf("is_verified = %v, want %v", stored.IsVerified, wantVerified)
	}
	gotKeys, err := ResolveAPIKey(stored.APIKey, stored.APIKeyCiphertext)
	if err != nil {
		t.Fatalf("decrypt stored API keys: %v", err)
	}
	if gotKeys != wantKeys {
		t.Fatalf("stored API keys = %q, want %q", gotKeys, wantKeys)
	}
	if wantKeys != "" && strings.Contains(stored.APIKeyCiphertext, wantKeys) {
		t.Fatalf("ciphertext contains plaintext API keys: %q", stored.APIKeyCiphertext)
	}
}
