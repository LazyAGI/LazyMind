package modelprovider

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"regexp"
	"slices"
	"strings"
	"testing"

	"lazymind/core/common/orm"
	"lazymind/core/store"
)

func TestProviderProtocolDeclarationsCoverCatalogAndMatchFrontend(t *testing.T) {
	raw, err := os.ReadFile("../config/model_catalog.yaml")
	if err != nil {
		t.Fatal(err)
	}
	catalog, err := loadModelCatalog(raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(providerProtocolDeclarations) != len(catalog["model_providers"].Suppliers) {
		t.Fatal("protocol declarations must cover every catalog service")
	}
	frontend, err := os.ReadFile("../../../frontend/src/modules/modelProvider/providerProtocols.ts")
	if err != nil {
		t.Fatal(err)
	}
	blocks := regexp.MustCompile(`(?s)  "([^"]+)": \{\s+protocols: \[([^\]]+)\]`).FindAllStringSubmatch(string(frontend), -1)
	byIdentity := map[string][]string{}
	for _, block := range blocks {
		var protocols []string
		if err := json.Unmarshal([]byte("["+block[2]+"]"), &protocols); err != nil {
			t.Fatal(err)
		}
		for i, protocol := range protocols {
			if protocol == "openai-chat" {
				protocols[i] = "openai"
			}
		}
		byIdentity[block[1]] = protocols
	}
	for _, supplier := range catalog["model_providers"].Suppliers {
		declaration, ok := providerProtocolDeclarations[supplier.Name]
		if !ok {
			t.Errorf("missing %s", supplier.Name)
			continue
		}
		brand, service := ProviderServiceIdentity(orm.UserModelProvider{Name: supplier.Name})
		if brand == "" {
			brand, service = supplier.Name, "standard"
		}
		if !slices.Equal(declaration.Protocols, byIdentity[brand+":"+service]) {
			t.Errorf("%s frontend/backend protocols diverged", supplier.Name)
		}
		if len(declaration.Sources) == 0 {
			t.Errorf("%s missing official sources", supplier.Name)
		}
		for _, source := range declaration.Sources {
			parsed, err := url.Parse(source)
			if err != nil || parsed.Scheme != "https" || parsed.Host == "" {
				t.Errorf("invalid source %s", source)
			}
		}
	}
}

func TestProviderProtocolDeclarationsIgnoreMutableRuntimeIdentity(t *testing.T) {
	for name, declaration := range providerProtocolDeclarations {
		for _, adapter := range []string{"OpenAI", "Claude", "Gemini", "unknown"} {
			provider := orm.UserModelProvider{Name: name, ProtocolName: adapter, BaseURL: "https://gateway.example/"}
			for _, protocol := range []string{"openai", "openai-responses", "anthropic", "gemini", "native"} {
				if got := providerDeclaresProtocol(provider, protocol); got != slices.Contains(declaration.Protocols, protocol) {
					t.Errorf("%s/%s depends on saved adapter", name, protocol)
				}
			}
			provider.IsCustom = true
			for _, protocol := range declaration.Protocols {
				if providerDeclaresProtocol(provider, protocol) {
					t.Errorf("custom %s inherited declaration", name)
				}
			}
		}
	}
	for _, name := range []string{"", "Unknown", "My OpenAI", "constructor", "__proto__"} {
		provider := orm.UserModelProvider{Name: name, ProtocolName: "OpenAI"}
		for _, protocol := range []string{"openai", "anthropic"} {
			if serviceSupportsRequestProtocol(provider, protocol) {
				t.Errorf("unknown %q guessed support", name)
			}
		}
	}
}

func TestRequestProtocolSeparatesOfficialDeclarationAndRuntime(t *testing.T) {
	for _, tc := range []struct {
		name, protocol                  string
		declared, runtime, configurable bool
	}{
		{"SenseNovaTokenPlan", "anthropic", true, false, false},
		{"SenseNovaTokenPlan", "openai", true, true, true},
		{"DeepSeek", "openai-responses", true, false, false},
		{"QwenCodingPlan", "openai-responses", false, false, false},
		{"OpenAI", "anthropic", false, true, false},
		{"GLM", "anthropic", false, true, false},
		{"LMDeploy", "anthropic", true, true, true},
		{"Unknown", "anthropic", false, true, false},
	} {
		t.Run(tc.name+"/"+tc.protocol, func(t *testing.T) {
			provider := orm.UserModelProvider{Name: tc.name, BaseURL: "https://configured.example/v1/"}
			if serviceSupportsRequestProtocol(provider, tc.protocol) != tc.configurable {
				t.Fatal("wrong configurable result")
			}
			for _, result := range requestConfigResponse(nil, provider).Protocols {
				if result.Protocol != tc.protocol {
					continue
				}
				if result.ProviderSupported == nil || *result.ProviderSupported != tc.declared || result.RuntimeSupported != tc.runtime || result.Supported != tc.configurable {
					t.Fatalf("incorrect separation: %#v", result)
				}
			}
		})
	}
	for name := range providerProtocolDeclarations {
		provider := orm.UserModelProvider{Name: name, IsCustom: true, BaseURL: "https://custom.example/v1/"}
		for _, result := range requestConfigResponse(nil, provider).Protocols {
			if result.ProviderSupported != nil {
				t.Fatalf("custom %s inherits vendor claim", name)
			}
			if result.Supported != (result.Protocol == "openai" || result.Protocol == "anthropic") {
				t.Fatalf("custom configurable protocols changed: %#v", result)
			}
		}
		encoded, err := json.Marshal(requestConfigResponse(nil, provider))
		if err != nil || strings.Contains(string(encoded), "provider_supported") {
			t.Fatal("custom declarations must be omitted from JSON")
		}
	}
}

func TestRequestProtocolUpdateRejectsUndeclaredAndUnimplementedSeparately(t *testing.T) {
	for _, tc := range []struct{ name, message string }{
		{"OpenAI", "This request protocol is not declared for the supplier service"},
		{"SenseNovaTokenPlan", "The current runtime does not support this request protocol"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db, parent, _ := setupEncryptedGroupKeyTest(t, "saved-key")
			t.Cleanup(func() { store.Init(nil, nil, nil) })
			if err := db.Model(&parent).Update("name", tc.name).Error; err != nil {
				t.Fatal(err)
			}
			rec := httptest.NewRecorder()
			SetProviderRequestConfig(rec, requestConfigRequest(http.MethodPut, parent.ID, "user-1", `{"default_protocol":"anthropic"}`))
			if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), tc.message) {
				t.Fatalf("incorrect rejection: %d %s", rec.Code, rec.Body.String())
			}
		})
	}
}
