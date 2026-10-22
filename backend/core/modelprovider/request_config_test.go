package modelprovider

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gorilla/mux"
	"lazymind/core/common/orm"
	"lazymind/core/store"
)

func requestConfigRequest(method, provider, user, body string) *http.Request {
	req := httptest.NewRequest(method, "/request-config", strings.NewReader(body))
	req.Header.Set("X-User-Id", user)
	return mux.SetURLVars(req, map[string]string{"model_provider_id": provider})
}

func TestRequestConfigSwitchesRuntimeProtocolAndRetainsModelIdentity(t *testing.T) {
	db, parent, group := setupEncryptedGroupKeyTest(t, "saved-key")
	t.Cleanup(func() { store.Init(nil, nil, nil) })
	if err := db.AutoMigrate(&orm.DefaultModelProvider{}, &orm.UserModelProviderGroupModel{}); err != nil {
		t.Fatal(err)
	}
	model := orm.UserModelProviderGroupModel{ID: "chat-model", UserModelProviderID: parent.ID, UserModelProviderGroupID: group.ID, ProviderName: "Qwen", Name: "claude-sonnet", ModelType: "llm", BaseModel: group.BaseModel}
	if err := db.Create(&model).Error; err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	SetProviderRequestConfig(rec, requestConfigRequest(http.MethodPut, parent.ID, "user-1", `{"default_protocol":"anthropic","endpoints":{"anthropic":"https://proxy.example"}}`))
	if rec.Code != http.StatusOK {
		t.Fatalf("config=%d %s", rec.Code, rec.Body.String())
	}
	var current orm.UserModelProvider
	db.Take(&current, "id = ?", parent.ID)
	db.Take(&model, "id = ?", model.ID)
	if current.Name != "Qwen" || RuntimeProviderName(current) != "Claude" || model.ProviderName != "Claude" || model.ID != "chat-model" {
		t.Fatal("default request protocol did not reach model runtime without replacing display identity")
	}
	if current.BaseURL != "https://proxy.example" {
		t.Fatal("default endpoint not activated")
	}
	assertStoredEncryptedAPIKeys(t, db, group.ID, "saved-key", false)
	var stored orm.UserModelProviderGroup
	db.Take(&stored, "id = ?", group.ID)
	if stored.BaseURL != current.BaseURL {
		t.Fatal("runtime endpoint mirror stale")
	}
	get := httptest.NewRecorder()
	GetProviderRequestConfig(get, requestConfigRequest(http.MethodGet, parent.ID, "user-1", ""))
	var response struct {
		Data providerRequestConfigResponse `json:"data"`
	}
	if json.Unmarshal(get.Body.Bytes(), &response) != nil || response.Data.DefaultProtocol != "anthropic" || len(response.Data.Protocols) != 4 {
		t.Fatal("request configuration did not persist")
	}
	for _, p := range response.Data.Protocols {
		if (p.Protocol == "gemini" || p.Protocol == "openai-responses") && p.Supported {
			t.Fatal("unsupported runtime advertised as functional")
		}
	}
}

func TestRequestConfigNoOpAndInactiveEndpointDoNotInvalidateEnabledKey(t *testing.T) {
	db, parent, group := setupEncryptedGroupKeyTest(t, "saved-key")
	t.Cleanup(func() { store.Init(nil, nil, nil) })
	rec := httptest.NewRecorder()
	SetProviderRequestConfig(rec, requestConfigRequest(http.MethodPut, parent.ID, "user-1", `{"default_protocol":"openai","endpoints":{"openai":"https://dashscope.aliyuncs.com/"}}`))
	if rec.Code != http.StatusOK {
		t.Fatalf("noop=%d %s", rec.Code, rec.Body.String())
	}
	var stored orm.UserModelProviderGroup
	db.Take(&stored, "id = ?", group.ID)
	if stored.CredentialRevision != 1 || !stored.IsVerified {
		t.Fatal("no-op config invalidated valid credential")
	}
	rec = httptest.NewRecorder()
	SetProviderRequestConfig(rec, requestConfigRequest(http.MethodPut, parent.ID, "user-1", `{"default_protocol":"openai","endpoints":{"anthropic":"https://anthropic-proxy.example"}}`))
	if rec.Code != http.StatusOK {
		t.Fatalf("inactive=%d %s", rec.Code, rec.Body.String())
	}
	assertStoredEncryptedAPIKeys(t, db, group.ID, "saved-key", true)
	db.Take(&parent, "id = ?", parent.ID)
	if RuntimeProviderName(parent) != "Qwen" {
		t.Fatal("inactive configuration changed native adapter")
	}
	if err := setProviderEndpoint(t.Context(), db, &parent, "https://new-active.example/v1", parent.UpdatedAt); err != nil {
		t.Fatal(err)
	}
	get := httptest.NewRecorder()
	GetProviderRequestConfig(get, requestConfigRequest(http.MethodGet, parent.ID, "user-1", ""))
	if !strings.Contains(get.Body.String(), "https://new-active.example/v1") || !strings.Contains(get.Body.String(), "https://anthropic-proxy.example") {
		t.Fatal("API Host edit lost request endpoint configuration")
	}
}

func TestRequestConfigRejectsUnsupportedProtocolAndForeignSupplier(t *testing.T) {
	_, parent, _ := setupEncryptedGroupKeyTest(t, "saved-key")
	t.Cleanup(func() { store.Init(nil, nil, nil) })
	for _, tc := range []struct {
		user, body string
		status     int
	}{
		{"user-1", `{"default_protocol":"gemini"}`, http.StatusBadRequest},
		{"user-1", `{"default_protocol":"openai","endpoints":{"gemini":"https://example.com"}}`, http.StatusBadRequest},
		{"user-1", `{"default_protocol":"openai","endpoints":{"openai":"https://secret@example.com/v1"}}`, http.StatusBadRequest},
		{"other", `{"default_protocol":"openai"}`, http.StatusNotFound},
	} {
		rec := httptest.NewRecorder()
		SetProviderRequestConfig(rec, requestConfigRequest(http.MethodPut, parent.ID, tc.user, tc.body))
		if rec.Code != tc.status {
			t.Fatalf("status=%d want=%d", rec.Code, tc.status)
		}
	}
}

func TestRequestConfigAnthropicRejectsNonChatModels(t *testing.T) {
	db, parent, group := setupEncryptedGroupKeyTest(t, "saved-key")
	t.Cleanup(func() { store.Init(nil, nil, nil) })
	if err := db.AutoMigrate(&orm.UserModelProviderGroupModel{}); err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&orm.UserModelProviderGroupModel{ID: "embedding", UserModelProviderID: parent.ID, UserModelProviderGroupID: group.ID, ProviderName: "Qwen", Name: "embed", ModelType: "embed", BaseModel: group.BaseModel}).Error; err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	SetProviderRequestConfig(rec, requestConfigRequest(http.MethodPut, parent.ID, "user-1", `{"default_protocol":"anthropic"}`))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("incompatible model accepted: %d", rec.Code)
	}
	assertStoredEncryptedAPIKeys(t, db, group.ID, "saved-key", true)
}
