package modelprovider

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"lazymind/core/common/orm"
	"lazymind/core/store"
)

func TestRequestDefaultsDistinguishArkProtocolRoots(t *testing.T) {
	for _, tc := range []struct {
		name, protocol, base, openai, anthropic string
	}{
		{"ordinary API", "Doubao", "https://ark.cn-beijing.volces.com/api/v3/", "https://ark.cn-beijing.volces.com/api/v3/", "https://ark.cn-beijing.volces.com/api/compatible"},
		{"ordinary Anthropic API", "Claude", "https://ark.cn-beijing.volces.com/api/compatible", "https://ark.cn-beijing.volces.com/api/v3/", "https://ark.cn-beijing.volces.com/api/compatible"},
		{"coding API", "Doubao", "https://ark.cn-beijing.volces.com/api/coding/v3", "https://ark.cn-beijing.volces.com/api/coding/v3/", "https://ark.cn-beijing.volces.com/api/coding"},
		{"coding Anthropic API", "Claude", "https://ark.cn-beijing.volces.com/api/coding", "https://ark.cn-beijing.volces.com/api/coding/v3/", "https://ark.cn-beijing.volces.com/api/coding"},
		{"custom host", "OpenAI", "https://gateway.example/api/v3/", "https://gateway.example/api/v3/", "https://gateway.example/api/v3"},
		{"official host with private prefix", "OpenAI", "https://ark.cn-beijing.volces.com/tenant/api/v3/", "https://ark.cn-beijing.volces.com/tenant/api/v3/", "https://ark.cn-beijing.volces.com/tenant/api/v3"},
		{"encoded official path", "OpenAI", "https://ark.cn-beijing.volces.com/tenant%2Fteam/api/v3/", "https://ark.cn-beijing.volces.com/tenant%2Fteam/api/v3/", "https://ark.cn-beijing.volces.com/tenant%2Fteam/api/v3"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			defaults := providerRequestDefaults(nil, orm.UserModelProvider{Name: tc.protocol, BaseURL: tc.base})
			if defaults["openai"] != tc.openai || defaults["anthropic"] != tc.anthropic {
				t.Fatalf("defaults = %#v, want OpenAI %q / Anthropic %q", defaults, tc.openai, tc.anthropic)
			}
		})
	}
}

func TestStoredArkDefaultsRepairOnlyInheritedAddresses(t *testing.T) {
	for _, tc := range []struct {
		name, base, oldDefault, want, endpoint, wantEndpoint string
	}{
		{"ordinary empty override", "https://ark.cn-beijing.volces.com/api/v3/", "https://ark.cn-beijing.volces.com/api/v3", "https://ark.cn-beijing.volces.com/api/compatible", "", ""},
		{"ordinary copied default", "https://ark.cn-beijing.volces.com/api/v3/", "https://ark.cn-beijing.volces.com/api/v3", "https://ark.cn-beijing.volces.com/api/compatible", "https://ark.cn-beijing.volces.com/api/v3/", ""},
		{"coding copied default", "https://ark.cn-beijing.volces.com/api/coding/v3/", "https://ark.cn-beijing.volces.com/api/coding/v3", "https://ark.cn-beijing.volces.com/api/coding", "https://ark.cn-beijing.volces.com/api/coding/v3", ""},
		{"explicit different host", "https://ark.cn-beijing.volces.com/api/v3/", "https://ark.cn-beijing.volces.com/api/v3", "https://ark.cn-beijing.volces.com/api/compatible", "https://private.example/anthropic", "https://private.example/anthropic"},
		{"explicit same-host path", "https://ark.cn-beijing.volces.com/api/v3/", "https://ark.cn-beijing.volces.com/api/v3", "https://ark.cn-beijing.volces.com/api/compatible", "https://ark.cn-beijing.volces.com/private-proxy", "https://ark.cn-beijing.volces.com/private-proxy"},
		{"independent stored default", "https://ark.cn-beijing.volces.com/api/v3/", "https://original.example/anthropic", "https://original.example/anthropic", "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			stored := savedProviderRequestConfig{DefaultsVersion: 1, DefaultProtocol: "openai", Endpoints: map[string]string{"anthropic": tc.endpoint}, Defaults: map[string]string{"openai": tc.base, "anthropic": tc.oldDefault}}
			encoded, _ := json.Marshal(stored)
			parent := orm.UserModelProvider{Name: "Doubao", BaseURL: "https://explicit-openai.example/v1/", RequestConfig: string(encoded)}
			config := savedRequestConfig(nil, parent)
			if config.DefaultsVersion != 1 || config.Defaults["openai"] != tc.base || config.Defaults["anthropic"] != tc.want || config.Endpoints["anthropic"] != tc.wantEndpoint {
				t.Fatalf("stored defaults or override not preserved/repaired: %#v", config)
			}
			if response := requestConfigResponse(nil, parent); response.Protocols[0].BaseURL != parent.BaseURL {
				t.Fatalf("reading defaults changed the active address: %#v", response)
			}
		})
	}
}

