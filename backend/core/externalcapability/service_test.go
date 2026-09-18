package externalcapability

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	"lazymind/core/capability"
	"lazymind/core/common/orm"
)

func TestDefaultCapabilityGrantFailsClosedOnReadError(t *testing.T) {
	s := New(testDB(t), nil)
	ctx := context.Background()
	if !s.hasGrant(ctx, "user-1", "codex", CapabilityTool, "builtin:calculator") {
		t.Fatal("tools should default to allowed")
	}
	if !s.hasGrant(ctx, "user-1", "codex", CapabilityModel, "model-1") {
		t.Fatal("models should default to allowed")
	}
	if s.hasGrant(ctx, "", "codex", CapabilityTool, "builtin:calculator") {
		t.Fatal("missing user must be denied")
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if s.hasGrant(canceled, "user-1", "codex", CapabilityTool, "builtin:calculator") {
		t.Fatal("failed authorization read must deny default access")
	}
	if s.hasGrant(canceled, "user-1", "codex", CapabilityModel, "model-1") {
		t.Fatal("failed authorization read must deny model access")
	}
}

func TestDefaultModelGrantProxiesWithoutExposingCredentialAndAudits(t *testing.T) {
	const secret = "server-only-secret"
	modelServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" || r.Header.Get("Authorization") != "Bearer "+secret {
			t.Fatalf("unexpected model request path=%s authorization=%q", r.URL.Path, r.Header.Get("Authorization"))
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"chat-1","model":"internal","choices":[{"message":{"content":"你好"},"finish_reason":"stop"}],"usage":{"prompt_tokens":3,"completion_tokens":2,"total_tokens":5}}`))
	}))
	defer modelServer.Close()

	db := testDB(t)
	seedModel(t, db, modelServer.URL+"/v1", secret)
	service := New(db, modelServer.Client())
	inventory, err := service.Inventory(context.Background(), "user-1", "codex")
	if err != nil || len(inventory.Capabilities) != 1 || !inventory.Capabilities[0].Authorized {
		t.Fatalf("default inventory=%#v err=%v", inventory, err)
	}
	call := capability.InvocationContext{
		Principal:     capability.Principal{UserID: "user-1", Permissions: capability.NewPermissionSet(capability.RequiredPermission)},
		ExternalAgent: "codex", InvocationID: "inv-1",
	}
	listed, err := service.ListExternalModels(context.Background(), call)
	if err != nil || len(listed.Items) != 1 || listed.Items[0].ID != "model-1" {
		t.Fatalf("listed=%#v err=%v", listed, err)
	}
	result, err := service.InvokeExternalModel(context.Background(), call, capability.InvokeExternalModelInput{
		ModelID: "model-1", Messages: []capability.ExternalModelMessage{{Role: "user", Content: "你好"}},
	})
	if err != nil || result.Content != "你好" || result.Usage.TotalTokens != 5 {
		t.Fatalf("result=%#v err=%v", result, err)
	}
	var audit orm.ExternalCapabilityInvocation
	if err := db.Take(&audit).Error; err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(audit)
	if audit.Agent != "codex" || audit.InvocationID != "inv-1" || audit.Status != "succeeded" ||
		!strings.Contains(string(audit.ResultJSON), "你好") || strings.Contains(string(raw), secret) {
		t.Fatalf("unsafe or incomplete audit: %s", raw)
	}
	if err := service.SetGrant(context.Background(), "user-1", GrantUpdate{
		Agent: "codex", CapabilityType: CapabilityModel, CapabilityID: "model-1", Enabled: false,
	}); err != nil {
		t.Fatal(err)
	}
	_, err = service.InvokeExternalModel(context.Background(), call, capability.InvokeExternalModelInput{
		ModelID: "model-1", Messages: []capability.ExternalModelMessage{{Role: "user", Content: "你好"}},
	})
	if code, ok := capability.CodeOf(err); !ok || code != capability.PermissionDenied {
		t.Fatalf("revoked model call error=%v code=%q", err, code)
	}
	listed, err = service.ListExternalModels(context.Background(), call)
	if err != nil || len(listed.Items) != 0 {
		t.Fatalf("explicitly disabled model remained listed: %#v %v", listed, err)
	}
	otherAgent := call
	otherAgent.ExternalAgent = "cursor"
	listed, err = service.ListExternalModels(context.Background(), otherAgent)
	if err != nil || len(listed.Items) != 1 {
		t.Fatalf("opt-out affected another agent: %#v %v", listed, err)
	}
	otherAgent.Principal.UserID = "user-2"
	listed, err = service.ListExternalModels(context.Background(), otherAgent)
	if err != nil || len(listed.Items) != 0 {
		t.Fatalf("default grant exposed another user's model: %#v %v", listed, err)
	}
	history, err := service.InvocationHistory(context.Background(), "user-1", "codex", 50)
	if err != nil || history.Total != 2 || history.Summary.Succeeded != 1 || history.Summary.Failed != 1 ||
		history.Summary.ModelCalls != 2 || history.Summary.ToolCalls != 0 || len(history.Summary.Capabilities) != 1 ||
		history.Summary.Capabilities[0].CallCount != 2 || len(history.Invocations) != 2 {
		t.Fatalf("unexpected invocation history: %#v err=%v", history, err)
	}
	if err := service.SetGrant(context.Background(), "user-1", GrantUpdate{
		Agent: "codex", CapabilityType: CapabilityModel, CapabilityID: "model-1", Enabled: true,
	}); err != nil {
		t.Fatal(err)
	}

	if err := db.Model(&orm.UserModelProviderGroup{}).Where("id = ?", "group-1").Update("is_verified", false).Error; err != nil {
		t.Fatal(err)
	}
	listed, err = service.ListExternalModels(context.Background(), call)
	if err != nil || len(listed.Items) != 0 {
		t.Fatalf("unverified model remained visible: %#v err=%v", listed, err)
	}
	inventory, err = service.Inventory(context.Background(), "user-1", "codex")
	if err != nil || inventory.Capabilities[0].Available || !inventory.Capabilities[0].Authorized {
		t.Fatalf("unavailable model grant should remain visible and revocable: %#v err=%v", inventory, err)
	}
}

func TestExplicitToolGrantChecksServerStateAndProxiesStoredHeaders(t *testing.T) {
	const secret = "tool-secret"
	toolServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+secret {
			t.Fatalf("tool credential missing: %q", r.Header.Get("Authorization"))
		}
		var request struct {
			ID     int64          `json:"id"`
			Method string         `json:"method"`
			Params map[string]any `json:"params"`
		}
		_ = json.NewDecoder(r.Body).Decode(&request)
		result := any(map[string]any{})
		if request.Method == "initialize" {
			result = map[string]any{"protocolVersion": "2024-11-05", "capabilities": map[string]any{}, "serverInfo": map[string]any{"name": "test", "version": "1"}}
		}
		if request.Method == "tools/call" {
			result = map[string]any{"content": []map[string]any{{"type": "text", "text": "done"}}, "isError": false}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": request.ID, "result": result})
	}))
	defer toolServer.Close()

	db := testDB(t)
	headers, _ := json.Marshal(map[string]string{"Authorization": "Bearer " + secret})
	wrapper, _ := json.Marshal(map[string]string{"enc": "base64", "v": base64.StdEncoding.EncodeToString(headers)})
	now := time.Now().UTC()
	if err := db.Create(&orm.MCPServer{
		ID: "server-1", Name: "Tools", Transport: "http", URL: toolServer.URL,
		HeadersJSON: wrapper, AllowedToolsJSON: json.RawMessage(`["echo"]`), Enabled: true, IsVerified: true,
		BaseModel: orm.BaseModel{CreateUserID: "user-1", CreatedAt: now, UpdatedAt: now},
	}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&orm.MCPServerTool{
		ID: "tool-1", MCPServerID: "server-1", ToolName: "echo", InputSchemaJSON: json.RawMessage(`{"type":"object"}`),
		LastDiscoveredAt: now, CreatedAt: now, UpdatedAt: now,
	}).Error; err != nil {
		t.Fatal(err)
	}
	service := New(db, nil)
	if err := service.SetGrant(context.Background(), "user-1", GrantUpdate{Agent: "codex", CapabilityType: CapabilityTool, CapabilityID: "tool-1", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	call := capability.InvocationContext{Principal: capability.Principal{UserID: "user-1"}, ExternalAgent: "codex"}
	result, err := service.InvokeExternalTool(context.Background(), call, capability.InvokeExternalToolInput{ToolID: "tool-1", Arguments: map[string]any{"value": "x"}})
	encodedResult, _ := json.Marshal(result.Result)
	if err != nil || !strings.Contains(string(encodedResult), "done") {
		t.Fatalf("result=%s err=%v", encodedResult, err)
	}
	if err := db.Model(&orm.MCPServer{}).Where("id = ?", "server-1").Update("enabled", false).Error; err != nil {
		t.Fatal(err)
	}
	_, err = service.InvokeExternalTool(context.Background(), call, capability.InvokeExternalToolInput{ToolID: "tool-1"})
	if code, ok := capability.CodeOf(err); !ok || code != capability.Unavailable {
		t.Fatalf("disabled tool call error=%v code=%q", err, code)
	}
	listed, err := service.ListExternalTools(context.Background(), call)
	if err != nil || len(listed.Items) != 0 {
		t.Fatalf("disabled tool remained visible: %#v err=%v", listed, err)
	}
	inventory, err := service.Inventory(context.Background(), "user-1", "codex")
	if err != nil || inventory.Capabilities[0].Available || !inventory.Capabilities[0].Authorized {
		t.Fatalf("unavailable tool grant should remain visible and revocable: %#v err=%v", inventory, err)
	}
}

func TestBuiltinImageToolUsesSelectedVerifiedModelThroughLazyMind(t *testing.T) {
	t.Setenv("LAZYMIND_PUBLIC_BASE_URL", "https://lazy.example/api/core")
	const secret = "image-provider-secret"
	chatServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/chat/tools/external-catalog" {
			_, _ = w.Write([]byte(`{"items":[{"name":"image_generator","description":"Image generation","available":true,"input_schema":{"type":"object"}}]}`))
			return
		}
		if r.URL.Path != "/api/chat/tools/execute" {
			t.Fatalf("unexpected built-in tool path: %s", r.URL.Path)
		}
		var request struct {
			ToolName  string                    `json:"tool_name"`
			Arguments map[string]any            `json:"arguments"`
			LLMConfig map[string]map[string]any `json:"llm_config"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatal(err)
		}
		if request.ToolName != "image_generator" || request.Arguments["prompt"] != "orange cat" {
			t.Fatalf("unexpected request: %#v", request)
		}
		imageConfig := request.LLMConfig["image_editing"]
		if imageConfig["api_key"] != secret || imageConfig["model"] != "Kwai-Kolors/Kolors" {
			t.Fatalf("selected image model was not injected: %#v", imageConfig)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"tool_name":"image_generator","result":{"image_url":"/static-files/ai_generated/generated.png?sig=test","image_markdown":"![generated](/static-files/ai_generated/generated.png?sig=test)"}}`))
	}))
	defer chatServer.Close()
	db := testDB(t)
	t.Setenv("LAZYMIND_CHAT_SERVICE_URL", chatServer.URL)
	seedModel(t, db, "https://model.example/v1", secret)
	now := time.Now().UTC()
	if err := db.Create(&orm.UserModelProviderGroupModel{
		ID: "image-model-1", UserModelProviderID: "provider-1", UserModelProviderGroupID: "group-1",
		ProviderName: "SiliconFlow", Name: "Kwai-Kolors/Kolors", ModelType: "image_editing",
		BaseModel: orm.BaseModel{CreateUserID: "user-1", CreatedAt: now, UpdatedAt: now},
	}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&orm.UserSelectedModel{
		UserID: "user-1", ModelKey: "image_editing", UserModelProviderGroupModelID: "image-model-1",
		CreatedAt: now, UpdatedAt: now,
	}).Error; err != nil {
		t.Fatal(err)
	}

	service := New(db, chatServer.Client())
	inventory, err := service.Inventory(context.Background(), "user-1", "codex")
	if err != nil {
		t.Fatal(err)
	}
	var imageTool *CapabilityItem
	for i := range inventory.Capabilities {
		if inventory.Capabilities[i].ID == "builtin:image_generator" {
			imageTool = &inventory.Capabilities[i]
			break
		}
	}
	if imageTool == nil || !imageTool.Available || !imageTool.Authorized {
		t.Fatalf("unexpected image tool inventory: %#v", imageTool)
	}
	if err := service.SetGrant(context.Background(), "user-1", GrantUpdate{
		Agent: "codex", CapabilityType: CapabilityTool, CapabilityID: imageTool.ID, Enabled: true,
	}); err != nil {
		t.Fatal(err)
	}
	call := capability.InvocationContext{
		Principal: capability.Principal{UserID: "user-1"}, ExternalAgent: "codex", InvocationID: "image-invocation",
	}
	result, err := service.InvokeExternalTool(context.Background(), call, capability.InvokeExternalToolInput{
		ToolID: imageTool.ID, Arguments: map[string]any{"prompt": "orange cat"},
	})
	encodedResult, _ := json.Marshal(result.Result)
	if err != nil || !strings.Contains(string(encodedResult), "generated.png") || strings.Contains(string(encodedResult), secret) {
		t.Fatalf("result=%s err=%v", encodedResult, err)
	}
	if strings.Count(string(encodedResult), "https://lazy.example/api/core/static-files/ai_generated/generated.png?sig=test") != 2 {
		t.Fatalf("external image links must be absolute: %s", encodedResult)
	}
	var audit orm.ExternalCapabilityInvocation
	if err := db.Where("capability_id = ?", imageTool.ID).First(&audit).Error; err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(audit.ResultJSON), "sig=") || strings.Contains(string(audit.ResultJSON), "https://lazy.example") {
		t.Fatalf("audit should retain stable local references: %s", audit.ResultJSON)
	}
}

func TestExternalModelFailureDoesNotModifyLazyMindConnection(t *testing.T) {
	const secret = "server-only-secret"
	modelServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":{"message":"credential server-only-secret rejected"}}`))
	}))
	defer modelServer.Close()

	db := testDB(t)
	seedModel(t, db, modelServer.URL+"/v1", secret)
	service := New(db, modelServer.Client())
	if err := service.SetGrant(context.Background(), "user-1", GrantUpdate{
		Agent: "codex", CapabilityType: CapabilityModel, CapabilityID: "model-1", Enabled: true,
	}); err != nil {
		t.Fatal(err)
	}
	_, err := service.InvokeExternalModel(context.Background(), capability.InvocationContext{
		Principal: capability.Principal{UserID: "user-1"}, ExternalAgent: "codex",
	}, capability.InvokeExternalModelInput{
		ModelID: "model-1", Messages: []capability.ExternalModelMessage{{Role: "user", Content: "hello"}},
	})
	if code, ok := capability.CodeOf(err); !ok || code != capability.Unavailable || strings.Contains(err.Error(), secret) {
		t.Fatalf("unsafe or unexpected external error: %v", err)
	}
	var group orm.UserModelProviderGroup
	if err := db.Where("id = ?", "group-1").Take(&group).Error; err != nil {
		t.Fatal(err)
	}
	if !group.IsVerified {
		t.Fatal("external invocation failure modified the original model connection")
	}
}

