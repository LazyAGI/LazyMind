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

func TestCheckGroupRequiresAPIKeyOnlyForDefaultBaseURL(t *testing.T) {
	tests := []struct {
		name           string
		providerName   string
		defaultBaseURL string
		storedBaseURL  string
		requestBody    string
		wantStatus     int
		wantAPIKey     string
		wantSource     string
		wantURL        string
		wantStoredURL  string
		wantSkipAuth   bool
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
			name:          "omitted key is accepted for custom base url",
			storedBaseURL: "https://models.example.com/v1",
			requestBody:   `{"provider_name":"Qwen","base_url":"https://models.example.com/v1","dry_run":false}`,
			wantStatus:    http.StatusOK,
			wantSource:    "qwen",
			wantURL:       "https://models.example.com/v1",
			wantSkipAuth:  true,
		},
		{
			name:          "empty key is accepted for custom base url",
			storedBaseURL: "http://172.24.176.1:43435/v1/",
			requestBody:   `{"provider_name":"Qwen","base_url":"http://172.24.176.1:43435/v1/","api_key":"","dry_run":false}`,
			wantStatus:    http.StatusOK,
			wantSource:    "qwen",
			wantURL:       "http://172.24.176.1:43435/v1/",
			wantSkipAuth:  true,
		},
		{
			name:        "provided key takes precedence",
			requestBody: `{"provider_name":"Qwen","base_url":"https://dashscope.aliyuncs.com/","api_key":"request-key","dry_run":false}`,
			wantStatus:  http.StatusOK,
			wantAPIKey:  "request-key",
			wantSource:  "qwen",
			wantURL:     "https://dashscope.aliyuncs.com/",
		},
		{
			name:         "official provider proxy keeps official source",
			providerName: "Qwen",
			requestBody:  `{"provider_name":"OpenAI","base_url":"http://12.34.56.78:8000/v1","api_key":"proxy-request-key","dry_run":false}`,
			wantStatus:   http.StatusOK,
			wantAPIKey:   "proxy-request-key",
			wantSource:   "qwen",
			wantURL:      "http://12.34.56.78:8000/v1",
		},
		{
			name:         "request provider fragment is replaced by canonical parent",
			providerName: "OpenRouter",
			requestBody:  `{"provider_name":"port!!!garbage","base_url":"https://proxy.example.com/v1/","api_key":"fragment-key","dry_run":false}`,
			wantStatus:   http.StatusOK,
			wantAPIKey:   "fragment-key",
			wantSource:   "openrouter",
			wantURL:      "https://proxy.example.com/v1/",
		},
		{
			name:           "official openrouter suffix is removed after verification",
			providerName:   "OpenRouter",
			defaultBaseURL: "https://openrouter.ai/api/v1/",
			storedBaseURL:  "https://openrouter.ai/api/v1/invalid_suffix",
			requestBody:    `{"provider_name":"OpenRouter","base_url":"https://openrouter.ai/api/v1/invalid_suffix","api_key":"openrouter-key","dry_run":false}`,
			wantStatus:     http.StatusOK,
			wantAPIKey:     "openrouter-key",
			wantSource:     "openrouter",
			wantURL:        "https://openrouter.ai/api/v1/",
			wantStoredURL:  "https://openrouter.ai/api/v1/",
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
			providerName := tc.providerName
			if providerName == "" {
				providerName = "Qwen"
			}
			defaultBaseURL := tc.defaultBaseURL
			if defaultBaseURL == "" {
				defaultBaseURL = "https://dashscope.aliyuncs.com/"
			}
			defaultProvider := orm.DefaultModelProvider{
				ID:          "default-qwen",
				Name:        providerName,
				Description: "Qwen provider",
				BaseURL:     defaultBaseURL,
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
			storedBaseURL := tc.storedBaseURL
			if storedBaseURL == "" {
				storedBaseURL = defaultProvider.BaseURL
			}
			group := orm.UserModelProviderGroup{
				ID:                  "qwen-group",
				UserModelProviderID: userProvider.ID,
				Name:                "Qwen",
				BaseURL:             storedBaseURL,
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
			if received.Source != tc.wantSource {
				t.Fatalf("upstream source = %q, want canonical provider source %q", received.Source, tc.wantSource)
			}
			if received.URL != tc.wantURL {
				t.Fatalf("upstream URL = %q, want proxy URL %q", received.URL, tc.wantURL)
			}
			if received.SkipAuth != tc.wantSkipAuth {
				t.Fatalf("upstream skip_auth = %t, want %t", received.SkipAuth, tc.wantSkipAuth)
			}
			var stored orm.UserModelProviderGroup
			if err := db.Take(&stored, "id = ?", group.ID).Error; err != nil {
				t.Fatalf("reload group: %v", err)
			}
			if !stored.IsVerified {
				t.Fatal("expected group to be marked verified")
			}
			if tc.wantStoredURL != "" && stored.BaseURL != tc.wantStoredURL {
				t.Fatalf("stored base URL = %q, want %q", stored.BaseURL, tc.wantStoredURL)
			}
		})
	}
}