func TestStoredNativeAnthropicArkDefaultsRepairOpenAIAddress(t *testing.T) {
	for _, tc := range []struct{ name, openai, anthropic string }{
		{"ordinary", "https://ark.cn-beijing.volces.com/api/v3/", "https://ark.cn-beijing.volces.com/api/compatible"},
		{"coding", "https://ark.cn-beijing.volces.com/api/coding/v3/", "https://ark.cn-beijing.volces.com/api/coding"},
	} {
		for _, override := range []string{"", tc.anthropic + "/v1/", "https://explicit.example/tenant/v1/", "https://ark.cn-beijing.volces.com/private-proxy/v1/"} {
			t.Run(tc.name+"/"+override, func(t *testing.T) {
				oldOpenAI := tc.anthropic + "/v1/"
				stored := savedProviderRequestConfig{DefaultsVersion: 1, DefaultProtocol: "anthropic", Endpoints: map[string]string{"openai": override}, Defaults: map[string]string{"openai": oldOpenAI, "anthropic": tc.anthropic, "openai-responses": oldOpenAI}}
				encoded, _ := json.Marshal(stored)
				parent := orm.UserModelProvider{Name: "Doubao", ProtocolName: "Claude", BaseURL: "https://explicit-anthropic.example/tenant", RequestConfig: string(encoded)}
				config := savedRequestConfig(nil, parent)
				wantOverride := override
				if override == oldOpenAI {
					wantOverride = ""
				}
				if config.Defaults["openai"] != tc.openai || config.Defaults["anthropic"] != tc.anthropic || config.Defaults["openai-responses"] != tc.openai || config.Endpoints["openai"] != wantOverride {
					t.Fatalf("native Anthropic defaults were not repaired safely: %#v", config)
				}
			})
		}
	}
}

func TestStoredNativeAnthropicArkSwitchesBackToCorrectOpenAIRoot(t *testing.T) {
	for _, tc := range []struct{ name, openai, anthropic string }{
		{"ordinary", "https://ark.cn-beijing.volces.com/api/v3/", "https://ark.cn-beijing.volces.com/api/compatible"},
		{"coding", "https://ark.cn-beijing.volces.com/api/coding/v3/", "https://ark.cn-beijing.volces.com/api/coding"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db, parent, group := setupEncryptedGroupKeyTest(t, "saved-key")
			t.Cleanup(func() { store.Init(nil, nil, nil) })
			parent.Name, parent.ProtocolName, parent.BaseURL = "Doubao", "Claude", tc.anthropic
			stored := savedProviderRequestConfig{DefaultsVersion: 1, DefaultProtocol: "anthropic", Endpoints: map[string]string{"openai": tc.anthropic + "/v1/"}, Defaults: map[string]string{"openai": tc.anthropic + "/v1/", "anthropic": tc.anthropic}}
			encoded, _ := json.Marshal(stored)
			if err := db.Model(&parent).Updates(map[string]any{"name": parent.Name, "protocol_name": parent.ProtocolName, "base_url": parent.BaseURL, "request_config": string(encoded)}).Error; err != nil {
				t.Fatal(err)
			}
			if err := db.Model(&group).Update("base_url", parent.BaseURL).Error; err != nil {
				t.Fatal(err)
			}
			rec := httptest.NewRecorder()
			SetProviderRequestConfig(rec, requestConfigRequest(http.MethodPut, parent.ID, "user-1", `{"default_protocol":"openai","endpoints":{"openai":""}}`))
			if rec.Code != http.StatusOK {
				t.Fatalf("switch = %d %s", rec.Code, rec.Body.String())
			}
			if err := db.Take(&parent, "id = ?", parent.ID).Error; err != nil {
				t.Fatal(err)
			}
			if err := db.Take(&group, "id = ?", group.ID).Error; err != nil {
				t.Fatal(err)
			}
			if parent.BaseURL != tc.openai || group.BaseURL != tc.openai || RuntimeProviderName(parent) != "OpenAI" || connectionBaseURL(parent, group.BaseURL) != tc.openai {
				t.Fatalf("switch did not activate correct OpenAI root: parent=%q group=%q runtime=%q", parent.BaseURL, group.BaseURL, connectionBaseURL(parent, group.BaseURL))
			}
		})
	}
}

