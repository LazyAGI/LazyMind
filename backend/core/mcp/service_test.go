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
	db := orm.MigrateTestDB(t, &orm.MCPServer{}, &orm.MCPServerTool{})
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

// TestNewServerID generates IDs with the msp_ prefix.
func TestNewServerID(t *testing.T) {
	id := newServerID()
	if !strings.HasPrefix(id, "msp_") {
		t.Fatalf("got %q, want prefix msp_", id)
	}
	id2 := newServerID()
	if id == id2 {
		t.Fatal("expected different IDs")
	}
}

// TestNewToolID generates IDs with the mst_ prefix.
func TestNewToolID(t *testing.T) {
	id := newToolID()
	if !strings.HasPrefix(id, "mst_") {
		t.Fatalf("got %q, want prefix mst_", id)
	}
}

// TestNormalizeStringList deduplicates and trims whitespace.
func TestNormalizeStringList(t *testing.T) {
	tests := []struct {
		name  string
		input []string
		want  []string
	}{
		{"no_dups", []string{"a", "b"}, []string{"a", "b"}},
		{"with_dups", []string{"a", "b", "a"}, []string{"a", "b"}},
		{"with_empty", []string{"", " a ", "b", ""}, []string{"a", "b"}},
		{"nil", nil, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := normalizeStringList(tt.input)
			if len(got) != len(tt.want) {
				t.Fatalf("len: got %d, want %d: %v", len(got), len(tt.want), got)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Fatalf("[%d]: got %q, want %q", i, got[i], tt.want[i])
				}
			}
		})
	}
}

// TestNormalizedTimeout returns defaultTimeoutSeconds (5) for <= 0.
func TestNormalizedTimeout(t *testing.T) {
	if got := normalizedTimeout(0); got != defaultTimeoutSeconds {
		t.Fatalf("0 got %d, want %d", got, defaultTimeoutSeconds)
	}
	if got := normalizedTimeout(-5); got != defaultTimeoutSeconds {
		t.Fatalf("-5 got %d, want %d", got, defaultTimeoutSeconds)
	}
	if got := normalizedTimeout(30); got != 30 {
		t.Fatalf("30 got %d, want 30", got)
	}
}

// TestMaskAPIKey masks different lengths of API keys.
func TestMaskAPIKey(t *testing.T) {
	// Short key (<= 6 runes)
	if got := maskAPIKey("ab"); got != "a-***" {
		t.Fatalf("short got %q, want a-***", got)
	}

	// Medium key (7-10 runes)
	if got := maskAPIKey("1234567"); got != "12***67" {
		t.Fatalf("medium got %q, want 12***67", got)
	}

	// Long key (> 10 runes)
	masked := maskAPIKey("1234567890abcdef")
	if !strings.Contains(masked, "-***") {
		t.Fatal("missing -*** in masked key")
	}

	// Empty key
	if got := maskAPIKey(""); got != "" {
		t.Fatalf("empty got %q, want empty", got)
	}
}

// TestValidateHTTPURL normalizes and validates HTTP/HTTPS URLs.
func TestValidateHTTPURL(t *testing.T) {
	// Valid http
	got, err := validateHTTPURL("http://example.com")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "http://example.com" {
		t.Fatalf("got %q", got)
	}

	// Whitespace trimmed
	got2, err := validateHTTPURL("  https://example.com  ")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got2 != "https://example.com" {
		t.Fatalf("got %q", got2)
	}

	// Empty
	_, err = validateHTTPURL("")
	if err == nil {
		t.Fatal("expected error for empty URL")
	}

	// Non-HTTP scheme
	_, err = validateHTTPURL("ftp://example.com")
	if err == nil {
		t.Fatal("expected error for non-HTTP scheme")
	}
}

// TestApiKeyPreview decodes headers JSON and returns masked Bearer token.
func TestApiKeyPreview(t *testing.T) {
	// Valid Authorization header
	headers := `{"Authorization":"Bearer sk-test-key-12345"}`
	if got := apiKeyPreview(json.RawMessage(headers)); got == "" {
		t.Fatal("should return non-empty preview for valid auth")
	}

	// No Authorization header
	headersNoAuth := `{"X-Custom":"value"}`
	if got := apiKeyPreview(json.RawMessage(headersNoAuth)); got != "" {
		t.Fatalf("got %q, want empty", got)
	}

	// Invalid JSON
	if got := apiKeyPreview(json.RawMessage("bad json")); got != "" {
		t.Fatalf("invalid got %q, want empty", got)
	}

	// Nil
	if got := apiKeyPreview(nil); got != "" {
		t.Fatalf("nil got %q, want empty", got)
	}
}

// TestParseStringJSON parses a JSON array of strings.
func TestParseStringJSON(t *testing.T) {
	// Valid array
	got := parseStringJSON(json.RawMessage(`["a","b","c"]`))
	if len(got) != 3 || got[0] != "a" {
		t.Fatalf("got %v", got)
	}

	// Empty array
	if got := parseStringJSON(json.RawMessage(`[]`)); len(got) != 0 {
		t.Fatalf("empty got %v", got)
	}

	// Nil
	if got := parseStringJSON(nil); len(got) != 0 {
		t.Fatalf("nil got %v", got)
	}

	// Invalid JSON
	if got := parseStringJSON(json.RawMessage("not json")); len(got) != 0 {
		t.Fatalf("invalid got %v", got)
	}
}

