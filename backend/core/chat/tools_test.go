package chat

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/mux"
	"lazymind/core/common/orm"
	"lazymind/core/mcp"
	"lazymind/core/store"
)

func newToolsTestDB(t *testing.T) *orm.DB {
	t.Helper()
	db, err := orm.Connect(orm.DriverSQLite, t.TempDir()+"/tools.db")
	if err != nil {
		t.Fatalf("connect db: %v", err)
	}
	models := append(orm.AllModelsForDDL(), &orm.UserSelectedProvider{})
	if err := db.AutoMigrate(models...); err != nil {
		t.Fatalf("auto migrate: %v", err)
	}
	return db
}

func startChatToolsTestServer(t *testing.T, handler http.Handler) string {
	t.Helper()
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Skipf("listener unavailable in current test environment: %v", err)
	}
	server := &http.Server{Handler: handler}
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(func() { _ = server.Shutdown(context.Background()) })
	return fmt.Sprintf("http://%s", listener.Addr().String())
}

func seedRuntimeModelConfig(t *testing.T, db *orm.DB, userID string) {
	t.Helper()
	now := time.Now()
	provider := orm.UserModelProvider{
		ID:                     "provider-model",
		DefaultModelProviderID: "default-model",
		Name:                   "OpenAI",
		Category:               "model",
		BaseModel: orm.BaseModel{
			CreateUserID:   userID,
			CreateUserName: userID,
			CreatedAt:      now,
			UpdatedAt:      now,
		},
	}
	group := orm.UserModelProviderGroup{
		ID:                  "group-model",
		UserModelProviderID: provider.ID,
		Name:                "OpenAI",
		BaseURL:             "https://api.openai.test/v1",
		APIKey:              "sk-model",
		IsVerified:          true,
		BaseModel: orm.BaseModel{
			CreateUserID:   userID,
			CreateUserName: userID,
			CreatedAt:      now,
			UpdatedAt:      now,
		},
	}
	model := orm.UserModelProviderGroupModel{
		ID:                       "model-llm",
		UserModelProviderID:      provider.ID,
		UserModelProviderGroupID: group.ID,
		ProviderName:             "OpenAI",
		Name:                     "gpt-4o",
		ModelType:                "llm",
		BaseModel: orm.BaseModel{
			CreateUserID:   userID,
			CreateUserName: userID,
			CreatedAt:      now,
			UpdatedAt:      now,
		},
	}
	selected := orm.UserSelectedModel{
		UserID:                        userID,
		UserName:                      userID,
		ModelKey:                      "llm",
		UserModelProviderGroupModelID: model.ID,
		Share:                         false,
		CreatedAt:                     now,
		UpdatedAt:                     now,
	}
	if err := db.Create(&provider).Error; err != nil {
		t.Fatalf("create provider: %v", err)
	}
	if err := db.Create(&group).Error; err != nil {
		t.Fatalf("create group: %v", err)
	}
	if err := db.Create(&model).Error; err != nil {
		t.Fatalf("create model: %v", err)
	}
	if err := db.Create(&selected).Error; err != nil {
		t.Fatalf("create selected model: %v", err)
	}
}

