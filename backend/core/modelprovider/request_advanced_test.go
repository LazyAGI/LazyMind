package modelprovider

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/mux"

	"lazymind/core/common/orm"
	"lazymind/core/credentialvault"
	"lazymind/core/store"
)

func TestAdvancedRequestSettingsPlaintextAndPreserveCredentials(t *testing.T) {
	db, parent, group := setupEncryptedGroupKeyTest(t, "saved-key")
	t.Cleanup(func() { store.Init(nil, nil, nil) })
	if err := prepareProviderKeySelection(t.Context(), db, &parent); err != nil {
		t.Fatal(err)
	}
	body := `{"default_protocol":"openai","endpoints":{"openai":"","openai-responses":"","anthropic":""},"advanced":{"headers":{"X-Gateway-Token":"private-gateway-secret"},"query":{"tenant":"private-tenant"},"extra_body":{"metadata":{"project":"private-project"}}}}`
	rec := httptest.NewRecorder()
	SetProviderRequestConfig(rec, requestConfigRequest(http.MethodPut, parent.ID, "user-1", body))
	if rec.Code != http.StatusOK {
		t.Fatalf("save advanced configuration: %d %s", rec.Code, rec.Body.String())
	}
	var saved orm.UserModelProvider
	if err := db.First(&saved, "id = ?", parent.ID).Error; err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"private-gateway-secret", "private-tenant", "private-project"} {
		if !strings.Contains(saved.RequestConfig, secret) {
			t.Fatal("advanced request option was not stored in plaintext")
		}
	}
	get := httptest.NewRecorder()
	GetProviderRequestConfig(get, requestConfigRequest(http.MethodGet, parent.ID, "user-1", ""))
	var response struct {
		Data struct {
			Advanced struct {
				Headers map[string]string `json:"headers"`
			} `json:"advanced"`
		} `json:"data"`
	}
	if json.Unmarshal(get.Body.Bytes(), &response) != nil || response.Data.Advanced.Headers["X-Gateway-Token"] != "private-gateway-secret" {
		t.Fatalf("authorized edit did not recover advanced settings: %d", get.Code)
	}
	var after orm.UserModelProviderGroup
	if err := db.First(&after, "id = ?", group.ID).Error; err != nil {
		t.Fatal(err)
	}
	pool, err := apiKeyForGroup(db, &after)
	if err != nil || pool != "saved-key" || saved.ActiveKeyID != parent.ActiveKeyID || saved.ActiveKeyGroupID != parent.ActiveKeyGroupID {
		t.Fatal("request options changed the enabled credential")
	}
	unchanged := httptest.NewRecorder()
	SetProviderRequestConfig(unchanged, requestConfigRequest(http.MethodPut, parent.ID, "user-1", `{"default_protocol":"openai"}`))
	if unchanged.Code != 200 {
		t.Fatalf("legacy save failed: %d", unchanged.Code)
	}
	var preserved orm.UserModelProvider
	if err := db.First(&preserved, "id = ?", parent.ID).Error; err != nil || preserved.RequestConfig != saved.RequestConfig {
		t.Fatal("omitted advanced settings did not preserve saved configuration")
	}
}