// TestSanitizeError cleans up error messages with raw JSON headers.
func TestSanitizeError(t *testing.T) {
	got := sanitizeError("test message", json.RawMessage(`{"key":"value"}`))
	if got != "test message" {
		t.Fatalf("got %q, want test message", got)
	}
	if got := sanitizeError("", nil); got != "" {
		t.Fatalf("empty got %q, want empty", got)
	}
}

// TestDedupeServers removes duplicate servers by ID, keeping first occurrence.
func TestDedupeServers(t *testing.T) {
	rows := []orm.MCPServer{
		{ID: "a", Name: "first"},
		{ID: "b", Name: "second"},
		{ID: "a", Name: "duplicate"},
		{ID: "c", Name: "third"},
	}
	deduped := dedupeServers(rows)
	if len(deduped) != 3 {
		t.Fatalf("got %d servers, want 3", len(deduped))
	}
	if deduped[0].Name != "first" {
		t.Fatalf("first occurrence should be kept, got %q", deduped[0].Name)
	}

	// Nil input returns empty slice
	if got := dedupeServers(nil); len(got) != 0 {
		t.Fatalf("nil got %v, want empty", got)
	}
}

// TestEscapeListServersLikePattern escapes special LIKE wildcards ! % _.
func TestEscapeListServersLikePattern(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"hello", "hello"},
		{"100%", "100!%"},
		{"under_score", "under!_score"},
		{"bang!", "bang!!"},
		{"mix_%!", "mix!_!%!!"},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			if got := escapeListServersLikePattern(tt.input); got != tt.want {
				t.Fatalf("got %q, want %q", got, tt.want)
			}
		})
	}
}

// TestNormalizeListServersRequest applies defaults for page and page_size.
func TestNormalizeListServersRequest(t *testing.T) {
	// Negative page → 1
	req := normalizeListServersRequest(ListServersRequest{Page: -1, PageSize: -1})
	if req.Page != 1 {
		t.Fatalf("page = %d, want 1", req.Page)
	}
	if req.PageSize != 0 {
		t.Fatalf("page_size = %d, want 0", req.PageSize)
	}

	// Zero page → 1
	req2 := normalizeListServersRequest(ListServersRequest{Page: 0, PageSize: 10})
	if req2.Page != 1 {
		t.Fatalf("page = %d, want 1", req2.Page)
	}

	// Valid values preserved
	req3 := normalizeListServersRequest(ListServersRequest{Keyword: "  test  ", Page: 2, PageSize: 20})
	if req3.Page != 2 || req3.PageSize != 20 {
		t.Fatalf("page=%d page_size=%d", req3.Page, req3.PageSize)
	}
	if req3.Keyword != "test" {
		t.Fatalf("keyword = %q, want test", req3.Keyword)
	}
}

// TestServerResponse maps ORM server row to response DTO.
func TestServerResponse(t *testing.T) {
	row := orm.MCPServer{
		ID:               "srv-1",
		Name:             "test-server",
		Transport:        "sse",
		URL:              "http://localhost:8080",
		HeadersJSON:      nil,
		AllowedToolsJSON: nil,
		Enabled:          true,
		IsVerified:       true,
		Share:            false,
		Timeout:          10,
	}
	tools := []ToolResponse{
		{ID: "t1", ToolName: "search", Description: "search docs"},
	}
	resp := serverResponse(row, 1, tools)
	if resp.ID != "srv-1" {
		t.Fatalf("ID = %q", resp.ID)
	}
	if resp.Name != "test-server" {
		t.Fatalf("name = %q", resp.Name)
	}
	if resp.Timeout != normalizedTimeout(10) {
		t.Fatalf("timeout = %d", resp.Timeout)
	}
	if len(resp.Tools) != 1 {
		t.Fatalf("tools = %d, want 1", len(resp.Tools))
	}
	if resp.ToolCount != 1 {
		t.Fatalf("tool_count = %d, want 1", resp.ToolCount)
	}
}

// TestToolResponse maps ORM tool row to response DTO, defaulting InputSchema to {}.
func TestToolResponse(t *testing.T) {
	// With input schema
	row := orm.MCPServerTool{
		ID:              "tool-1",
		ToolName:        "search",
		Description:     "search docs",
		InputSchemaJSON: json.RawMessage(`{"type":"object"}`),
	}
	resp := toolResponse(row)
	if resp.ToolName != "search" {
		t.Fatalf("tool_name = %q", resp.ToolName)
	}
	if string(resp.InputSchema) != `{"type":"object"}` {
		t.Fatalf("input_schema = %s", string(resp.InputSchema))
	}

	// Empty schema → {}
	row2 := orm.MCPServerTool{ID: "tool-2", ToolName: "empty"}
	resp2 := toolResponse(row2)
	if string(resp2.InputSchema) != `{}` {
		t.Fatalf("empty input_schema = %s", string(resp2.InputSchema))
	}
}