func TestListToolsForwardsRuntimeConfigsAndMarksDisabled(t *testing.T) {
	db := newToolsTestDB(t)
	store.Init(db.DB, nil, nil)
	t.Cleanup(func() { store.Init(nil, nil, nil) })
	seedRuntimeModelConfig(t, db, "u1")
	seedSelectedSearchTool(t, db.DB, "u1", "Bing", "search-group", "bing-key", false)
	if err := disableToolForUser(context.Background(), db.DB, "u1", "User 1", "bing"); err != nil {
		t.Fatalf("disable tool: %v", err)
	}

	var upstreamBody map[string]any
	baseURL := startChatToolsTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/authservice/") {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"data": map[string]any{"items": []any{}},
			})
			return
		}
		if r.URL.Path != chatToolsPath {
			http.NotFound(w, r)
			return
		}
		if err := json.NewDecoder(r.Body).Decode(&upstreamBody); err != nil {
			t.Fatalf("decode upstream body: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"tool_groups": []map[string]any{
				{"name": "bing", "label": "Bing", "can_disable": true, "active": true},
				{"name": "skill", "label": "Skill", "can_disable": false, "active": true},
			},
		})
	}))
	t.Setenv("LAZYMIND_CHAT_SERVICE_URL", baseURL)
	t.Setenv("LAZYMIND_AUTH_SERVICE_URL", baseURL)

	req := httptest.NewRequest(http.MethodGet, "/api/core/tools", nil)
	req.Header.Set("X-User-Id", "u1")
	req.Header.Set("X-User-Name", "User 1")
	rec := httptest.NewRecorder()

	ListTools(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	llmConfig, _ := upstreamBody["llm_config"].(map[string]any)
	if llmConfig["llm"] == nil {
		t.Fatalf("expected llm_config forwarded, got %#v", upstreamBody["llm_config"])
	}
	toolConfig, _ := upstreamBody["tool_config"].(map[string]any)
	if toolConfig["bing"] != "bing-key" {
		t.Fatalf("expected bing tool_config, got %#v", upstreamBody["tool_config"])
	}
	if _, ok := upstreamBody["mcp_config"]; ok {
		t.Fatalf("list tools should not forward mcp_config, got %#v", upstreamBody["mcp_config"])
	}
	var resp struct {
		Data chatToolsResponse `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(resp.Data.ToolGroups) != 2 {
		t.Fatalf("expected 2 tool groups, got %#v", resp.Data.ToolGroups)
	}
	if resp.Data.ToolGroups[0]["name"] != "skill" || resp.Data.ToolGroups[0]["disabled"] != false {
		t.Fatalf("expected enabled skill first, got %#v", resp.Data.ToolGroups[0])
	}
	if resp.Data.ToolGroups[1]["name"] != "bing" || resp.Data.ToolGroups[1]["disabled"] != true {
		t.Fatalf("expected disabled bing last, got %#v", resp.Data.ToolGroups[1])
	}
}

func TestListToolsFiltersAndReturnsAll(t *testing.T) {
	db := newToolsTestDB(t)
	store.Init(db.DB, nil, nil)
	t.Cleanup(func() { store.Init(nil, nil, nil) })

	baseURL := startChatToolsTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/authservice/") {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"data": map[string]any{"items": []any{}},
			})
			return
		}
		if r.URL.Path != chatToolsPath {
			http.NotFound(w, r)
			return
		}
		groups := make([]map[string]any, 0, 12)
		for i := 1; i <= 12; i++ {
			description := "general tool"
			if i <= 5 {
				description = "Calendar scheduling helper"
			}
			name := fmt.Sprintf("tool-%02d", i)
			if i == 12 {
				name = "report-builder"
			}
			groups = append(groups, map[string]any{
				"name":        name,
				"label":       fmt.Sprintf("Tool %02d", i),
				"description": description,
				"can_disable": true,
				"active":      true,
			})
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"tool_groups": groups})
	}))
	t.Setenv("LAZYMIND_CHAT_SERVICE_URL", baseURL)
	t.Setenv("LAZYMIND_AUTH_SERVICE_URL", baseURL)

	req := httptest.NewRequest(http.MethodGet, "/api/core/tools", nil)
	req.Header.Set("X-User-Id", "u1")
	rec := httptest.NewRecorder()
	ListTools(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	var firstPage struct {
		Data chatToolsResponse `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &firstPage); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if firstPage.Data.Page != 1 || firstPage.Data.PageSize != 12 || firstPage.Data.Total != 12 {
		t.Fatalf("unexpected default page metadata: %#v", firstPage.Data)
	}
	if len(firstPage.Data.ToolGroups) != 12 {
		t.Fatalf("expected all tools, got %d", len(firstPage.Data.ToolGroups))
	}
	if firstPage.Data.ToolGroups[0]["name"] != "tool-01" || firstPage.Data.ToolGroups[11]["name"] != "report-builder" {
		t.Fatalf("unexpected default items: %#v", firstPage.Data.ToolGroups)
	}

	req = httptest.NewRequest(http.MethodGet, "/api/core/tools?keyword=calendar&page=2&page_size=2", nil)
	req.Header.Set("X-User-Id", "u1")
	rec = httptest.NewRecorder()
	ListTools(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	var filteredPage struct {
		Data chatToolsResponse `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &filteredPage); err != nil {
		t.Fatalf("decode filtered response: %v", err)
	}
	if filteredPage.Data.Page != 1 || filteredPage.Data.PageSize != 5 || filteredPage.Data.Total != 5 {
		t.Fatalf("unexpected filtered metadata: %#v", filteredPage.Data)
	}
	if len(filteredPage.Data.ToolGroups) != 5 {
		t.Fatalf("expected all filtered tools, got %#v", filteredPage.Data.ToolGroups)
	}
	if filteredPage.Data.ToolGroups[0]["name"] != "tool-01" || filteredPage.Data.ToolGroups[4]["name"] != "tool-05" {
		t.Fatalf("unexpected filtered items: %#v", filteredPage.Data.ToolGroups)
	}

	req = httptest.NewRequest(http.MethodGet, "/api/core/tools?keyword=REPORT&page_size=10", nil)
	req.Header.Set("X-User-Id", "u1")
	rec = httptest.NewRecorder()
	ListTools(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	var nameMatched struct {
		Data chatToolsResponse `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &nameMatched); err != nil {
		t.Fatalf("decode name matched response: %v", err)
	}
	if nameMatched.Data.Total != 1 || len(nameMatched.Data.ToolGroups) != 1 || nameMatched.Data.ToolGroups[0]["name"] != "report-builder" {
		t.Fatalf("expected keyword to match tool name case-insensitively, got %#v", nameMatched.Data)
	}
}

func TestDisableToolRejectsNonDisableableTool(t *testing.T) {
	db := newToolsTestDB(t)
	store.Init(db.DB, nil, nil)
	t.Cleanup(func() { store.Init(nil, nil, nil) })
	baseURL := startChatToolsTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/authservice/") {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"data": map[string]any{"items": []any{}},
			})
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"tool_groups": []map[string]any{
				{"name": "skill", "can_disable": false},
			},
		})
	}))
	t.Setenv("LAZYMIND_CHAT_SERVICE_URL", baseURL)
	t.Setenv("LAZYMIND_AUTH_SERVICE_URL", baseURL)

	req := httptest.NewRequest(http.MethodPost, "/api/core/tools/skill:disable", nil)
	req.Header.Set("X-User-Id", "u1")
	req = mux.SetURLVars(req, map[string]string{"tool_name": "skill"})
	rec := httptest.NewRecorder()

	DisableTool(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected status 400, got %d body=%s", rec.Code, rec.Body.String())
	}
	disabled, err := listDisabledToolNames(context.Background(), db.DB, "u1")
	if err != nil {
		t.Fatalf("list disabled tools: %v", err)
	}
	if len(disabled) != 0 {
		t.Fatalf("expected no disabled tools, got %#v", disabled)
	}
}

