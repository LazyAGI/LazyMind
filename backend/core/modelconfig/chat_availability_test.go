package modelconfig

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"

	"lazymind/core/providerconnection"
)

func TestCloudChatAvailabilityPreservesAlgorithmContract(t *testing.T) {
	previous := providerconnection.DefaultService()
	providerconnection.SetDefaultService(nil)
	t.Cleanup(func() { providerconnection.SetDefaultService(previous) })
	var enabled, failed atomic.Bool
	enabled.Store(true)
	var tokenRequests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
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
		if strings.HasSuffix(r.URL.Path, "/fixture-connection/token") && r.URL.Query().Get("user_id") == "fixture-owner" {
			tokenRequests.Add(1)
			_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{
				"connection_id": "fixture-connection", "provider": "feishu", "status": "ACTIVE", "access_token": "fixture-token",
			}})
			return
		}
		t.Errorf("unexpected request: %s", r.URL.Path)
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()
	t.Setenv("LAZYMIND_AUTH_SERVICE_URL", server.URL)

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
}