func TestExternalToolFailureDoesNotModifyLazyMindConnection(t *testing.T) {
	const secret = "tool-secret"
	toolServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte("credential " + secret + " rejected"))
	}))
	defer toolServer.Close()

	db := testDB(t)
	now := time.Now().UTC()
	if err := db.Create(&orm.MCPServer{
		ID: "server-failing", Name: "Failing Tools", Transport: "http", URL: toolServer.URL,
		HeadersJSON: json.RawMessage(`{}`), AllowedToolsJSON: json.RawMessage(`["echo"]`), Enabled: true, IsVerified: true,
		BaseModel: orm.BaseModel{CreateUserID: "user-1", CreatedAt: now, UpdatedAt: now},
	}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&orm.MCPServerTool{
		ID: "tool-failing", MCPServerID: "server-failing", ToolName: "echo",
		InputSchemaJSON: json.RawMessage(`{"type":"object"}`), LastDiscoveredAt: now,
		CreatedAt: now, UpdatedAt: now,
	}).Error; err != nil {
		t.Fatal(err)
	}
	service := New(db, nil)
	if err := service.SetGrant(context.Background(), "user-1", GrantUpdate{
		Agent: "codex", CapabilityType: CapabilityTool, CapabilityID: "tool-failing", Enabled: true,
	}); err != nil {
		t.Fatal(err)
	}
	_, err := service.InvokeExternalTool(context.Background(), capability.InvocationContext{
		Principal: capability.Principal{UserID: "user-1"}, ExternalAgent: "codex",
	}, capability.InvokeExternalToolInput{ToolID: "tool-failing"})
	if code, ok := capability.CodeOf(err); !ok || code != capability.Unavailable || strings.Contains(err.Error(), secret) {
		t.Fatalf("unsafe or unexpected external error: %v", err)
	}
	var server orm.MCPServer
	if err := db.Where("id = ?", "server-failing").Take(&server).Error; err != nil {
		t.Fatal(err)
	}
	if !server.Enabled || !server.IsVerified {
		t.Fatal("external invocation failure modified the original MCP connection")
	}
}

