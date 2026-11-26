package workflowmcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
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
			got, err := CodexDriver(&mcp.CallToolRequest{Params: &mcp.CallToolParamsRaw{Meta: tc.meta}}, tc.fallback)
			if (err != nil) != tc.fail || got != tc.want {
				t.Fatalf("got %q, %v", got, err)
			}
		})
	}
}

func TestControlledStartBindsBeforeReturningStateAndRetainsRetryIdentity(t *testing.T) {
	for _, provider := range []string{"codex", "workbuddy"} {
		for _, failBind := range []bool{false, true} {
			t.Run(provider+"/"+map[bool]string{false: "bound", true: "retryable binding failure"}[failBind], func(t *testing.T) {
				bound := false
				requests := 0
				consumeID := ""
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					requests++
					switch r.URL.Path {
					case "/api/core/workflow-control/capabilities":
						_, _ = w.Write([]byte(`{"protocol":"workflow.control.v1","schema_ready":true}`))
					case "/api/core/workflow-preparations":
						var body map[string]any
						_ = json.NewDecoder(r.Body).Decode(&body)
						if body["host_binding_required"] != true || body["host_provider"] != provider || body["origin_ref"] != provider+":"+threadUUID || body["idempotency_key"] != "retry-1" {
							t.Errorf("wrong preparation: %v", body)
						}
						_, _ = w.Write([]byte(`{"preparation_id":"prep-1"}`))
					case "/api/core/workflow-preparations/prep-1:consume":
						var body struct {
							SessionID string `json:"session_id"`
						}
						_ = json.NewDecoder(r.Body).Decode(&body)
						if consumeID != "" && consumeID != body.SessionID {
							t.Errorf("retry changed session identity: %q != %q", consumeID, body.SessionID)
						}
						consumeID = body.SessionID
						_, _ = w.Write([]byte(`{"session_id":"run-1","workflow_id":"wf-1"}`))
					case "/api/core/workflow-sessions/run-1/projection":
						if !bound {
							t.Error("projection read before binding")
						}
						_, _ = fmt.Fprintf(w, `{"session_id":"run-1","control":{"continuation":"awaiting_user","binding":{"bound":true,"driver_session_id":%q}}}`, threadUUID)
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
				client := &Client{api: api, HostProvider: provider, RequireHostBinding: true, BindController: func(_ context.Context, run, driver string) error {
					if run != "run-1" || driver != threadUUID || requests%3 != 0 {
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
				input := StartInput{WorkflowID: "wf-1", SessionID: "run-1", IdempotencyKey: "retry-1", DriverSessionID: threadUUID}
				result, err := client.Start(context.Background(), input)
				if failBind {
					if err != nil || result.BindingStatus != "pending" || result.IdempotencyKey != "retry-1" || result.SessionID != "run-1" || requests != 3 {
						t.Fatalf("lost retry identity: %+v %v", result, err)
					}
					failBind = false
					input.SessionID, input.IdempotencyKey = result.SessionID, result.IdempotencyKey
					result, err = client.Start(context.Background(), input)
				}
				if err != nil || result.SessionID != "run-1" || result.BindingStatus != "confirmed" || result.State == nil || !strings.Contains(result.State.AgentInstruction, "END this turn") {
					t.Fatalf("unexpected result: %+v %v", result, err)
				}
			})
		}
	}

}

func TestDisconnectedHostRejectsStartBeforeCreatingCoreRun(t *testing.T) {
	checked := false
	client := &Client{HostProvider: "codex", RequireHostBinding: true, EnsureHostReady: func(context.Context) error { checked = true; return errors.New("reconnect Codex with CDP") }}
	_, err := client.Start(context.Background(), StartInput{WorkflowID: "acceptance", DriverSessionID: threadUUID})
	if !checked || err == nil || !strings.Contains(err.Error(), "reconnect Codex") {
		t.Fatalf("unexpected start: %v", err)
	}
}
