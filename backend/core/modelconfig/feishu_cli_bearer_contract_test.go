package modelconfig

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"lazymind/core/providerconnection"
)

type cliToolCredentialBackend struct {
	unavailableFeishuBackend
	capabilities []string
}

func (b *cliToolCredentialBackend) UserAccessToken(_ context.Context, owner, id, profile, capability string) (providerconnection.ResolvedToken, error) {
	b.capabilities = append(b.capabilities, capability)
	if owner != "fixture-owner" || id != "fixture-connection" || profile != "fixture-profile" {
		return providerconnection.ResolvedToken{}, providerconnection.ErrCLIProfileOwnerMismatch
	}
	return providerconnection.ResolvedToken{AuthConnectionID: id, Provider: "feishu", TokenType: "Bearer", SubjectType: "user", Status: "ACTIVE", AccessToken: "fixture-cli-user-access"}, nil
}

func TestCLIAuthenticationPreservesAllExistingToolConfigConsumers(t *testing.T) {
	previous := providerconnection.DefaultService()
	t.Cleanup(func() { providerconnection.SetDefaultService(previous) })
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/authservice/v1/cloud/connections/internal/chat-enabled" || r.Header.Get("X-LazyMind-Internal-Token") != "fixture-internal" || r.URL.Query().Get("owner_user_id") != "fixture-owner" {
			t.Error("CLI consumer bypassed the unified bridge")
			w.WriteHeader(403)
			return
		}
		items := []map[string]any{}
		if r.URL.Query().Get("provider") == "feishu" {
			items = append(items, map[string]any{"connection_id": "fixture-connection", "provider": "feishu", "owner_user_id": "fixture-owner", "status": "ACTIVE"})
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"items": items}})
	}))
	defer server.Close()
	t.Setenv("LAZYMIND_AUTH_SERVICE_URL", server.URL)
	t.Setenv("LAZYMIND_AUTH_SERVICE_INTERNAL_TOKEN", "fixture-internal")
	registry := &writerResolutionRegistry{metas: map[string]providerconnection.ConnectionMeta{
		"fixture-connection": {AuthConnectionID: "fixture-connection", OwnerUserID: "fixture-owner", Provider: "feishu", ConnectionMethod: "cli_personal_app", CredentialLocation: "local", ProfileRef: "fixture-profile", Status: "ACTIVE", ProviderOptions: map[string]any{"chat_enabled": true}},
	}}
	backend := &cliToolCredentialBackend{}
	bridge, err := providerconnection.NewLocalService(registry, &unavailableSourceAuthorizer{}, "fixture-client-instance")
	if err != nil {
		t.Fatal(err)
	}
	bridge.FeishuCLI = backend
	providerconnection.SetDefaultService(bridge)
	for _, loader := range []struct {
		name, capability string
		load             func() (map[string]any, error)
	}{
		{"chat", "chat.search", func() (map[string]any, error) { return LoadCloudToolConfig(t.Context(), "fixture-owner") }},
		{"workflow and subagent", "chat.search", func() (map[string]any, error) {
			return LoadToolConfigForCapabilities(t.Context(), nil, "fixture-owner", []string{"feishu"})
		}},
		{"Writer publication", "chat.write", func() (map[string]any, error) {
			return LoadWriterProviderToolConfig(t.Context(), "feishu", "fixture-owner")
		}},
	} {
		t.Run(loader.name, func(t *testing.T) {
			before := len(backend.capabilities)
			got, err := loader.load()
			if err != nil || !reflect.DeepEqual(got, map[string]any{"feishu": "fixture-cli-user-access"}) {
				t.Fatal("algorithm tool_config changed or contains an opaque CLI handle")
			}
			if len(backend.capabilities) != before+1 || backend.capabilities[before] != loader.capability {
				t.Fatal("consumer did not request the correct credential capability")
			}
		})
	}
	if registry.legacyCalls != 0 || backend.calls.Load() != 0 {
		t.Fatal("CLI credential loading called legacy auth or CLI document execution")
	}
}
