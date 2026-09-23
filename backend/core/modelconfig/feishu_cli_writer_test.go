package modelconfig

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"lazymind/core/providerconnection"
)

type writerCLIBackend struct{ unavailableFeishuBackend }

func (backend *writerCLIBackend) Execute(context.Context, string, string, string, string, map[string]string) (providerconnection.FeishuCLIExecutionResult, error) {
	backend.calls.Add(1)
	return providerconnection.FeishuCLIExecutionResult{Data: json.RawMessage(`{"document":{"document_id":"doc_fixture"}}`)}, nil
}

func TestFeishuWriterAndChatUseCLIHandlesWithoutExportingTokens(t *testing.T) {
	for _, mode := range []string{"read-write", "read-only", "legacy-with-unavailable-cli"} {
		t.Run(mode, func(t *testing.T) {
			scopes := "offline_access drive:drive:readonly wiki:space:retrieve wiki:node:read wiki:node:retrieve docx:document:readonly"
			if mode != "read-only" {
				scopes += " drive:drive wiki:wiki docx:document"
			}
			meta := providerconnection.ConnectionMeta{
				AuthConnectionID: "fixture-cli", OwnerUserID: "fixture-owner", Provider: "feishu",
				ConnectionMethod: "cli_personal_app", CredentialLocation: "local", ProfileRef: "fixture-profile",
				Status: "ACTIVE", Scope: scopes, ProviderOptions: map[string]any{"chat_enabled": true},
			}
			var rawCLITokenCalls, legacyTokenCalls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				if r.Header.Get("X-LazyMind-Internal-Token") != "fixture-internal-token" {
					t.Error("missing internal authentication")
					w.WriteHeader(403)
					return
				}
				switch strings.TrimPrefix(r.URL.Path, "/api/authservice") {
				case "/v1/cloud/connections/internal/chat-enabled":
					if r.URL.Query().Get("owner_user_id") != "fixture-owner" {
						t.Error("wrong owner")
						w.WriteHeader(403)
						return
					}
					items := []providerconnection.ConnectionMeta{}
					if r.URL.Query().Get("provider") == "feishu" {
						items = append(items, meta)
						if mode == "legacy-with-unavailable-cli" {
							items = append(items, providerconnection.ConnectionMeta{AuthConnectionID: "fixture-legacy", OwnerUserID: "fixture-owner", Provider: "feishu", ConnectionMethod: "legacy_byo", Status: "ACTIVE"})
						}
					}
					_ = json.NewEncoder(w).Encode(map[string]any{"code": 200, "data": map[string]any{"items": items}})
				case "/v1/cloud/connections/internal/fixture-cli":
					_ = json.NewEncoder(w).Encode(map[string]any{"code": 200, "data": meta})
				case "/v1/cloud/connections/internal/fixture-legacy":
					_ = json.NewEncoder(w).Encode(map[string]any{"code": 200, "data": providerconnection.ConnectionMeta{AuthConnectionID: "fixture-legacy", OwnerUserID: "fixture-owner", Provider: "feishu", ConnectionMethod: "legacy_byo", Status: "ACTIVE"}})
				case "/v1/cloud/connections/fixture-cli/token":
					rawCLITokenCalls.Add(1)
					w.WriteHeader(http.StatusForbidden)
				case "/v1/cloud/connections/fixture-legacy/token":
					if r.URL.Query().Get("user_id") != "fixture-owner" {
						t.Error("wrong token owner")
						w.WriteHeader(403)
						return
					}
					legacyTokenCalls.Add(1)
					_ = json.NewEncoder(w).Encode(map[string]any{"code": 200, "data": map[string]any{"connection_id": "fixture-legacy", "provider": "feishu", "status": "ACTIVE", "access_token": "fixture-original-token"}})
				default:
					t.Errorf("unexpected request: %s", r.URL.Path)
					w.WriteHeader(404)
				}
			}))
			defer server.Close()
			t.Setenv("LAZYMIND_AUTH_SERVICE_URL", server.URL)
			t.Setenv("LAZYMIND_AUTH_SERVICE_INTERNAL_TOKEN", "fixture-internal-token")
			authorizer := &unavailableSourceAuthorizer{}
			service, err := providerconnection.NewLocalService(providerconnection.HTTPRegistry{BaseURL: server.URL, InternalToken: "fixture-internal-token", HTTPClient: server.Client()}, authorizer, "fixture-instance-identifier")
			if err != nil {
				t.Fatal(err)
			}
			backend := &writerCLIBackend{}
			if mode != "legacy-with-unavailable-cli" {
				service.FeishuCLI = backend
			}
			previous := providerconnection.DefaultService()
			providerconnection.SetDefaultService(service)
			t.Cleanup(func() { providerconnection.SetDefaultService(previous) })
			writer, writerErr := LoadWriterProviderToolConfig(context.Background(), "feishu", "fixture-owner")
			chat, chatErr := LoadCloudProviderTokens(context.Background(), "feishu", "fixture-owner")
			if rawCLITokenCalls.Load() != 0 {
				t.Error("CLI credentials were requested from the raw token endpoint")
			}
			if chatErr != nil || len(chat) != 1 {
				t.Fatalf("chat authorization failed: %v %v", chat, chatErr)
			}
			if mode == "legacy-with-unavailable-cli" {
				if writerErr != nil || writer["feishu"] != "fixture-original-token" || chat[0] != "fixture-original-token" || legacyTokenCalls.Load() != 2 {
					t.Fatalf("unavailable CLI broke original OAuth: writer=%v err=%v chat=%v", writer, writerErr, chat)
				}
				return
			}
			if !strings.HasPrefix(chat[0], "lmc_fcli_") {
				t.Fatal("CLI chat received a raw token")
			}
			params := map[string]string{"method": "POST", "path": "/open-apis/docx/v1/documents", "query": "{}", "body": `{"title":"Fixture"}`}
			if mode == "read-only" {
				if writerErr == nil {
					t.Error("read-only CLI was accepted by Writer")
				}
				if _, err := service.ExecuteFeishuCLI(context.Background(), chat[0], "document_request", params); err == nil {
					t.Error("read-only chat handle wrote a document")
				}
				if backend.calls.Load() != 0 {
					t.Error("read-only connection reached CLI write")
				}
				return
			}
			writerHandle, ok := writer["feishu"].(string)
			if writerErr != nil || !ok || !strings.HasPrefix(writerHandle, "lmc_fcli_") {
				t.Fatalf("Writer did not get CLI handle: %v %v", writer, writerErr)
			}
			for _, handle := range []string{writerHandle, chat[0]} {
				if _, err := service.ExecuteFeishuCLI(context.Background(), handle, "document_request", params); err != nil {
					t.Fatalf("authorized document write rejected: %v", err)
				}
			}
			if backend.calls.Load() != 2 || authorizer.calls.Load() != 0 {
				t.Fatalf("incorrect CLI document routing: calls=%d source=%d", backend.calls.Load(), authorizer.calls.Load())
			}
		})
	}
}