func TestArkRequestProtocolRoundTripPreservesRuntimeAddress(t *testing.T) {
	for _, tc := range []struct{ name, openai, anthropic string }{
		{"ordinary", "https://ark.cn-beijing.volces.com/api/v3/", "https://ark.cn-beijing.volces.com/api/compatible"},
		{"coding", "https://ark.cn-beijing.volces.com/api/coding/v3/", "https://ark.cn-beijing.volces.com/api/coding"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db, parent, group := setupEncryptedGroupKeyTest(t, "saved-key")
			t.Cleanup(func() { store.Init(nil, nil, nil) })
			if err := db.AutoMigrate(&orm.UserModelProviderGroupModel{}); err != nil {
				t.Fatal(err)
			}
			parent.Name, parent.BaseURL = "Doubao", tc.openai
			if err := db.Model(&parent).Updates(map[string]any{"name": parent.Name, "base_url": parent.BaseURL}).Error; err != nil {
				t.Fatal(err)
			}
			if err := db.Model(&group).Update("base_url", parent.BaseURL).Error; err != nil {
				t.Fatal(err)
			}
			model := orm.UserModelProviderGroupModel{ID: "persistent-chat-model", UserModelProviderID: parent.ID, UserModelProviderGroupID: group.ID, ProviderName: "Doubao", Name: "test-chat", ModelType: "llm", BaseModel: group.BaseModel}
			if err := db.Create(&model).Error; err != nil {
				t.Fatal(err)
			}
			for _, target := range []struct{ protocol, adapter, endpoint string }{{"anthropic", "Claude", tc.anthropic}, {"openai", "OpenAI", tc.openai}} {
				body, _ := json.Marshal(updateProviderRequestConfigRequest{DefaultProtocol: target.protocol, Endpoints: map[string]string{target.protocol: ""}})
				rec := httptest.NewRecorder()
				SetProviderRequestConfig(rec, requestConfigRequest(http.MethodPut, parent.ID, "user-1", string(body)))
				if rec.Code != http.StatusOK {
					t.Fatalf("switch to %s: %d %s", target.protocol, rec.Code, rec.Body.String())
				}
				if err := db.Take(&parent, "id = ?", parent.ID).Error; err != nil {
					t.Fatal(err)
				}
				if err := db.Take(&group, "id = ?", group.ID).Error; err != nil {
					t.Fatal(err)
				}
				if err := db.Take(&model, "id = ?", model.ID).Error; err != nil {
					t.Fatal(err)
				}
				if parent.Name != "Doubao" || RuntimeProviderName(parent) != target.adapter || model.ProviderName != target.adapter || parent.BaseURL != target.endpoint || group.BaseURL != target.endpoint || connectionBaseURL(parent, group.BaseURL) != target.endpoint {
					t.Fatalf("switch %s lost supplier/model/runtime identity: adapter=%q parent=%q group=%q runtime=%q model=%q", target.protocol, RuntimeProviderName(parent), parent.BaseURL, group.BaseURL, connectionBaseURL(parent, group.BaseURL), model.ProviderName)
				}
				assertStoredEncryptedAPIKeys(t, db, group.ID, "saved-key", false)
			}
		})
	}
}