func TestAuditResultPreviewRedactsCredentialsAndBoundsOutput(t *testing.T) {
	preview := auditResultPreview(map[string]any{
		"api_key": "top-secret",
		"nested": map[string]any{
			"Authorization": "Bearer abc.def.ghi",
			"content":       "credential=do-not-store",
		},
		"tokens": map[string]any{"total_tokens": 42},
	})
	encoded := string(preview)
	if strings.Contains(encoded, "top-secret") || strings.Contains(encoded, "abc.def.ghi") ||
		strings.Contains(encoded, "do-not-store") || !strings.Contains(encoded, "[redacted]") ||
		!strings.Contains(encoded, "total_tokens") {
		t.Fatalf("unexpected credential redaction: %s", encoded)
	}

	items := make([]string, maxAuditArrayItems)
	for index := range items {
		items[index] = strings.Repeat("狗", maxAuditStringRunes)
	}
	large := auditResultPreview(map[string]any{"items": items})
	if len(large) > maxAuditResultBytes || !strings.Contains(string(large), `"truncated":true`) {
		t.Fatalf("large result was not bounded: bytes=%d result=%s", len(large), large)
	}
}

func testDB(t *testing.T) *gorm.DB {
	t.Helper()
	catalog := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"items":[]}`))
	}))
	t.Cleanup(catalog.Close)
	t.Setenv("LAZYMIND_CHAT_SERVICE_URL", catalog.URL)
	db, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(
		&orm.UserModelProvider{}, &orm.UserModelProviderGroup{}, &orm.UserModelProviderGroupModel{},
		&orm.UserSelectedModel{}, &orm.UserSelectedProvider{}, &orm.UserDisabledTool{},
		&orm.MCPServer{}, &orm.MCPServerTool{}, &orm.ExternalCapabilityGrant{}, &orm.ExternalCapabilityInvocation{},
	); err != nil {
		t.Fatal(err)
	}
	return db
}

func seedModel(t *testing.T, db *gorm.DB, baseURL, secret string) {
	t.Helper()
	now := time.Now().UTC()
	if err := db.Create(&orm.UserModelProvider{
		ID: "provider-1", DefaultModelProviderID: "default-1", Name: "OpenAI compatible", BaseURL: baseURL,
		Capabilities: "has_models", BaseModel: orm.BaseModel{CreateUserID: "user-1", CreatedAt: now, UpdatedAt: now},
	}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&orm.UserModelProviderGroup{
		ID: "group-1", UserModelProviderID: "provider-1", Name: "Internal", BaseURL: baseURL, APIKey: secret, IsVerified: true,
		BaseModel: orm.BaseModel{CreateUserID: "user-1", CreatedAt: now, UpdatedAt: now},
	}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&orm.UserModelProviderGroupModel{
		ID: "model-1", UserModelProviderID: "provider-1", UserModelProviderGroupID: "group-1", ProviderName: "OpenAI compatible",
		Name: "Qwen/Qwen3.8-Flash-Next", ModelType: "llm", BaseModel: orm.BaseModel{CreateUserID: "user-1", CreatedAt: now, UpdatedAt: now},
	}).Error; err != nil {
		t.Fatal(err)
	}
}
