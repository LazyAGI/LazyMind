package modelprovider

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"github.com/gorilla/mux"
	"gorm.io/gorm"

	"lazymind/core/common/orm"
	"lazymind/core/store"
)

func TestCheckGroupRequiresRequestAPIKey(t *testing.T) {
	tests := []struct {
		name        string
		requestBody string
		wantStatus  int
		wantAPIKey  string
	}{
		{
			name:        "omitted key is rejected",
			requestBody: `{"provider_name":"Qwen","base_url":"https://dashscope.aliyuncs.com/","dry_run":false}`,
			wantStatus:  http.StatusBadRequest,
		},
		{
			name:        "empty key is rejected",
			requestBody: `{"provider_name":"Qwen","base_url":"https://dashscope.aliyuncs.com/","api_key":"","dry_run":false}`,
			wantStatus:  http.StatusBadRequest,
		},
		{
			name:        "provided key takes precedence",
			requestBody: `{"provider_name":"Qwen","base_url":"https://dashscope.aliyuncs.com/","api_key":"request-key","dry_run":false}`,
			wantStatus:  http.StatusOK,
			wantAPIKey:  "request-key",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("LAZYMIND_MODEL_PROVIDER_SECRET_KEY", "check-group-test-key")

			dbName := "check_group_" + strings.NewReplacer("/", "_", " ", "_").Replace(t.Name())
			db, err := gorm.Open(sqlite.Open("file:"+dbName+"?mode=memory&cache=shared"), &gorm.Config{})
			if err != nil {
				t.Fatalf("open sqlite: %v", err)
			}
			if err := db.AutoMigrate(
				&orm.DefaultModelProvider{},
				&orm.UserModelProvider{},
				&orm.UserModelProviderGroup{},
			); err != nil {
				t.Fatalf("migrate: %v", err)
			}

			now := time.Now()
			defaultProvider := orm.DefaultModelProvider{
				ID:          "default-qwen",
				Name:        "Qwen",
				Description: "Qwen provider",
				BaseURL:     "https://dashscope.aliyuncs.com/",
				Category:    defaultProviderCategory,
				CreatedAt:   now,
				UpdatedAt:   now,
			}
			userProvider := orm.UserModelProvider{
				ID:                     "user-qwen",
				DefaultModelProviderID: defaultProvider.ID,
				Name:                   defaultProvider.Name,
				Description:            defaultProvider.Description,
				BaseURL:                defaultProvider.BaseURL,
				Category:               defaultProvider.Category,
				BaseModel: orm.BaseModel{
					CreateUserID:   "user-1",
					CreateUserName: "User 1",
					CreatedAt:      now,
					UpdatedAt:      now,
				},
			}
			ciphertext, err := encryptModelProviderAPIKey("stored-key")
			if err != nil {
				t.Fatalf("encrypt stored key: %v", err)
			}
			group := orm.UserModelProviderGroup{
				ID:                  "qwen-group",
				UserModelProviderID: userProvider.ID,
				Name:                "Qwen",
				BaseURL:             defaultProvider.BaseURL,
				APIKeyCiphertext:    ciphertext,
				CredentialVersion:   modelProviderCredentialVersion,
				BaseModel: orm.BaseModel{
					CreateUserID:   "user-1",
					CreateUserName: "User 1",
					CreatedAt:      now,
					UpdatedAt:      now,
				},
			}
			if err := db.Create(&defaultProvider).Error; err != nil {
				t.Fatalf("create default provider: %v", err)
			}
			if err := db.Create(&userProvider).Error; err != nil {
				t.Fatalf("create user provider: %v", err)
			}
			if err := db.Create(&group).Error; err != nil {
				t.Fatalf("create group: %v", err)
			}
			store.Init(db, db, nil)

			var received algoModelCheckBody
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if err := json.NewDecoder(r.Body).Decode(&received); err != nil {
					http.Error(w, err.Error(), http.StatusBadRequest)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"success":true,"message":"accepted"}`))
			}))
			defer server.Close()
			t.Setenv("LAZYMIND_CHAT_SERVICE_URL", server.URL)

			req := httptest.NewRequest(
				http.MethodPost,
				"/api/core/model_providers/user-qwen/groups/qwen-group:check",
				strings.NewReader(tc.requestBody),
			)
			req.Header.Set("X-User-Id", "user-1")
			req = mux.SetURLVars(req, map[string]string{
				"model_provider_id": userProvider.ID,
				"group_id":          group.ID,
			})
			rec := httptest.NewRecorder()

			CheckGroup(rec, req)

			if rec.Code != tc.wantStatus {
				t.Fatalf("expected status %d, got %d: %s", tc.wantStatus, rec.Code, rec.Body.String())
			}
			if tc.wantStatus != http.StatusOK {
				return
			}
			if received.APIKey != tc.wantAPIKey {
				t.Fatalf("upstream api key = %q, want %q", received.APIKey, tc.wantAPIKey)
			}
			var stored orm.UserModelProviderGroup
			if err := db.Take(&stored, "id = ?", group.ID).Error; err != nil {
				t.Fatalf("reload group: %v", err)
			}
			if !stored.IsVerified {
				t.Fatal("expected group to be marked verified")
			}
		})
	}
}
