package workflowtool

import (
	"encoding/json"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"

	"lazymind/core/common/orm"
	"lazymind/core/workflow/graphengine"
)

func TestGlobalToolGatewayUsesPersistedAgentAndNoModelConfiguration(t *testing.T) {
	var executions atomic.Int32
	service, input := workflowToolFixture(t, func(request *http.Request) (*http.Response, error) {
		var payload map[string]any
		if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		if config, _ := payload["llm_config"].(map[string]any); len(config) != 0 {
			t.Fatal("global deterministic tool loaded model configuration")
		}
		switch request.URL.Path {
		case "/api/chat/tools/external-catalog":
			return toolResponse(`{"items":[{"name":"url_fetch","available":true,"input_schema":{"type":"object"}}]}`), nil
		case "/api/chat/tools/execute":
			executions.Add(1)
			if payload["tool_name"] != "url_fetch" {
				t.Fatal("wrong global tool executed")
			}
			return toolResponse(`{"result":{"text":"fetched by the global tool"}}`), nil
		default:
			t.Fatalf("unexpected endpoint %s", request.URL.Path)
			return nil, nil
		}
	})
	if err := service.DB.AutoMigrate(&orm.MCPServer{}, &orm.MCPServerTool{}, &orm.ExternalCapabilityGrant{}, &orm.ExternalCapabilityInvocation{},
		&orm.UserSelectedProvider{}, &orm.UserModelProviderGroup{}, &orm.UserModelProvider{}); err != nil {
		t.Fatal(err)
	}
	if service.DB.Migrator().HasTable(&orm.UserSelectedModel{}) {
		t.Fatal("fixture unexpectedly has model configuration")
	}
	var revision orm.WorkflowRevision
	if err := service.DB.First(&revision, "id = ?", "pinned-revision").Error; err != nil {
		t.Fatal(err)
	}
	var graph graphengine.CompiledStateGraph
	if err := json.Unmarshal(revision.CompiledGraph, &graph); err != nil {
		t.Fatal(err)
	}
	node := graph.Nodes["write"]
	node.Execution.Tools = []graphengine.ExecutionTool{{Name: "builtin:url_fetch", Source: "global"}}
	graph.Nodes["write"] = node
	if err := service.DB.Model(&revision).Update("compiled_graph", graph.JSON()).Error; err != nil {
		t.Fatal(err)
	}
	if err := service.DB.Model(&orm.WorkflowSession{}).Where("id = ?", "run").Update("control_binding_json", `{"provider":"codex","driver_session_id":"bound-native-thread"}`).Error; err != nil {
		t.Fatal(err)
	}
	// An unrelated agent's opt-out must not override the persisted Codex binding.
	grant := orm.ExternalCapabilityGrant{ID: "cursor-disabled", OwnerUserID: "owner", Agent: "cursor", CapabilityType: "tool", CapabilityID: "builtin:url_fetch", Enabled: false}
	if err := service.DB.Create(&grant).Error; err != nil {
		t.Fatal(err)
	}
	listed, err := service.List(t.Context(), "owner", "run", "attempt", "lease")
	if err != nil || len(listed) != 1 || !listed[0].Available || listed[0].Name != "builtin:url_fetch" {
		t.Fatalf("global catalog failed: %+v %v", listed, err)
	}
	input.ToolName, input.Arguments = "builtin:url_fetch", map[string]any{"url": "https://example.com"}
	result, err := service.Call(t.Context(), "owner", "run", "attempt", input)
	if err != nil || result.Error != nil || executions.Load() != 1 {
		t.Fatalf("global call failed: %+v %v", result, err)
	}
	var audit orm.ExternalCapabilityInvocation
	if err := service.DB.First(&audit).Error; err != nil || audit.Agent != "codex" || audit.OwnerUserID != "owner" || audit.CapabilityType != "tool" || audit.Status != "succeeded" {
		t.Fatalf("global call used caller-controlled identity: %+v %v", audit, err)
	}
	grant.ID, grant.Agent = "codex-disabled", "codex"
	if err := service.DB.Create(&grant).Error; err != nil {
		t.Fatal(err)
	}
	input.CommandID = "new-call-after-disabled"
	result, err = service.Call(t.Context(), "owner", "run", "attempt", input)
	if err != nil || result.Error == nil || result.Error.Code != "PERMISSION_DENIED" || !strings.Contains(result.Error.Message, "Settings") || executions.Load() != 1 {
		t.Fatalf("opted-out tool was executed: %+v %v", result, err)
	}
	replay, err := service.Call(t.Context(), "owner", "run", "attempt", input)
	if err != nil || replay.Error == nil || replay.Error.Code != result.Error.Code || replay.Error.Message != result.Error.Message || executions.Load() != 1 {
		t.Fatalf("permission remediation was lost in its saved receipt: %+v %v", replay, err)
	}
}
