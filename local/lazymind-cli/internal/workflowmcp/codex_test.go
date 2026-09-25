package workflowmcp

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"lazymind/agentconnector/internal/coreapi"
	"lazymind/agentconnector/internal/credentials"
)

const threadUUID = "01a0cd90-7aaa-7960-86ab-6ee368c3d327"
const otherThreadUUID = "01a0cd90-7aaa-7960-86ab-6ee368c3d328"

func TestCodexDriverIdentity(t *testing.T) {
	for _, tc := range []struct {
		name           string
		meta           mcp.Meta
		fallback, want string
		fail           bool
	}{
		{name: "native beats agent", meta: mcp.Meta{"thread_id": threadUUID}, fallback: otherThreadUUID, want: threadUUID},
		{name: "nested", meta: mcp.Meta{"x-codex-turn-metadata": map[string]any{"threadId": threadUUID}}, want: threadUUID},
		{name: "encoded", meta: mcp.Meta{"x-codex-turn-metadata": `{"thread_id":"` + threadUUID + `"}`}, want: threadUUID},
		{name: "agent fallback", fallback: threadUUID, want: threadUUID},
		{name: "transport session is not a thread", meta: mcp.Meta{"session_id": threadUUID}, fail: true},
		{name: "invalid host identity cannot fallback", meta: mcp.Meta{"thread_id": "bad"}, fallback: threadUUID, fail: true},
		{name: "conflicting host identity", meta: mcp.Meta{"thread_id": threadUUID, "threadId": otherThreadUUID}, fail: true},
		{name: "no identity", fail: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := codexDriver(&mcp.CallToolRequest{Params: &mcp.CallToolParamsRaw{Meta: tc.meta}}, tc.fallback)
			if (err != nil) != tc.fail || got != tc.want {
				t.Fatalf("got %q, %v", got, err)
			}
		})
	}
}

func TestCodexStartBindsBeforeReturningStateAndRetainsRetryIdentity(t *testing.T) {
	for _, failBind := range []bool{false, true} {
		t.Run(map[bool]string{false: "bound", true: "retryable binding failure"}[failBind], func(t *testing.T) {
			bound := false
			requests := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests++
				switch r.URL.Path {
				case "/api/core/workflow-control/capabilities":
					_, _ = w.Write([]byte(`{"protocol":"workflow.control.v1","schema_ready":true}`))
				case "/api/core/workflow-preparations":
					var body map[string]any
					_ = json.NewDecoder(r.Body).Decode(&body)
					if body["host_binding_required"] != true || body["host_provider"] != "codex" || body["origin_ref"] != "codex:"+threadUUID {
						t.Errorf("wrong preparation: %v", body)
					}
					_, _ = w.Write([]byte(`{"preparation_id":"prep-1"}`))
				case "/api/core/workflow-preparations/prep-1:consume":
					_, _ = w.Write([]byte(`{"session_id":"run-1","workflow_id":"wf-1"}`))
				case "/api/core/workflow-sessions/run-1/projection":
					if !bound {
						t.Error("projection read before binding")
					}
					_, _ = w.Write([]byte(`{"session_id":"run-1","control":{"continuation":"awaiting_user"}}`))
				default:
					t.Errorf("unexpected request: %s", r.URL.Path)
				}
			}))
			defer server.Close()
			store, _ := credentials.NewStore(t.TempDir(), "")
			if err := store.Save(credentials.Credentials{ServerURL: server.URL, AccessToken: "access", RefreshToken: "refresh"}); err != nil {
				t.Fatal(err)
			}
			api, _ := coreapi.New(store)
			client := &Client{api: api, HostProvider: "codex", RequireHostBinding: true, BindController: func(_ context.Context, run, driver string) error {
				if run != "run-1" || driver != threadUUID || requests != 3 {
					t.Fatalf("wrong binding: %s %s requests=%d", run, driver, requests)
				}
				if failBind {
					return errors.New("offline")
				}
				bound = true
				return nil
			}}
			_, err := client.Start(context.Background(), StartInput{WorkflowID: "wf-1"})
			if err == nil || requests != 0 {
				t.Fatal("missing identity created a run")
			}
			result, err := client.Start(context.Background(), StartInput{WorkflowID: "wf-1", IdempotencyKey: "retry-1", DriverSessionID: threadUUID})
			if failBind {
				if err == nil || !strings.Contains(err.Error(), "idempotency_key=retry-1 and session_id=run-1") || requests != 3 {
					t.Fatalf("lost retry identity: %v", err)
				}
			} else if err != nil || result.SessionID != "run-1" || !strings.Contains(result.State.AgentInstruction, "END this turn") {
				t.Fatalf("unexpected result: %+v %v", result, err)
			}
		})
	}
}
