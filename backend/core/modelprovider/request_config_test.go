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

func TestRequestDefaultsUseSupplierAddressAndKeepProxyPrefix(t *testing.T) {
	for _, tc := range []struct {
		name, protocol, base, openai, anthropic string
	}{
		{"openai gateway", "OpenAI", "https://aihubmix.com/v1/", "https://aihubmix.com/v1/", "https://aihubmix.com"},
		{"prefixed openai gateway", "OpenAI", "https://gateway.example/tenant/api/v1/", "https://gateway.example/tenant/api/v1/", "https://gateway.example/tenant/api"},
		{"encoded proxy prefix", "OpenAI", "https://gateway.example/tenant%2Fteam/v1/", "https://gateway.example/tenant%2Fteam/v1/", "https://gateway.example/tenant%2Fteam"},
		{"prefixed anthropic gateway", "Claude", "https://gateway.example/tenant/api", "https://gateway.example/tenant/api/v1/", "https://gateway.example/tenant/api"},
		{"official anthropic", "Claude", "https://api.anthropic.com/v1/", "https://api.anthropic.com/v1/", "https://api.anthropic.com"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			parent := orm.UserModelProvider{Name: tc.protocol, BaseURL: tc.base, IsCustom: true}
			defaults := providerRequestDefaults(nil, parent)
			if defaults["openai"] != tc.openai || defaults["anthropic"] != tc.anthropic {
				t.Fatalf("defaults = %#v, want OpenAI %q / Anthropic %q", defaults, tc.openai, tc.anthropic)
			}
		})
	}
	db, parent, _ := setupEncryptedGroupKeyTest(t, "saved-key")
	t.Cleanup(func() { store.Init(nil, nil, nil) })
	if err := db.AutoMigrate(&orm.DefaultModelProvider{}); err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&orm.DefaultModelProvider{ID: "catalog-openai", Name: "OpenAI", BaseURL: "https://api.openai.com/v1/"}).Error; err != nil {
		t.Fatal(err)
	}
	parent.DefaultModelProviderID = "catalog-openai"
	parent.ProtocolName = "OpenAI"
	parent.BaseURL = "https://private-gateway.example/api/v1/"
	defaults := providerRequestDefaults(db, parent)
	if defaults["openai"] != parent.BaseURL || defaults["anthropic"] != "https://private-gateway.example/api" {
		t.Fatalf("catalog replaced configured supplier address: %#v", defaults)
	}
}

func TestLegacyRequestDefaultsAreRepairedWithoutDiscardingProtocolOverrides(t *testing.T) {
	for _, tc := range []struct {
		name, protocol, base, config, openai, anthropic, anthropicOverride string
	}{
		{"old catalog defaults", "OpenAI", "https://gateway.example/tenant/v1/", `{"default_protocol":"openai","endpoints":{"openai":"https://gateway.example/tenant/v1/","anthropic":"https://api.openai.com"},"defaults":{"openai":"https://api.openai.com/v1/","anthropic":"https://api.openai.com"}}`, "https://gateway.example/tenant/v1/", "https://gateway.example/tenant", ""},
		{"old Claude prefix loss", "Claude", "https://gateway.example/tenant", `{"default_protocol":"anthropic","endpoints":{"openai":"https://gateway.example/v1/","anthropic":"https://gateway.example/tenant"},"defaults":{"openai":"https://gateway.example/v1/","anthropic":"https://gateway.example"}}`, "https://gateway.example/tenant/v1/", "https://gateway.example/tenant", ""},
		{"different Anthropic host", "Claude", "https://explicit.example/claude", `{"default_protocol":"anthropic","endpoints":{"openai":"https://gateway.example/tenant/v1/","anthropic":"https://explicit.example/claude"},"defaults":{"openai":"https://gateway.example/tenant/v1/","anthropic":"https://gateway.example"}}`, "https://gateway.example/tenant/v1/", "https://gateway.example/tenant", "https://explicit.example/claude"},
		{"new defaults retain their anchor", "OpenAI", "https://explicit.example/v1/", `{"defaults_version":1,"default_protocol":"openai","endpoints":{"openai":"https://explicit.example/v1/"},"defaults":{"openai":"https://gateway.example/tenant/v1/","anthropic":"https://gateway.example/tenant"}}`, "https://gateway.example/tenant/v1/", "https://gateway.example/tenant", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			parent := orm.UserModelProvider{Name: tc.protocol, BaseURL: tc.base, IsCustom: true, RequestConfig: tc.config}
			config := savedRequestConfig(nil, parent)
			if config.Defaults["openai"] != tc.openai || config.Defaults["anthropic"] != tc.anthropic || config.Endpoints["anthropic"] != tc.anthropicOverride {
				t.Fatalf("legacy config was not repaired safely: %#v", config)
			}
		})
	}
}