func TestArkDefaultReadsAndMetadataSavesKeepVerifiedKey(t *testing.T) {
	db, parent, group := setupEncryptedGroupKeyTest(t, "saved-key")
	t.Cleanup(func() { store.Init(nil, nil, nil) })
	parent.Name, parent.BaseURL = "Doubao", "https://ark.cn-beijing.volces.com/api/v3/"
	if err := db.Model(&parent).Updates(map[string]any{"name": parent.Name, "base_url": parent.BaseURL}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&group).Update("base_url", parent.BaseURL).Error; err != nil {
		t.Fatal(err)
	}
	for index, body := range []string{"", `{"default_protocol":"openai","endpoints":{"openai":"https://ark.cn-beijing.volces.com/api/v3/"}}`, `{"default_protocol":"openai","endpoints":{"anthropic":"https://explicit.example/anthropic"}}`} {
		rec := httptest.NewRecorder()
		if body == "" {
			GetProviderRequestConfig(rec, requestConfigRequest(http.MethodGet, parent.ID, "user-1", ""))
		} else {
			SetProviderRequestConfig(rec, requestConfigRequest(http.MethodPut, parent.ID, "user-1", body))
		}
		if rec.Code != http.StatusOK {
			t.Fatalf("request = %d %s", rec.Code, rec.Body.String())
		}
		var current orm.UserModelProviderGroup
		if err := db.Take(&current, "id = ?", group.ID).Error; err != nil {
			t.Fatal(err)
		}
		if current.BaseURL != parent.BaseURL || index < 2 && current.CredentialRevision != group.CredentialRevision || !current.IsVerified {
			t.Fatalf("read/no-op/inactive save invalidated active key: %#v", current)
		}
		assertStoredEncryptedAPIKeys(t, db, group.ID, "saved-key", true)
	}
}

func TestStoredArkAnthropicDefaultReadDoesNotChangeActiveAddress(t *testing.T) {
	db, parent, group := setupEncryptedGroupKeyTest(t, "saved-key")
	t.Cleanup(func() { store.Init(nil, nil, nil) })
	parent.Name, parent.ProtocolName, parent.BaseURL = "Doubao", "Claude", "https://ark.cn-beijing.volces.com/api/v3/"
	parent.RequestConfig = `{"defaults_version":1,"default_protocol":"anthropic","endpoints":{"anthropic":"https://ark.cn-beijing.volces.com/api/v3"},"defaults":{"openai":"https://ark.cn-beijing.volces.com/api/v3/","anthropic":"https://ark.cn-beijing.volces.com/api/v3"}}`
	if err := db.Model(&parent).Updates(map[string]any{"name": parent.Name, "protocol_name": parent.ProtocolName, "base_url": parent.BaseURL, "request_config": parent.RequestConfig}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&group).Update("base_url", parent.BaseURL).Error; err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	GetProviderRequestConfig(rec, requestConfigRequest(http.MethodGet, parent.ID, "user-1", ""))
	var response struct {
		Data providerRequestConfigResponse `json:"data"`
	}
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &response) != nil {
		t.Fatalf("read = %d %s", rec.Code, rec.Body.String())
	}
	if response.Data.DefaultProtocol != "anthropic" || response.Data.Protocols[1].BaseURL != parent.BaseURL || response.Data.Protocols[1].DefaultBaseURL != "https://ark.cn-beijing.volces.com/api/compatible" {
		t.Fatalf("read did not distinguish active address from repaired default: %#v", response.Data)
	}
	var current orm.UserModelProvider
	if err := db.Take(&current, "id = ?", parent.ID).Error; err != nil {
		t.Fatal(err)
	}
	if current.BaseURL != parent.BaseURL || current.ProtocolName != parent.ProtocolName || current.RequestConfig != parent.RequestConfig {
		t.Fatal("GET changed the enabled protocol or persisted request configuration")
	}
	assertStoredEncryptedAPIKeys(t, db, group.ID, "saved-key", true)
}
