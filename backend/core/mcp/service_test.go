package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"lazymind/core/common/orm"
	appLog "lazymind/core/log"
)

func newTestDB(t *testing.T) *orm.DB {
	t.Helper()
	db, err := orm.Connect(orm.DriverSQLite, t.TempDir()+"/mcp.db")
	if err != nil {
		t.Fatalf("connect db: %v", err)
	}
	if err := db.AutoMigrate(&orm.MCPServer{}, &orm.MCPServerTool{}); err != nil {
		t.Fatalf("auto migrate: %v", err)
	}
	return db
}

func TestDoRPCNon2xxHidesResponseBodyFromError(t *testing.T) {
	appLog.InitNop()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"trace_id":"trace-1","error":{"code":"bad_request","message":"model is required"}}`))
	}))
	defer server.Close()

	_, _, err := doRPC(context.Background(), server.Client(), server.URL, nil, jsonRPCRequest{
		JSONRPC: "2.0",
		ID:      1,
		Method:  "initialize",
	})
	if err == nil {
		t.Fatalf("expected rpc error")
	}
	if got, want := err.Error(), "mcp rpc returned 400"; got != want {
		t.Fatalf("unexpected error: got %q want %q", got, want)
	}
	if strings.Contains(err.Error(), "model is required") || strings.Contains(err.Error(), "trace-1") {
		t.Fatalf("error leaked response body: %q", err.Error())
	}
}

func TestCreateServerMasksAndEncryptsAPIKey(t *testing.T) {
	db := newTestDB(t)
	resp, err := CreateServer(context.Background(), db.DB, CreateServerRequest{
		Name:         "context7",
		Transport:    "sse",
		URL:          "https://mcp.example.com/sse",
		APIKey:       "sk-secret-xyz",
		AllowedTools: []string{"get-library-docs", "resolve-library-id"},
	}, "u1", "User 1")
	if err != nil {
		t.Fatalf("create server: %v", err)
	}
	if resp.APIKeyPreview != "sk-***xyz" {
		t.Fatalf("unexpected api key preview: %q", resp.APIKeyPreview)
	}
	if resp.Enabled {
		t.Fatalf("new server should start disabled")
	}

	var row orm.MCPServer
	if err := db.First(&row, "id = ?", resp.ID).Error; err != nil {
		t.Fatalf("query row: %v", err)
	}
	if strings.Contains(string(row.HeadersJSON), "sk-secret-xyz") || strings.Contains(string(row.HeadersJSON), "Authorization") {
		t.Fatalf("headers_json leaked credential material: %s", row.HeadersJSON)
	}

	if err := db.Model(&orm.MCPServer{}).
		Where("id = ?", resp.ID).
		Updates(map[string]any{"is_verified": true, "enabled": true}).Error; err != nil {
		t.Fatalf("enable verified server: %v", err)
	}

	runtime, err := LoadRuntimeConfig(context.Background(), db.DB, "u1")
	if err != nil {
		t.Fatalf("load runtime config: %v", err)
	}
	if len(runtime) != 1 {
		t.Fatalf("expected one runtime config, got %d", len(runtime))
	}
	if got := runtime[0].Headers["Authorization"]; got != "Bearer sk-secret-xyz" {
		t.Fatalf("unexpected runtime authorization header: %#v", got)
	}
	if len(runtime[0].AllowedTools) != 2 || runtime[0].AllowedTools[0] != "get-library-docs" {
		t.Fatalf("unexpected allowed tools: %#v", runtime[0].AllowedTools)
	}
}

func TestCreateServerIgnoresEnabledAndStartsDisabled(t *testing.T) {
	db := newTestDB(t)
	enabled := true
	resp, err := CreateServer(context.Background(), db.DB, CreateServerRequest{
		Name:      "context7",
		Transport: "sse",
		URL:       "https://mcp.example.com/sse",
		Enabled:   &enabled,
	}, "u1", "User 1")
	if err != nil {
		t.Fatalf("create server: %v", err)
	}
	if resp.Enabled {
		t.Fatalf("new server should ignore requested enabled=true and start disabled")
	}

	var row orm.MCPServer
	if err := db.First(&row, "id = ?", resp.ID).Error; err != nil {
		t.Fatalf("query row: %v", err)
	}
	if row.Enabled {
		t.Fatalf("stored server should be disabled")
	}

	runtime, err := LoadRuntimeConfig(context.Background(), db.DB, "u1")
	if err != nil {
		t.Fatalf("load runtime config: %v", err)
	}
	if len(runtime) != 0 {
		t.Fatalf("disabled new server should not load into runtime config: %#v", runtime)
	}
}

func TestListServersFiltersByKeywordAndReturnsAll(t *testing.T) {
	db := newTestDB(t)
	for _, item := range []struct {
		name string
		url  string
	}{
		{name: "网站检索", url: "https://search.example.com/mcp"},
		{name: "Alpha API", url: "https://alpha.example.com/mcp"},
		{name: "网站分析", url: "https://analytics.example.com/mcp"},
	} {
		if _, err := CreateServer(context.Background(), db.DB, CreateServerRequest{
			Name:      item.name,
			Transport: "http",
			URL:       item.url,
		}, "u1", "User 1"); err != nil {
			t.Fatalf("create server %q: %v", item.name, err)
		}
	}

	resp, err := ListServers(context.Background(), db.DB, "u1", ListServersRequest{
		Keyword:  "网站",
		Page:     2,
		PageSize: 1,
	})
	if err != nil {
		t.Fatalf("list servers: %v", err)
	}
	if resp.Total != 2 || resp.Page != 1 || resp.PageSize != 2 {
		t.Fatalf("unexpected list metadata: %#v", resp)
	}
	if len(resp.MCPServers) != 2 {
		t.Fatalf("expected all matching servers, got %#v", resp.MCPServers)
	}
	if !strings.Contains(resp.MCPServers[0].Name, "网站") || !strings.Contains(resp.MCPServers[1].Name, "网站") {
		t.Fatalf("unexpected filtered servers: %#v", resp.MCPServers)
	}
}

func TestListServersOrdersEnabledFirstThenCreatedDesc(t *testing.T) {
	db := newTestDB(t)
	base := time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC)
	rows := []orm.MCPServer{
		{
			ID:               "disabled-new",
			Name:             "disabled-new",
			Transport:        "http",
			URL:              "https://disabled-new.example.com/mcp",
			HeadersJSON:      json.RawMessage(`{}`),
			AllowedToolsJSON: json.RawMessage(`[]`),
			Enabled:          false,
			BaseModel: orm.BaseModel{
				CreateUserID:   "u1",
				CreateUserName: "User 1",
				CreatedAt:      base.Add(3 * time.Hour),
				UpdatedAt:      base.Add(3 * time.Hour),
			},
		},
		{
			ID:               "enabled-old",
			Name:             "enabled-old",
			Transport:        "http",
			URL:              "https://enabled-old.example.com/mcp",
			HeadersJSON:      json.RawMessage(`{}`),
			AllowedToolsJSON: json.RawMessage(`[]`),
			Enabled:          true,
			BaseModel: orm.BaseModel{
				CreateUserID:   "u1",
				CreateUserName: "User 1",
				CreatedAt:      base.Add(time.Hour),
				UpdatedAt:      base.Add(time.Hour),
			},
		},
		{
			ID:               "enabled-new",
			Name:             "enabled-new",
			Transport:        "http",
			URL:              "https://enabled-new.example.com/mcp",
			HeadersJSON:      json.RawMessage(`{}`),
			AllowedToolsJSON: json.RawMessage(`[]`),
			Enabled:          true,
			BaseModel: orm.BaseModel{
				CreateUserID:   "u1",
				CreateUserName: "User 1",
				CreatedAt:      base.Add(2 * time.Hour),
				UpdatedAt:      base.Add(2 * time.Hour),
			},
		},
	}
	if err := db.Create(&rows).Error; err != nil {
		t.Fatalf("create servers: %v", err)
	}

	resp, err := ListServers(context.Background(), db.DB, "u1", ListServersRequest{Page: 2, PageSize: 1})
	if err != nil {
		t.Fatalf("list servers: %v", err)
	}
	if resp.Total != 3 || resp.Page != 1 || resp.PageSize != 3 {
		t.Fatalf("unexpected list metadata: %#v", resp)
	}
	got := []string{resp.MCPServers[0].ID, resp.MCPServers[1].ID, resp.MCPServers[2].ID}
	want := []string{"enabled-new", "enabled-old", "disabled-new"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("unexpected order: got %#v want %#v", got, want)
		}
	}
}

func TestListServersKeywordMatchesURLCaseInsensitively(t *testing.T) {
	db := newTestDB(t)
	if _, err := CreateServer(context.Background(), db.DB, CreateServerRequest{
		Name:      "docs",
		Transport: "http",
		URL:       "https://Website.example.com/mcp",
	}, "u1", "User 1"); err != nil {
		t.Fatalf("create matching server: %v", err)
	}
	if _, err := CreateServer(context.Background(), db.DB, CreateServerRequest{
		Name:      "alpha",
		Transport: "http",
		URL:       "https://alpha.example.com/mcp",
	}, "u1", "User 1"); err != nil {
		t.Fatalf("create non-matching server: %v", err)
	}

	resp, err := ListServers(context.Background(), db.DB, "u1", ListServersRequest{
		Keyword: "WEBSITE",
	})
	if err != nil {
		t.Fatalf("list servers: %v", err)
	}
	if resp.Total != 1 || len(resp.MCPServers) != 1 || resp.MCPServers[0].Name != "docs" {
		t.Fatalf("unexpected keyword response: %#v", resp)
	}
}

func TestUpdateServerRequiresVerificationBeforeEnabling(t *testing.T) {
	db := newTestDB(t)
	created, err := CreateServer(context.Background(), db.DB, CreateServerRequest{
		Name:      "context7",
		Transport: "sse",
		URL:       "https://mcp.example.com/sse",
	}, "u1", "User 1")
	if err != nil {
		t.Fatalf("create server: %v", err)
	}

	enabled := true
	if _, err := UpdateServer(context.Background(), db.DB, "u1", created.ID, UpdateServerRequest{
		Enabled: &enabled,
	}); !errors.Is(err, errBadRequest) {
		t.Fatalf("expected bad request enabling unverified server, got %v", err)
	}

	var row orm.MCPServer
	if err := db.First(&row, "id = ?", created.ID).Error; err != nil {
		t.Fatalf("query row: %v", err)
	}
	if row.Enabled {
		t.Fatalf("unverified server should remain disabled")
	}

	if err := db.Model(&orm.MCPServer{}).
		Where("id = ?", created.ID).
		Update("is_verified", true).Error; err != nil {
		t.Fatalf("mark server verified: %v", err)
	}
	updated, err := UpdateServer(context.Background(), db.DB, "u1", created.ID, UpdateServerRequest{
		Enabled: &enabled,
	})
	if err != nil {
		t.Fatalf("enable verified server: %v", err)
	}
	if !updated.Enabled {
		t.Fatalf("verified server should be enabled")
	}
}

func TestCheckServerMarksVerifiedOnSuccess(t *testing.T) {
	db := newTestDB(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req jsonRPCRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode rpc request: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		switch req.Method {
		case "initialize":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"jsonrpc": "2.0",
				"id":      req.ID,
				"result": map[string]any{
					"protocolVersion": "2024-11-05",
					"capabilities":    map[string]any{},
					"serverInfo":      map[string]any{"name": "test", "version": "1"},
				},
			})
		case "notifications/initialized":
			_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "result": map[string]any{}})
		case "tools/list":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"jsonrpc": "2.0",
				"id":      req.ID,
				"result": map[string]any{
					"tools": []map[string]any{{
						"name":        "search",
						"description": "search docs",
						"inputSchema": map[string]any{"type": "object"},
					}},
				},
			})
		default:
			t.Fatalf("unexpected rpc method: %s", req.Method)
		}
	}))
	defer server.Close()

	created, err := CreateServer(context.Background(), db.DB, CreateServerRequest{
		Name:      "local",
		Transport: "http",
		URL:       server.URL,
		Timeout:   2,
	}, "u1", "User 1")
	if err != nil {
		t.Fatalf("create server: %v", err)
	}

	var before orm.MCPServer
	if err := db.First(&before, "id = ?", created.ID).Error; err != nil {
		t.Fatalf("query created server: %v", err)
	}
	if before.IsVerified {
		t.Fatalf("new server should start unverified")
	}

	resp, err := CheckServer(context.Background(), db.DB, "u1", created.ID)
	if err != nil {
		t.Fatalf("check server: %v", err)
	}
	if !resp.Success || resp.ToolCount != 1 {
		t.Fatalf("unexpected check response: %#v", resp)
	}

	var row orm.MCPServer
	if err := db.First(&row, "id = ?", created.ID).Error; err != nil {
		t.Fatalf("query checked server: %v", err)
	}
	if !row.IsVerified {
		t.Fatalf("expected successful check to mark server verified")
	}
}

func TestDiscoverReplacesToolsAndSoftDeletesMissing(t *testing.T) {
	db := newTestDB(t)
	now := time.Now()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req jsonRPCRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Fatalf("decode rpc request: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		switch req.Method {
		case "initialize":
			w.Header().Set("Mcp-Session-Id", "session-1")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"jsonrpc": "2.0",
				"id":      req.ID,
				"result": map[string]any{
					"protocolVersion": "2024-11-05",
					"capabilities":    map[string]any{},
					"serverInfo":      map[string]any{"name": "test", "version": "1"},
				},
			})
		case "notifications/initialized":
			if r.Header.Get("Mcp-Session-Id") != "session-1" {
				t.Fatalf("initialized notification missing session header: %q", r.Header.Get("Mcp-Session-Id"))
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "result": map[string]any{}})
		case "tools/list":
			if r.Header.Get("Mcp-Session-Id") != "session-1" {
				t.Fatalf("tools/list missing session header: %q", r.Header.Get("Mcp-Session-Id"))
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"jsonrpc": "2.0",
				"id":      req.ID,
				"result": map[string]any{
					"tools": []map[string]any{{
						"name":        "new-tool",
						"description": "new description",
						"inputSchema": map[string]any{"type": "object"},
					}},
				},
			})
		default:
			t.Fatalf("unexpected rpc method: %s", req.Method)
		}
	}))
	defer server.Close()

	created, err := CreateServer(context.Background(), db.DB, CreateServerRequest{
		Name:      "local",
		Transport: "http",
		URL:       server.URL,
		Timeout:   2,
	}, "u1", "User 1")
	if err != nil {
		t.Fatalf("create server: %v", err)
	}
	oldTool := orm.MCPServerTool{
		ID:               "mst_old",
		MCPServerID:      created.ID,
		ToolName:         "old-tool",
		Description:      "old",
		InputSchemaJSON:  json.RawMessage(`{}`),
		LastDiscoveredAt: now,
		CreatedAt:        now,
		UpdatedAt:        now,
	}
	if err := db.Create(&oldTool).Error; err != nil {
		t.Fatalf("seed old tool: %v", err)
	}

	resp, err := DiscoverServer(context.Background(), db.DB, "u1", created.ID)
	if err != nil {
		t.Fatalf("discover server: %v", err)
	}
	if !resp.Success || len(resp.Tools) != 1 || resp.Tools[0].ToolName != "new-tool" {
		t.Fatalf("unexpected discover response: %#v", resp)
	}

	var oldRow orm.MCPServerTool
	if err := db.First(&oldRow, "id = ?", "mst_old").Error; err != nil {
		t.Fatalf("query old tool: %v", err)
	}
	if oldRow.DeletedAt == nil {
		t.Fatalf("expected missing old tool to be soft deleted")
	}

	detail, err := GetServer(context.Background(), db.DB, "u1", created.ID)
	if err != nil {
		t.Fatalf("get server: %v", err)
	}
	if !detail.IsVerified || len(detail.Tools) != 1 || detail.Tools[0].ToolName != "new-tool" {
		t.Fatalf("unexpected server detail: %#v", detail)
	}
}
