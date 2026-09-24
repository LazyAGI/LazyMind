package modelconfig

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	"lazymind/core/cloudclient"
	"lazymind/core/providerconnection"
)

func TestCloudChatAvailabilityPreservesAlgorithmContract(t *testing.T) {
	testCloudChatAvailabilityPreservesAlgorithmContract(t, false)
}

func TestLegacyFeishuCredentialsWithUnavailableCloudAndRegisteredBridge(t *testing.T) {
	testCloudChatAvailabilityPreservesAlgorithmContract(t, true)
}

type unavailableFeishuBackend struct{ calls atomic.Int32 }

func (backend *unavailableFeishuBackend) Start(context.Context, string, string) (cloudclient.ProviderConnectionSession, error) {
	backend.calls.Add(1)
	return cloudclient.ProviderConnectionSession{}, errors.New("fixture CLI unavailable")
}
func (backend *unavailableFeishuBackend) Get(context.Context, string, string) (cloudclient.ProviderConnectionSession, error) {
	backend.calls.Add(1)
	return cloudclient.ProviderConnectionSession{}, errors.New("fixture CLI unavailable")
}
func (backend *unavailableFeishuBackend) Cancel(context.Context, string, string) error {
	backend.calls.Add(1)
	return errors.New("fixture CLI unavailable")
}
func (backend *unavailableFeishuBackend) Execute(context.Context, string, string, string, string, map[string]string) (providerconnection.FeishuCLIExecutionResult, error) {
	backend.calls.Add(1)
	return providerconnection.FeishuCLIExecutionResult{}, errors.New("fixture CLI unavailable")
}

type unavailableSourceAuthorizer struct{ calls atomic.Int32 }

func (authorizer *unavailableSourceAuthorizer) Authorize(context.Context, string, string, string, string, string) error {
	authorizer.calls.Add(1)
	return errors.New("fixture source service unavailable")
}

func testCloudChatAvailabilityPreservesAlgorithmContract(t *testing.T, registeredBridge bool) {
	t.Helper()
	previous := providerconnection.DefaultService()
	providerconnection.SetDefaultService(nil)
	t.Cleanup(func() { providerconnection.SetDefaultService(previous) })
	var enabled, failed atomic.Bool
	enabled.Store(true)
	var tokenRequests atomic.Int32
	var metadataRequests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Header.Get("X-LazyMind-Internal-Token") != "fixture-internal-token" {
			t.Error("missing internal authentication")
			w.WriteHeader(http.StatusForbidden)
			return
		}
		if strings.HasSuffix(r.URL.Path, "/internal/chat-enabled") {
			if r.URL.Query().Get("owner_user_id") != "fixture-owner" {
				t.Error("missing owner scope")
				w.WriteHeader(http.StatusForbidden)
				return
			}
			if failed.Load() {
				w.WriteHeader(http.StatusServiceUnavailable)
				return
			}
			items := []map[string]any{}
			if enabled.Load() && r.URL.Query().Get("provider") == "feishu" {
				items = append(items, map[string]any{
					"connection_id": "fixture-connection", "provider": "feishu", "owner_user_id": "fixture-owner",
					"status": "ACTIVE", "can_use_chat": true,
				})
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"items": items}})
			return
		}
		if strings.HasSuffix(r.URL.Path, "/internal/fixture-connection") && r.URL.Query().Get("user_id") == "fixture-owner" {
			metadataRequests.Add(1)
			_ = json.NewEncoder(w).Encode(map[string]any{"code": 200, "data": map[string]any{
				"connection_id": "fixture-connection", "provider": "feishu", "owner_user_id": "fixture-owner",
				"connection_method": "legacy_byo", "credential_location": "local", "status": "ACTIVE",
			}})
			return
		}
		if strings.HasSuffix(r.URL.Path, "/fixture-connection/token") && r.URL.Query().Get("user_id") == "fixture-owner" {
			tokenRequests.Add(1)
			_ = json.NewEncoder(w).Encode(map[string]any{"code": 200, "data": map[string]any{
				"connection_id": "fixture-connection", "provider": "feishu", "status": "ACTIVE", "access_token": "fixture-token",
			}})
			return
		}
		t.Errorf("unexpected request: %s", r.URL.Path)
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()
	t.Setenv("LAZYMIND_AUTH_SERVICE_URL", server.URL)
	t.Setenv("LAZYMIND_AUTH_SERVICE_INTERNAL_TOKEN", "fixture-internal-token")
	cli := &unavailableFeishuBackend{}
	authorizer := &unavailableSourceAuthorizer{}
	if registeredBridge {
		bridge, err := providerconnection.NewLocalService(providerconnection.HTTPRegistry{
			BaseURL: server.URL, InternalToken: "fixture-internal-token", HTTPClient: server.Client(),
		}, authorizer, "fixture-instance-identifier")
		if err != nil {
			t.Fatal(err)
		}
		// No Cloud client or Cloud session exists; CLI is registered but unavailable.
		bridge.FeishuCLI = cli
		providerconnection.SetDefaultService(bridge)
	}

	loaders := []struct {
		name string
		load func() (map[string]any, error)
	}{
		{"chat", func() (map[string]any, error) { return LoadCloudToolConfig(t.Context(), "fixture-owner") }},
		{"workflow and subagent", func() (map[string]any, error) {
			return LoadToolConfigForCapabilities(t.Context(), nil, "fixture-owner", []string{"feishu"})
		}},
		{"writer", func() (map[string]any, error) {
			return LoadWriterProviderToolConfig(t.Context(), "feishu", "fixture-owner")
		}},
	}
	for _, loader := range loaders {
		t.Run(loader.name, func(t *testing.T) {
			enabled.Store(true)
			failed.Store(false)
			config, err := loader.load()
			if err != nil || !reflect.DeepEqual(config, map[string]any{"feishu": "fixture-token"}) {
				t.Fatalf("algorithm contract changed: config=%v, err=%v", config, err)
			}
			enabled.Store(false)
			before := tokenRequests.Load()
			config, err = loader.load()
			if len(config) != 0 || tokenRequests.Load() != before {
				t.Fatal("disabled connection still supplies credentials")
			}
			if loader.name == "writer" && err == nil {
				t.Fatal("writer accepted a publish with no eligible account")
			}
			if loader.name != "writer" && err != nil {
				t.Fatal(err)
			}
			failed.Store(true)
			if _, err := loader.load(); err == nil {
				t.Fatal("failed availability lookup silently became an empty account list")
			}
		})
	}
	if registeredBridge && metadataRequests.Load() == 0 {
		t.Fatal("registered Bridge connection routing was not exercised")
	}
	if tokenRequests.Load() != int32(len(loaders)) || cli.calls.Load() != 0 || authorizer.calls.Load() != 0 {
		t.Fatalf("legacy credentials depended on managed services: tokens=%d cli=%d source=%d", tokenRequests.Load(), cli.calls.Load(), authorizer.calls.Load())
	}
}
