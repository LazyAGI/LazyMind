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
		storedKeys        string
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
		{
			name:              "appends beyond the former whole-group limit",
			storedKeys:        strings.Repeat("a", 400),
			newKey:            strings.Repeat("b", 400),
			wantStatus:        http.StatusOK,
			wantKeys:          strings.Repeat("a", 400) + "\n" + strings.Repeat("b", 400),
			wantUpstreamCalls: 1,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			storedKeys := tc.storedKeys
			if storedKeys == "" {
				storedKeys = "key-one\nkey-two"
			}
			db, parent, group := setupEncryptedGroupKeyTest(t, storedKeys)

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

func TestGroupKeyMetadataSupportsDeletionWithoutExposingSecrets(t *testing.T) {
	db, parent, group := setupEncryptedGroupKeyTest(t, "fixture-secret-one\nfixture-secret-two")
	request := httptest.NewRequest(http.MethodGet, "/groups", nil)
	request.Header.Set("X-User-Id", "user-1")
	request = mux.SetURLVars(request, map[string]string{"model_provider_id": parent.ID})
	recorder := httptest.NewRecorder()
	ListGroups(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("list status=%d: %s", recorder.Code, recorder.Body.String())
	}
	var response struct {
		Data struct {
			Groups []struct {
				HasAPIKey bool `json:"has_api_key"`
				Keys      []struct {
					ID     string `json:"id"`
					Masked string `json:"masked"`
				} `json:"keys"`
			} `json:"groups"`
		} `json:"data"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if len(response.Data.Groups) != 1 || !response.Data.Groups[0].HasAPIKey || len(response.Data.Groups[0].Keys) != 2 {
		t.Fatalf("missing credential metadata: %s", recorder.Body.String())
	}
	if strings.Contains(recorder.Body.String(), "fixture-secret") || strings.Contains(recorder.Body.String(), `"api_key":`) {
		t.Fatal("group listing exposed a secret")
	}
	keys := response.Data.Groups[0].Keys
	if keys[0].ID == "" || keys[0].ID == keys[1].ID || keys[0].Masked == "" {
		t.Fatal("keys need distinct identifiers and masked labels")
	}
	for _, userID := range []string{"other-user", "user-1"} {
		body, _ := json.Marshal(map[string]string{"key_id": keys[0].ID})
		request := httptest.NewRequest(http.MethodDelete, "/keys", strings.NewReader(string(body)))
		request.Header.Set("X-User-Id", userID)
		request = mux.SetURLVars(request, map[string]string{"model_provider_id": parent.ID, "group_id": group.ID})
		recorder := httptest.NewRecorder()
		RemoveKey(recorder, request)
		if userID == "other-user" {
			if recorder.Code != http.StatusNotFound {
				t.Fatalf("cross-owner deletion status=%d", recorder.Code)
			}
			assertStoredEncryptedAPIKeys(t, db, group.ID, "fixture-secret-one\nfixture-secret-two", true)
		} else {
			if recorder.Code != http.StatusOK {
				t.Fatalf("delete status=%d: %s", recorder.Code, recorder.Body.String())
			}
			assertStoredEncryptedAPIKeys(t, db, group.ID, "fixture-secret-two", true)
		}
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
	ciphertext, err := encryptModelProviderAPIKeyForGroup("user-1", "group-1", 1, apiKeys)
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
		CredentialRevision:  1,
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
