package workflowmcp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"lazymind/agentconnector/internal/coreapi"
	"lazymind/agentconnector/internal/credentials"
)

func TestStateGuidesBudgetedRetryWithoutBypassingBarriers(t *testing.T) {
	for _, provider := range []string{"codex", "workbuddy", "deepseek-harness"} {
		for _, continuation := range []string{"continue", "awaiting_user", "failed"} {
			t.Run(provider+"/"+continuation, func(t *testing.T) {
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					_ = json.NewEncoder(w).Encode(map[string]any{"session_id": "test-session", "status": "failed", "control": map[string]any{
						"continuation": continuation, "admission": map[string]any{"can_begin": continuation == "continue"}, "automatic_retry_steps": []string{"plan_page_prompts"},
					}})
				}))
				defer server.Close()
				store, err := credentials.NewStore(t.TempDir(), "")
				if err != nil {
					t.Fatal(err)
				}
				if err := store.Save(credentials.Credentials{ServerURL: server.URL, AccessToken: "test-access", RefreshToken: "test-refresh"}); err != nil {
					t.Fatal(err)
				}
				api, err := coreapi.New(store)
				if err != nil {
					t.Fatal(err)
				}
				state, err := (&Client{api: api, HostProvider: provider}).State(context.Background(), "test-session")
				if err != nil {
					t.Fatal(err)
				}
				if continuation == "continue" {
					if !strings.Contains(state.AgentInstruction, "workflow.step.begin") || !strings.Contains(state.AgentInstruction, "plan_page_prompts") || !strings.Contains(state.AgentInstruction, "Keep this run") {
						t.Fatalf("missing retry guidance: %q", state.AgentInstruction)
					}
				} else if strings.Contains(state.AgentInstruction, "A step failed but automatic retry budget remains") {
					t.Fatalf("barrier bypass instruction: %q", state.AgentInstruction)
				} else if provider != "deepseek-harness" && !strings.Contains(state.AgentInstruction, "END this turn") {
					t.Fatalf("missing yield guidance: %q", state.AgentInstruction)
				}
			})
		}
	}
}
