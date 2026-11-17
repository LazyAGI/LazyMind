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

func TestGlobalProviderFailureSurvivesGatewayAndReceiptReplay(t *testing.T) {
	for _, test := range []struct {
		name, body, code, message string
		status                    int
		retryable                 bool
	}{
		{"rejected", `{"detail":{"code":"PROVIDER_REJECTED","message":"image_generator failed (HTTP 451): provider rejected this request","retryable":false,"upstream":{"http_status":451,"code":"provider_policy","request_id":"request-123"}}}`,
			"PROVIDER_REJECTED", "provider rejected this request", 451, false},
		{"rate limited", `{"detail":{"code":"RATE_LIMITED","message":"image_generator failed (HTTP 429): limit reached","retryable":true,"upstream":{"http_status":429,"request_id":"request-123"}}}`,
			"RATE_LIMITED", "limit reached", 429, true},
		{"arguments", `{"detail":{"code":"INVALID_ARGUMENT","message":"image_size must be 1280x720","retryable":false}}`,
			"INVALID_ARGUMENT", "image_size must be 1280x720", 0, false},
		{"unknown body", `{"detail":"private provider credential"}`, "UNAVAILABLE", "without public error details", 0, true},
		{"unknown code", `{"detail":{"code":"UNREVIEWED","message":"private provider credential"}}`, "UNAVAILABLE", "without public error details", 0, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			var executions atomic.Int32
			service, input := workflowToolFixture(t, func(request *http.Request) (*http.Response, error) {
				if request.URL.Path == "/api/chat/tools/external-catalog" {
					return toolResponse(`{"items":[{"name":"image_generator","available":true,"input_schema":{"type":"object"}}]}`), nil
				}
				if request.URL.Path != "/api/chat/tools/execute" {
					t.Fatalf("unexpected endpoint %s", request.URL.Path)
				}
				var payload struct {
					ToolName  string                    `json:"tool_name"`
					LLMConfig map[string]map[string]any `json:"llm_config"`
				}
				if err := json.NewDecoder(request.Body).Decode(&payload); err != nil {
					t.Fatal(err)
				}
				if payload.ToolName != "image_generator" || payload.LLMConfig["text2image"]["model"] != "Kwai-Kolors/Kolors" {
					t.Fatal("configured image model was not passed to the tool")
				}
				executions.Add(1)
				response := toolResponse(test.body)
				response.StatusCode = http.StatusBadGateway
				return response, nil
			})
			if err := service.DB.AutoMigrate(&orm.MCPServer{}, &orm.MCPServerTool{}, &orm.ExternalCapabilityGrant{},
				&orm.ExternalCapabilityInvocation{}, &orm.UserSelectedProvider{}, &orm.UserModelProviderGroup{}, &orm.UserModelProvider{},
				&orm.UserSelectedModel{}, &orm.UserSelectedCloudModel{}, &orm.UserModelProviderGroupModel{}); err != nil {
				t.Fatal(err)
			}
			for _, row := range []any{
				&orm.UserModelProvider{ID: "provider", DefaultModelProviderID: "default", Name: "SiliconFlow",
					BaseModel: orm.BaseModel{CreateUserID: "owner"}},
				&orm.UserModelProviderGroup{ID: "group", UserModelProviderID: "provider", Name: "Images",
					BaseURL: "https://model.invalid/v1", APIKey: "private-fixture-key", IsVerified: true,
					BaseModel: orm.BaseModel{CreateUserID: "owner"}},
				&orm.UserModelProviderGroupModel{ID: "image-model", UserModelProviderID: "provider", UserModelProviderGroupID: "group",
					ProviderName: "SiliconFlow", Name: "Kwai-Kolors/Kolors", ModelType: "text2image",
					BaseModel: orm.BaseModel{CreateUserID: "owner"}},
				&orm.UserSelectedModel{UserID: "owner", ModelKey: "text2image", UserModelProviderGroupModelID: "image-model"},
			} {
				if err := service.DB.Create(row).Error; err != nil {
					t.Fatal(err)
				}
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
			node.Execution.Tools = []graphengine.ExecutionTool{{Name: "image_generator", Source: "global"}}
			graph.Nodes["write"] = node
			if err := service.DB.Model(&revision).Update("compiled_graph", graph.JSON()).Error; err != nil {
				t.Fatal(err)
			}
			if err := service.DB.Model(&orm.WorkflowSession{}).Where("id = ?", "run").Update("control_binding_json", `{"provider":"codex"}`).Error; err != nil {
				t.Fatal(err)
			}
			input.ToolName, input.Arguments = "image_generator", map[string]any{"prompt": "background"}
			var previous []byte
			for repeat := 0; repeat < 2; repeat++ {
				result, err := service.Call(t.Context(), "owner", "run", "attempt", input)
				if err != nil || result.Error == nil || result.Error.Code != test.code ||
					!strings.Contains(result.Error.Message, test.message) || result.Error.Retryable != test.retryable {
					t.Fatalf("lost error contract: %+v %v", result.Error, err)
				}
				if test.status != 0 && (result.Error.Upstream == nil || result.Error.Upstream.HTTPStatus != test.status || result.Error.Upstream.RequestID != "request-123") {
					t.Fatalf("lost provider diagnostics: %+v", result.Error)
				}
				encoded, _ := json.Marshal(result)
				if strings.Contains(string(encoded), "private") || (repeat > 0 && string(previous) != string(encoded)) {
					t.Fatalf("unsafe or changed receipt: %s", encoded)
				}
				previous = encoded
			}
			if executions.Load() != 1 {
				t.Fatal("replaying a provider failure executed a second paid tool call")
			}
			if test.status != 0 {
				var audit orm.ExternalCapabilityInvocation
				if err := service.DB.First(&audit).Error; err != nil || audit.ErrorCode != test.code || !strings.Contains(audit.ErrorMessage, test.message) {
					t.Fatalf("audit lost public provider failure: %+v %v", audit, err)
				}
			}
		})
	}
}