func TestChatConversationsMergesPersistedDisabledTools(t *testing.T) {
	db := newToolsTestDB(t)
	store.Init(db.DB, nil, nil)
	t.Cleanup(func() { store.Init(nil, nil, nil) })
	if err := disableToolForUser(context.Background(), db.DB, "u1", "User 1", "bing"); err != nil {
		t.Fatalf("disable tool: %v", err)
	}
	created, err := mcp.CreateServer(context.Background(), db.DB, mcp.CreateServerRequest{
		Name:         "context7",
		Transport:    "sse",
		URL:          "https://mcp.example.com/sse",
		APIKey:       "sk-secret-xyz",
		AllowedTools: []string{"resolve-library-id"},
	}, "u1", "User 1")
	if err != nil {
		t.Fatalf("create mcp server: %v", err)
	}
	if err := db.Model(&orm.MCPServer{}).
		Where("id = ?", created.ID).
		Updates(map[string]any{"is_verified": true, "enabled": true}).Error; err != nil {
		t.Fatalf("enable verified mcp server: %v", err)
	}

	var upstreamBody map[string]any
	baseURL := startChatToolsTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/chat" {
			http.NotFound(w, r)
			return
		}
		if err := json.NewDecoder(r.Body).Decode(&upstreamBody); err != nil {
			t.Fatalf("decode upstream body: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"code": 200,
			"msg":  "success",
			"data": map[string]any{
				"text":    "answer",
				"sources": []any{},
			},
		})
	}))
	t.Setenv("LAZYMIND_CHAT_SERVICE_URL", baseURL)
	t.Setenv("LAZYMIND_AUTH_SERVICE_URL", baseURL)

	req := httptest.NewRequest(
		http.MethodPost,
		"/api/core/conversations:chat",
		strings.NewReader(`{"query":"hello","stream":false}`),
	)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-User-Id", "u1")
	req.Header.Set("X-User-Name", "User 1")
	rec := httptest.NewRecorder()

	ChatConversations(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d body=%s", rec.Code, rec.Body.String())
	}
	rawDisabled, _ := upstreamBody["disabled_tools"].([]any)
	if len(rawDisabled) != 1 || rawDisabled[0] != "bing" {
		t.Fatalf("expected disabled_tools to include persisted tool, got %#v", upstreamBody["disabled_tools"])
	}
	rawMCPConfig, _ := upstreamBody["mcp_config"].([]any)
	if len(rawMCPConfig) != 1 {
		t.Fatalf("expected mcp_config to be forwarded for chat, got %#v", upstreamBody["mcp_config"])
	}
	firstMCPConfig, _ := rawMCPConfig[0].(map[string]any)
	headers, _ := firstMCPConfig["headers"].(map[string]any)
	if headers["Authorization"] != "Bearer sk-secret-xyz" {
		t.Fatalf("expected decrypted authorization header in mcp_config, got %#v", headers)
	}
}