func TestLegacySameOriginDefaultsUseOriginalTemplateProtocol(t *testing.T) {
	for _, source := range []string{"OpenAI", "Claude"} {
		t.Run(source, func(t *testing.T) {
			db, parent, _ := setupEncryptedGroupKeyTest(t, "saved-key")
			t.Cleanup(func() { store.Init(nil, nil, nil) })
			if err := db.AutoMigrate(&orm.DefaultModelProvider{}); err != nil {
				t.Fatal(err)
			}
			if err := db.Create(&orm.DefaultModelProvider{ID: "original-template", Name: source, BaseURL: "https://official.example/v1/"}).Error; err != nil {
				t.Fatal(err)
			}
			parent.DefaultModelProviderID = "original-template"
			parent.Name, parent.ProtocolName, parent.IsCustom = "Gateway", "Claude", true
			parent.BaseURL = "https://gateway.example/anthropic"
			parent.RequestConfig = `{"default_protocol":"anthropic","endpoints":{"openai":"https://gateway.example/v1/","anthropic":"https://gateway.example/anthropic"},"defaults":{"openai":"https://gateway.example/v1/","anthropic":"https://gateway.example"}}`
			config := savedRequestConfig(db, parent)
			wantOpenAI, wantAnthropic, wantOverride := "https://gateway.example/v1/", "https://gateway.example", parent.BaseURL
			if source == "Claude" {
				wantOpenAI, wantAnthropic, wantOverride = "https://gateway.example/anthropic/v1/", parent.BaseURL, ""
			}
			if config.Defaults["openai"] != wantOpenAI || config.Defaults["anthropic"] != wantAnthropic || config.Endpoints["anthropic"] != wantOverride {
				t.Fatalf("original %s template lost protocol path: %#v", source, config)
			}
		})
	}
}

func TestRequestDefaultsFollowAPIAddressWithoutLosingExplicitProtocolOverrides(t *testing.T) {
	db, parent, _ := setupEncryptedGroupKeyTest(t, "saved-key")
	t.Cleanup(func() { store.Init(nil, nil, nil) })
	parent.IsCustom, parent.ProtocolName = true, "OpenAI"
	parent.BaseURL = "https://old-gateway.example/api/v1/"
	if err := db.Model(&parent).Updates(map[string]any{"is_custom": true, "protocol_name": parent.ProtocolName, "base_url": parent.BaseURL}).Error; err != nil {
		t.Fatal(err)
	}
	save := func(body string) {
		t.Helper()
		rec := httptest.NewRecorder()
		SetProviderRequestConfig(rec, requestConfigRequest(http.MethodPut, parent.ID, "user-1", body))
		if rec.Code != http.StatusOK {
			t.Fatalf("config=%d %s", rec.Code, rec.Body.String())
		}
		if err := db.Take(&parent, "id = ?", parent.ID).Error; err != nil {
			t.Fatal(err)
		}
	}
	save(`{"default_protocol":"openai","endpoints":{"openai":"https://explicit-openai.example/v1/","anthropic":"https://explicit.example/claude"}}`)
	if err := setProviderEndpoint(t.Context(), db, &parent, parent.BaseURL, parent.UpdatedAt); err != nil {
		t.Fatal(err)
	}
	if got := savedRequestConfig(db, parent).Defaults["openai"]; got != "https://old-gateway.example/api/v1/" {
		t.Fatalf("unchanged API address replaced the supplier's default: %q", got)
	}
	if err := setProviderEndpoint(t.Context(), db, &parent, "https://new-gateway.example/tenant/v1/", parent.UpdatedAt); err != nil {
		t.Fatal(err)
	}
	response := requestConfigResponse(db, parent)
	for _, entry := range response.Protocols {
		if entry.Protocol == "openai" && entry.DefaultBaseURL != "https://new-gateway.example/tenant/v1/" {
			t.Fatalf("OpenAI default did not follow API address: %#v", entry)
		}
		if entry.Protocol == "anthropic" && (entry.DefaultBaseURL != "https://new-gateway.example/tenant" || entry.BaseURL != "https://explicit.example/claude") {
			t.Fatalf("Anthropic default or explicit override lost: %#v", entry)
		}
	}
	// The UI submits the displayed effective addresses for both protocols.
	save(`{"default_protocol":"openai","endpoints":{"openai":"https://new-gateway.example/tenant/v1/","anthropic":"https://new-gateway.example/tenant"}}`)
	config := savedRequestConfig(db, parent)
	if config.Endpoints["openai"] != "" || config.Endpoints["anthropic"] != "" {
		t.Fatalf("displayed defaults were frozen into overrides: %#v", config.Endpoints)
	}
	save(`{"default_protocol":"anthropic","endpoints":{"anthropic":""}}`)
	if parent.BaseURL != "https://new-gateway.example/tenant" {
		t.Fatalf("clearing Anthropic override used stale API address: %q", parent.BaseURL)
	}
	save(`{"default_protocol":"openai","endpoints":{"openai":""}}`)
	if parent.BaseURL != "https://new-gateway.example/tenant/v1/" {
		t.Fatalf("switching back lost OpenAI default: %q", parent.BaseURL)
	}
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