func TestCreateGroupVerifiesSubmittedAPIKeyBeforeSaving(t *testing.T) {
	t.Setenv("LAZYMIND_MODEL_PROVIDER_SECRET_KEY", "create-group-test-key")

	db, err := gorm.Open(sqlite.Open("file:create_group_verify?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if err := db.AutoMigrate(
		&orm.DefaultModelProvider{},
		&orm.UserModelProvider{},
		&orm.UserModelProviderGroup{},
		&orm.UserSelectedModel{},
	); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	now := time.Now()
	defaultProvider := orm.DefaultModelProvider{
		ID:          "default-qwen-create",
		Name:        "Qwen",
		Description: "Qwen provider",
		BaseURL:     "https://dashscope.aliyuncs.com/",
		Category:    defaultProviderCategory,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	userProvider := orm.UserModelProvider{
		ID:                     "user-qwen-create",
		DefaultModelProviderID: defaultProvider.ID,
		Name:                   defaultProvider.Name,
		Description:            defaultProvider.Description,
		BaseURL:                defaultProvider.BaseURL,
		Category:               defaultProvider.Category,
		Capabilities:           "multi_group,custom_base_url",
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
	store.Init(db, db, nil)

	var received algoModelCheckBody
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&received); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if received.APIKey == "rejected-key" {
			_, _ = w.Write([]byte(`{"success":false,"message":"rejected"}`))
			return
		}
		_, _ = w.Write([]byte(`{"success":true,"message":"accepted"}`))
	}))
	defer server.Close()
	t.Setenv("LAZYMIND_CHAT_SERVICE_URL", server.URL)

	req := httptest.NewRequest(
		http.MethodPost,
		"/api/core/model_providers/user-qwen-create/groups",
		strings.NewReader(`{"name":"Qwen","base_url":"https://dashscope.aliyuncs.com/","api_key":"submitted-key","verify":true}`),
	)
	req.Header.Set("X-User-Id", "user-1")
	req = mux.SetURLVars(req, map[string]string{"model_provider_id": userProvider.ID})
	rec := httptest.NewRecorder()

	CreateGroup(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d: %s", http.StatusOK, rec.Code, rec.Body.String())
	}
	if received.APIKey != "submitted-key" {
		t.Fatalf("upstream api key = %q, want submitted key", received.APIKey)
	}
	var group orm.UserModelProviderGroup
	if err := db.Where("user_model_provider_id = ?", userProvider.ID).Take(&group).Error; err != nil {
		t.Fatalf("load created group: %v", err)
	}
	if !group.IsVerified {
		t.Fatal("expected created group to be marked verified")
	}

	updateReq := httptest.NewRequest(
		http.MethodPatch,
		"/api/core/model_providers/user-qwen-create/groups/"+group.ID,
		strings.NewReader(`{"name":"Qwen","base_url":"https://dashscope.aliyuncs.com/","api_key":"updated-key","verify":true}`),
	)
	updateReq.Header.Set("X-User-Id", "user-1")
	updateReq = mux.SetURLVars(updateReq, map[string]string{
		"model_provider_id": userProvider.ID,
		"group_id":          group.ID,
	})
	updateRec := httptest.NewRecorder()

	UpdateGroup(updateRec, updateReq)

	if updateRec.Code != http.StatusOK {
		t.Fatalf("expected update status %d, got %d: %s", http.StatusOK, updateRec.Code, updateRec.Body.String())
	}
	if received.APIKey != "updated-key" {
		t.Fatalf("updated upstream api key = %q, want updated key", received.APIKey)
	}
	var updateResponse struct {
		Data struct {
			Check *CheckModelProviderData `json:"check"`
		} `json:"data"`
	}
	if err := json.NewDecoder(updateRec.Body).Decode(&updateResponse); err != nil {
		t.Fatalf("decode update response: %v", err)
	}
	if updateResponse.Data.Check == nil || !updateResponse.Data.Check.Success {
		t.Fatalf("expected successful verification data, got %+v", updateResponse.Data.Check)
	}

	failedReq := httptest.NewRequest(
		http.MethodPost,
		"/api/core/model_providers/user-qwen-create/groups",
		strings.NewReader(`{"name":"Rejected","base_url":"https://dashscope.aliyuncs.com/","api_key":"rejected-key","verify":true}`),
	)
	failedReq.Header.Set("X-User-Id", "user-1")
	failedReq = mux.SetURLVars(failedReq, map[string]string{"model_provider_id": userProvider.ID})
	failedRec := httptest.NewRecorder()

	CreateGroup(failedRec, failedReq)

	if failedRec.Code != http.StatusBadGateway {
		t.Fatalf("expected status %d, got %d: %s", http.StatusBadGateway, failedRec.Code, failedRec.Body.String())
	}
	var count int64
	if err := db.Model(&orm.UserModelProviderGroup{}).Where("user_model_provider_id = ?", userProvider.ID).Count(&count).Error; err != nil {
		t.Fatalf("count created groups: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected failed verification to create no group, got %d groups", count)
	}
}
