package mcpadapter

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/auth"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"lazymind/core/capability"
	"lazymind/core/capability/internal/scanadapter"
)

func TestMCPCloudReadEndToEndAndStructuredAuthorizationErrors(t *testing.T) {
	var disabled atomic.Bool
	var runtimeCalls atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-LazyMind-Internal-Token") != "internal" {
			t.Error("internal authentication missing")
		}
		switch r.URL.Path {
		case "/v1/cloud/connections/internal/connection":
			if r.URL.Query().Get("user_id") != "verified-user" {
				t.Error("owner not derived from principal")
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"data": map[string]any{"connection_id": "connection", "provider": "googledrive", "owner_user_id": "verified-user", "status": "ACTIVE", "provider_options": map[string]bool{"chat_enabled": !disabled.Load()}}})
		case "/v1/cloud/connections/connection/token":
			_, _ = io.WriteString(w, `{"data":{"connection_id":"connection","provider":"googledrive","status":"ACTIVE","access_token":"private-platform-token"}}`)
		case "/internal/documents:read":
			runtimeCalls.Add(1)
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			if body["user_id"] != "verified-user" || body["tenant_id"] != "" || body["provider"] != "googledrive" {
				t.Error("runtime identity incorrect")
			}
			if body["expected_version"] == "stale" {
				w.WriteHeader(409)
				_, _ = io.WriteString(w, `{"detail":{"code":"VERSION_CHANGED","message":"private-platform-token"}}`)
				return
			}
			next := 4
			_ = json.NewEncoder(w).Encode(capability.ReadCloudDocumentResult{SourceID: "connection", Provider: "googledrive", DocumentID: "doc", Title: "Plan", Content: "body", ContentFormat: "markdown", Version: "v1", NextOffset: &next, TotalChars: 8, Warnings: []string{}})
		default:
			t.Errorf("unexpected upstream request %s", r.URL.Path)
			w.WriteHeader(500)
		}
	}))
	defer upstream.Close()
	cloud, err := scanadapter.NewCloudDocumentReader(upstream.URL, upstream.URL, "internal", 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := cloud.SetRuntimeEndpoint(upstream.URL); err != nil {
		t.Fatal(err)
	}
	ports := &mcpFakePorts{}
	service, err := capability.NewService(capability.Dependencies{Skills: ports, Knowledge: ports, Documents: ports, Search: ports, Cloud: cloud, CloudContent: cloud, Vocabulary: ports})
	if err != nil {
		t.Fatal(err)
	}
	handler, err := NewHandler(service, HandlerConfig{Verifier: func(_ context.Context, token string, _ *http.Request) (*auth.TokenInfo, error) {
		if token != "valid" {
			return nil, auth.ErrInvalidToken
		}
		return &auth.TokenInfo{UserID: "verified-user", Scopes: []string{capability.RequiredPermission}}, nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(handler)
	defer server.Close()
	client := mcp.NewClient(&mcp.Implementation{Name: "cloud-test", Version: "1"}, nil)
	session, err := client.Connect(context.Background(), &mcp.StreamableClientTransport{Endpoint: server.URL, DisableStandaloneSSE: true, HTTPClient: &http.Client{Transport: bearerTransport{base: http.DefaultTransport, token: "valid"}}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	tools, err := session.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, tool := range tools.Tools {
		if tool.Name != "cloud_document.read" {
			continue
		}
		found = true
		schema, _ := json.Marshal(tool.InputSchema)
		if strings.Contains(string(schema), "tool_config") || strings.Contains(string(schema), "user_id") {
			t.Fatal("private input exposed in MCP")
		}
		if tool.OutputSchema == nil || !tool.Annotations.ReadOnlyHint {
			t.Fatal("missing read-only schema contract")
		}
	}
	if !found {
		t.Fatal("read tool not published")
	}
	args := map[string]any{"source_id": "connection", "locator": "googledrive:/doc", "limit": 4}
	first, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: "cloud_document.read", Arguments: args})
	if err != nil || first.IsError {
		t.Fatalf("first=%#v err=%v", first, err)
	}
	encoded, _ := json.Marshal(first.StructuredContent)
	if !strings.Contains(string(encoded), `"content":"body"`) || strings.Contains(string(encoded), "private-platform-token") {
		t.Fatalf("unsafe output=%s", encoded)
	}
	args["offset"], args["expected_version"] = 4, "stale"
	conflict, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: "cloud_document.read", Arguments: args})
	if err != nil || !conflict.IsError {
		t.Fatalf("conflict=%#v err=%v", conflict, err)
	}
	encoded, _ = json.Marshal(conflict.StructuredContent)
	if !strings.Contains(string(encoded), "restart_read") || strings.Contains(string(encoded), "private-platform-token") {
		t.Fatalf("lost structured error=%s", encoded)
	}
	disabled.Store(true)
	denied, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: "cloud_document.read", Arguments: args})
	if err != nil || !denied.IsError {
		t.Fatalf("denied=%#v err=%v", denied, err)
	}
	encoded, _ = json.Marshal(denied.StructuredContent)
	if !strings.Contains(string(encoded), "CONNECTION_DISABLED") || !strings.Contains(string(encoded), "connection_id=connection") || runtimeCalls.Load() != 2 {
		t.Fatalf("page was not reauthorized: %s calls=%d", encoded, runtimeCalls.Load())
	}
	args["user_id"] = "forged"
	_, _ = session.CallTool(context.Background(), &mcp.CallToolParams{Name: "cloud_document.read", Arguments: args})
	if runtimeCalls.Load() != 2 {
		t.Fatal("forged identity reached Runtime")
	}
}