func TestAdvancedRequestSettingsReachDiscoveryCheckAndProbe(t *testing.T) {
	t.Setenv("LAZYMIND_MODEL_PROVIDER_SECRET_KEY", "advanced-test-only")
	db, parent, group, model := setupProbeTest(t)
	if err := db.Model(&parent).Update("capabilities", "has_models,custom_base_url").Error; err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	SetProviderRequestConfig(rec, requestConfigRequest(http.MethodPut, parent.ID, "probe-owner", `{"default_protocol":"openai","advanced":{"headers":{"X-Gateway-Token":"private-token"},"query":{"tenant":"private-tenant"},"extra_body":{"metadata":{"project":"private-project"}}}}`))
	if rec.Code != 200 {
		t.Fatalf("save advanced: %d %s", rec.Code, rec.Body.String())
	}
	var discovered bool
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		discovered = true
		if r.Header.Get("X-Gateway-Token") != "private-token" || r.Header.Get("Authorization") != "Bearer active-test-key" || r.URL.Query().Get("tenant") != "private-tenant" || r.URL.Path != "/v1/models" || r.ContentLength > 0 {
			t.Error("discovery lost private request settings or active key")
		}
		_, _ = w.Write([]byte(`{"data":[{"id":"test-chat"}]}`))
	}))
	defer upstream.Close()
	_ = siliconFlowTestBaseURL(t, upstream)
	request := httptest.NewRequest(http.MethodGet, "/remote-models", nil)
	request.Header.Set("X-User-Id", "probe-owner")
	request = mux.SetURLVars(request, map[string]string{"model_provider_id": parent.ID, "group_id": group.ID})
	list := httptest.NewRecorder()
	ListRemoteGroupModels(list, request)
	if list.Code != 200 || !discovered {
		t.Fatalf("discovery: %d %s", list.Code, list.Body.String())
	}
	var checked, probed bool
	algorithm := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			RequestAdvanced credentialvault.ProviderRequestAdvanced `json:"request_advanced"`
			APIKey          string                                  `json:"api_key"`
		}
		if json.NewDecoder(r.Body).Decode(&body) != nil || body.RequestAdvanced.Headers["X-Gateway-Token"] != "private-token" || body.RequestAdvanced.Query["tenant"] != "private-tenant" || body.APIKey != "active-test-key" {
			t.Error("algorithm request lost private settings or selected key")
		}
		if r.URL.Path == "/api/model/probe" {
			probed = true
			_, _ = w.Write([]byte(`{"status":"passed","code":"ok","model_type":"llm"}`))
		} else {
			checked = true
			_, _ = w.Write([]byte(`{"success":true,"message":"available"}`))
		}
	}))
	defer algorithm.Close()
	t.Setenv("LAZYMIND_CHAT_SERVICE_URL", algorithm.URL)
	if err := db.First(&parent, "id = ?", parent.ID).Error; err != nil {
		t.Fatal(err)
	}
	check, err := doProviderGroupCheck(t.Context(), "model", RuntimeProviderName(parent), parent.BaseURL, "active-test-key", "", parent)
	if err != nil || check == nil || !check.Success || !checked {
		t.Fatal("check did not consume advanced request options")
	}
	probe := httptest.NewRecorder()
	ProbeGroupModel(probe, probeRequest(parent.ID, group.ID, model.ID))
	if probe.Code != 200 || !probed || decodeProbeTest(t, probe).Status != "passed" {
		t.Fatalf("probe failed: %d %s", probe.Code, probe.Body.String())
	}
	for _, response := range []*httptest.ResponseRecorder{list, probe} {
		if strings.Contains(response.Body.String(), "private-token") || strings.Contains(response.Body.String(), "private-tenant") || strings.Contains(response.Body.String(), "private-project") {
			t.Fatal("diagnostic response leaked private request configuration")
		}
	}
}

