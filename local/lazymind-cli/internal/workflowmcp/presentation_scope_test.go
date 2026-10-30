package workflowmcp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"lazymind/agentconnector/internal/coreapi"
	"lazymind/agentconnector/internal/credentials"
)

func TestPanelURLScopeOnMCPWire(t *testing.T) {
	t.Setenv("LAZYMIND_WEB_URL", "https://panel.example")
	for _, provider := range []string{"codex", "workbuddy", "deepseek-harness", ""} {
		t.Run(provider, func(t *testing.T) {
			apiServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var body any
				switch {
				case strings.HasSuffix(r.URL.Path, "/workflow-preparations"):
					body = map[string]any{"preparation_id": "prep-1"}
				case strings.HasSuffix(r.URL.Path, "prep-1:consume"):
					body = map[string]any{"session_id": "run-1", "workflow_id": "wf-1"}
				case strings.HasSuffix(r.URL.Path, "/projection"):
					body = map[string]any{"session_id": "run-1", "interaction_url": "https://stale.example/panel",
						"state_version": 1, "control": map[string]any{"protocol": ControlProtocol, "session_id": "run-1",
							"state_version": 1, "continuation": "awaiting_user", "admission": map[string]any{"can_begin": false}}}
				case strings.HasSuffix(r.URL.Path, "/executions:begin"):
					body = map[string]any{"receipt": map[string]any{"execution_id": "exec-1"}}
				case strings.HasSuffix(r.URL.Path, "/hosted-attempts/exec-1:begin"), strings.HasSuffix(r.URL.Path, "/hosted-attempts/exec-1:resume"):
					body = map[string]any{"execution_id": "exec-1", "execution_handle": "test-grant", "executor_host": "external-agent",
						"step_contract": map[string]any{"prompt": "Read https://files.example/artifact"}}
				case strings.HasSuffix(r.URL.Path, "/hosted-attempts/exec-1:complete"):
					body = map[string]any{"execution_id": "exec-1", "attempt_status": "succeeded"}
				default:
					t.Errorf("unexpected path %s", r.URL.Path)
					w.WriteHeader(http.StatusNotFound)
					return
				}
				_ = json.NewEncoder(w).Encode(body)
			}))
			defer apiServer.Close()
			store, _ := credentials.NewStore(t.TempDir(), "")
			if err := store.Save(credentials.Credentials{ServerURL: apiServer.URL, AccessToken: "test-access", RefreshToken: "test-refresh"}); err != nil {
				t.Fatal(err)
			}
			api, _ := coreapi.New(store)
			server := mcp.NewServer(&mcp.Implementation{Name: "presentation-test", Version: "1"}, nil)
			Register(server, &Client{api: api, HostProvider: provider})
			a, b := mcp.NewInMemoryTransports()
			ctx := context.Background()
			ss, err := server.Connect(ctx, b, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer ss.Close()
			cs, err := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "1"}, nil).Connect(ctx, a, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer cs.Close()
			for _, call := range []struct {
				name string
				args map[string]any
			}{
				{"workflow.start", map[string]any{"workflow_id": "wf-1"}},
				{"workflow.state", map[string]any{"session_id": "run-1"}},
				{"workflow.step.begin", map[string]any{"session_id": "run-1", "step_id": "step-1"}},
				{"workflow.step.claim", map[string]any{"session_id": "run-1", "execution_id": "exec-1"}},
				{"workflow.step.resume", map[string]any{"session_id": "run-1", "execution_id": "exec-1"}},
				{"workflow.step.complete", map[string]any{"session_id": "run-1", "execution_id": "exec-1", "execution_handle": "test-grant", "outcome": "succeeded"}},
			} {
				result, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: call.name, Arguments: call.args})
				if err != nil || result.IsError {
					t.Fatalf("%s: %v %+v", call.name, err, result)
				}
				for _, channel := range []any{result.Content, result.StructuredContent} {
					encoded, _ := json.Marshal(channel)
					text := string(encoded)
					if strings.Contains(text, "https://stale.example") {
						t.Fatal("Core presentation URL leaked")
					}
					if strings.Contains(text, "https://panel.example/workflow-runs/run-1/embed") {
						t.Fatalf("wrong panel URL scope for %s: %s", call.name, text)
					}
					if strings.Contains(text, "interaction_url") {
						t.Fatalf("empty URL field retained: %s", text)
					}
				}
				if call.name == "workflow.step.begin" || call.name == "workflow.step.claim" || call.name == "workflow.step.resume" {
					encoded, _ := json.Marshal(result.StructuredContent)
					if !strings.Contains(string(encoded), "https://files.example/artifact") || !strings.Contains(string(encoded), "test-grant") {
						t.Fatal("execution or artifact information changed")
					}
				}
			}
		})
	}
}
