package coreapi

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"lazymind/agentconnector/internal/credentials"
)

func TestCloneRequestCarriesExternalLeaseContext(t *testing.T) {
	t.Setenv("LAZYMIND_EXTERNAL_REF", "run-1")
	t.Setenv("LAZYMIND_EXTERNAL_LEASE", "lease-1")
	t.Setenv("LAZYMIND_EXTERNAL_HOST", "host-1")
	t.Setenv("LAZYMIND_CONVERSATION_ID", "conversation-1")
	request := httptest.NewRequest("POST", "http://lazymind.test/api/core/test", bytes.NewBufferString(`{}`)).
		WithContext(WithInvocation(context.Background(), InvocationMetadata{
			ID: "inv-1", ClientName: "codex", ConnectorInstanceID: "connector-1",
		}))
	clone := cloneRequest(request, []byte(`{}`), "access-token")
	if clone.Header.Get("X-LazyMind-External-Ref") != "run-1" ||
		clone.Header.Get("X-LazyMind-External-Lease") != "lease-1" ||
		clone.Header.Get("X-LazyMind-External-Host") != "host-1" ||
		clone.Header.Get("X-LazyMind-Conversation-Id") != "conversation-1" ||
		clone.Header.Get("X-LazyMind-Invocation-Id") != "inv-1" {
		t.Fatalf("missing execution context headers: %#v", clone.Header)
	}
}

func TestCloneRequestKeepsStandaloneSourceOutsideManagedLeaseHeaders(t *testing.T) {
	request := httptest.NewRequest("POST", "http://lazymind.test/api/core/test", bytes.NewBufferString(`{}`)).
		WithContext(WithInvocation(context.Background(), InvocationMetadata{
			ID: "inv-standalone", ClientName: "codex", ConnectorInstanceID: "connector-1",
			ConversationID: "conversation-standalone", ExternalRef: "run-standalone",
		}))
	clone := cloneRequest(request, []byte(`{}`), "access-token")
	if clone.Header.Get("X-LazyMind-External-Ref") != "" ||
		clone.Header.Get("X-LazyMind-Conversation-Id") != "" {
		t.Fatalf("standalone source leaked into managed lease headers: %#v", clone.Header)
	}
	if clone.Header.Get("X-LazyMind-Invocation-Conversation-Id") != "conversation-standalone" {
		t.Fatalf("standalone invocation scope is missing: %#v", clone.Header)
	}
}

func TestDoJSONRecoversRejectedHostCredentialAndRetriesRegistration(t *testing.T) {
	var coreCalls atomic.Int32
	var refreshCalls atomic.Int32
	var bootstrapCalls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/api/core/external-chat/hosts/cursor/claim":
			coreCalls.Add(1)
			if request.Header.Get("Authorization") != "Bearer new-access" {
				writer.WriteHeader(http.StatusUnauthorized)
				_, _ = writer.Write([]byte(`{"message":"access token rejected"}`))
				return
			}
			writer.Header().Set("Content-Type", "application/json")
			_, _ = writer.Write([]byte(`{"ok":true,"result":{"run":null}}`))
		case "/api/authservice/auth/refresh":
			refreshCalls.Add(1)
			writer.WriteHeader(http.StatusUnauthorized)
			_, _ = writer.Write([]byte(`{"message":"refresh_token is invalid or expired"}`))
		case "/_local/admin-session":
			bootstrapCalls.Add(1)
			if request.URL.Query().Get("force") != "true" {
				writer.WriteHeader(http.StatusConflict)
				_, _ = writer.Write([]byte(`{"message":"force refresh required"}`))
				return
			}
			writer.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(writer).Encode(map[string]string{
				"token": "new-access", "refreshToken": "new-refresh", "username": "admin",
			})
		default:
			writer.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	store, err := credentials.NewStore(t.TempDir(), "")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Save(credentials.Credentials{
		ServerURL: server.URL, AccessToken: "old-access", RefreshToken: "old-refresh",
	}); err != nil {
		t.Fatal(err)
	}
	client, err := New(store)
	if err != nil {
		t.Fatal(err)
	}
	var response struct {
		Run any `json:"run"`
	}
	if err := client.DoJSON(context.Background(), http.MethodPost,
		"/external-chat/hosts/cursor/claim", map[string]any{"host_id": "host-1"}, &response); err != nil {
		t.Fatal(err)
	}
	if coreCalls.Load() != 2 || refreshCalls.Load() != 1 || bootstrapCalls.Load() != 1 {
		t.Fatalf("core=%d refresh=%d bootstrap=%d", coreCalls.Load(), refreshCalls.Load(), bootstrapCalls.Load())
	}
}