func TestAdvancedRequestBackupRestoresPlaintextOptions(t *testing.T) {
	db, parent, group := setupEncryptedGroupKeyTest(t, "saved-key")
	t.Cleanup(func() { store.Init(nil, nil, nil) })
	rec := httptest.NewRecorder()
	SetProviderRequestConfig(rec, requestConfigRequest(http.MethodPut, parent.ID, "user-1", `{"default_protocol":"openai","advanced":{"headers":{"X-Gateway-Token":"backup-private"},"query":{"tenant":"private-tenant"}}}`))
	if rec.Code != 200 {
		t.Fatalf("save: %d", rec.Code)
	}
	payload, err := (CredentialBackupSource{DB: db}).LoadCredential(t.Context(), group.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer clear(payload.APIKey)
	if !strings.Contains(payload.RequestConfig, "backup-private") || strings.Contains(payload.RequestConfig, "advanced_ciphertext") {
		t.Fatal("backup did not export portable request options inside its encrypted payload")
	}
	if _, err := credentialvault.EncodeProviderCredentialPayload(payload); err != nil {
		t.Fatal(err)
	}
	restore := SetCredentialKeyManager(nil)
	defer restore()
	destination, err := restoredSupplier(db, "user-1", payload, credentialvault.ConflictSaveCopy, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if destination.ID == parent.ID || !strings.Contains(destination.RequestConfig, "backup-private") || strings.Contains(destination.RequestConfig, "advanced_ciphertext") {
		t.Fatal("restore did not preserve plaintext options in a separate supplier")
	}
	advanced, err := ResolveProviderRequestAdvanced(destination)
	if err != nil || advanced.Headers["X-Gateway-Token"] != "backup-private" || advanced.Query["tenant"] != "private-tenant" {
		t.Fatal("destination could not read restored plaintext options")
	}

}

func TestAdvancedRequestSettingsRejectProtectedFieldsWithoutMutation(t *testing.T) {
	for _, advanced := range []string{
		`{"headers":{"Authorization":"override"}}`,
		`{"headers":{"X-Test":"line\r\ninjection"}}`,
		`{"extra_body":{"model":"replacement"}}`,
		`{"extra_body":{"messages":[]}}`,
		`{"headers":{"X-Test":123}}`,
		`{"extra_body":[]}`,
		`{"extra_body":{"metadata":"` + strings.Repeat("a", 6200) + `"}}`,
	} {
		t.Run(advanced[:min(30, len(advanced))], func(t *testing.T) {
			db, parent, _ := setupEncryptedGroupKeyTest(t, "saved-key")
			t.Cleanup(func() { store.Init(nil, nil, nil) })
			rec := httptest.NewRecorder()
			SetProviderRequestConfig(rec, requestConfigRequest(http.MethodPut, parent.ID, "user-1", `{"default_protocol":"openai","advanced":`+advanced+`}`))
			if rec.Code != 400 {
				t.Fatalf("invalid advanced configuration accepted: %d", rec.Code)
			}
			var saved orm.UserModelProvider
			if err := db.First(&saved, "id = ?", parent.ID).Error; err != nil || saved.RequestConfig != parent.RequestConfig {
				t.Fatal("rejected request mutated saved configuration")
			}
		})
	}
}

func TestAdvancedRequestSaveWithoutKeyStorePreservesAPIKeyCiphertext(t *testing.T) {
	for _, legacy := range []bool{false, true} {
		t.Run(fmt.Sprint(legacy), func(t *testing.T) {
			db, parent, group := setupEncryptedGroupKeyTest(t, "saved-key")
			t.Cleanup(func() { store.Init(nil, nil, nil) })
			if err := prepareProviderKeySelection(t.Context(), db, &parent); err != nil {
				t.Fatal(err)
			}
			if legacy {
				old, err := encryptModelProviderAPIKeyForGroup(parent.CreateUserID, requestAdvancedAAD(parent.ID), 1, `{"query":{"old":"value"}}`)
				if err != nil {
					t.Fatal(err)
				}
				raw, _ := json.Marshal(savedProviderRequestConfig{DefaultProtocol: "openai", AdvancedCiphertext: old})
				parent.RequestConfig = string(raw)
				if err := db.Model(&parent).Update("request_config", parent.RequestConfig).Error; err != nil {
					t.Fatal(err)
				}
			}
			restore := SetCredentialKeyManager(nil)
			defer restore()
			for _, advanced := range []string{`{"query":{"tenant":"new"},"extra_body":{"temperature":0.2}}`, `{}`} {
				rec := httptest.NewRecorder()
				SetProviderRequestConfig(rec, requestConfigRequest(http.MethodPut, parent.ID, "user-1", `{"default_protocol":"openai","advanced":`+advanced+`}`))
				if rec.Code != 200 {
					t.Fatalf("save without key store: %d %s", rec.Code, rec.Body.String())
				}
				var saved orm.UserModelProvider
				if err := db.First(&saved, "id = ?", parent.ID).Error; err != nil {
					t.Fatal(err)
				}
				if strings.Contains(saved.RequestConfig, "advanced_ciphertext") {
					t.Fatal("old ciphertext retained")
				}
				actual, err := ResolveProviderRequestAdvanced(saved)
				if err != nil {
					t.Fatal(err)
				}
				if advanced == "{}" && !actual.Empty() || advanced != "{}" && actual.Query["tenant"] != "new" {
					t.Fatalf("wrong options: %#v", actual)
				}
				var after orm.UserModelProviderGroup
				if err := db.First(&after, "id = ?", group.ID).Error; err != nil {
					t.Fatal(err)
				}
				if after.APIKeyCiphertext != group.APIKeyCiphertext || after.CredentialRevision != group.CredentialRevision || after.APIKey != "" {
					t.Fatal("advanced save changed API key storage")
				}
				get := httptest.NewRecorder()
				GetProviderRequestConfig(get, requestConfigRequest(http.MethodGet, parent.ID, "user-1", ""))
				if get.Code != 200 {
					t.Fatalf("reload without key store: %d", get.Code)
				}
			}
		})
	}
}

func TestLegacyAdvancedRequestReadAndOmittedSavePreserveValues(t *testing.T) {
	db, parent, _ := setupEncryptedGroupKeyTest(t, "saved-key")
	t.Cleanup(func() { store.Init(nil, nil, nil) })
	old, err := encryptModelProviderAPIKeyForGroup(parent.CreateUserID, requestAdvancedAAD(parent.ID), 1, `{"query":{"tenant":"legacy"}}`)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(savedProviderRequestConfig{DefaultProtocol: "openai", AdvancedCiphertext: old})
	parent.RequestConfig = string(raw)
	if err := db.Model(&parent).Update("request_config", parent.RequestConfig).Error; err != nil {
		t.Fatal(err)
	}
	got, err := ResolveProviderRequestAdvanced(parent)
	if err != nil || got.Query["tenant"] != "legacy" {
		t.Fatalf("legacy read: %#v %v", got, err)
	}
	rec := httptest.NewRecorder()
	SetProviderRequestConfig(rec, requestConfigRequest(http.MethodPut, parent.ID, "user-1", `{"default_protocol":"openai"}`))
	if rec.Code != 200 {
		t.Fatalf("legacy save: %d", rec.Code)
	}
	var saved orm.UserModelProvider
	if err := db.First(&saved, "id = ?", parent.ID).Error; err != nil {
		t.Fatal(err)
	}
	got, err = ResolveProviderRequestAdvanced(saved)
	if err != nil || got.Query["tenant"] != "legacy" {
		t.Fatal("omitted advanced lost legacy values")
	}
	restore := SetCredentialKeyManager(nil)
	defer restore()
	// An unavailable legacy key must never be interpreted as empty settings.
	if _, err := ResolveProviderRequestAdvanced(parent); err == nil {
		t.Fatal("unreadable legacy ciphertext silently discarded")
	}
}

func TestUnreadableLegacyAdvancedConfigCanBeExplicitlyReplaced(t *testing.T) {
	db, parent, _ := setupEncryptedGroupKeyTest(t, "saved-key")
	t.Cleanup(func() { store.Init(nil, nil, nil) })
	parent.RequestConfig = `{"default_protocol":"openai","advanced_ciphertext":"unreadable"}`
	if err := db.Model(&parent).Update("request_config", parent.RequestConfig).Error; err != nil {
		t.Fatal(err)
	}
	restore := SetCredentialKeyManager(nil)
	defer restore()
	get := httptest.NewRecorder()
	GetProviderRequestConfig(get, requestConfigRequest(http.MethodGet, parent.ID, "user-1", ""))
	if get.Code != 200 || !strings.Contains(get.Body.String(), `"advanced_unavailable":true`) {
		t.Fatalf("legacy recovery unavailable: %d %s", get.Code, get.Body.String())
	}
	var saved orm.UserModelProvider
	if err := db.First(&saved, "id = ?", parent.ID).Error; err != nil {
		t.Fatal(err)
	}
	if saved.RequestConfig != parent.RequestConfig {
		t.Fatal("GET discarded unreadable legacy configuration")
	}
}
