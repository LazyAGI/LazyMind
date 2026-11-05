package workflowmcp

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"reflect"
	"strings"
	"testing"
)

func TestStartClarificationMCPRoundTrip(t *testing.T) {
	requests := map[string]string{}
	created, consumeCalls := map[string]bool{}, 0
	client := readinessClient(t, func(w http.ResponseWriter, r *http.Request) {
		var response any
		switch r.URL.Path {
		case "/api/core/workflow-preparations":
			body, _ := io.ReadAll(r.Body)
			var input map[string]any
			if err := json.Unmarshal(body, &input); err != nil {
				t.Error(err)
			}
			key, _ := input["idempotency_key"].(string)
			if previous, exists := requests[key]; exists && previous != string(body) {
				w.WriteHeader(http.StatusConflict)
				_, _ = w.Write([]byte(`{"ok":false,"error":{"code":"IDEMPOTENCY_CONFLICT","message":"idempotency key was used with another payload"}}`))
				return
			}
			requests[key] = string(body)
			if values, ok := input["clarification_values"].(map[string]any); ok {
				if input["revision_id"] != "revision-5" || values["topic"] != "Genshin" || values["slide_count"] != "9" || values["generate_background_images"] != "disabled" || values["template_mode"] != "none" {
					t.Errorf("lost confirmed answers or pinned revision: %v", input)
				}
				response = map[string]any{"preparation_id": "ready", "status": "ready", "workflow_revision": "revision-5"}
			} else {
				response = map[string]any{"preparation_id": "pending", "status": "needs_input", "workflow_id": "ppt-workflow", "workflow_revision": "revision-5",
					"missing_inputs": []string{}, "missing_clarification_fields": []string{"topic", "slide_count", "generate_background_images"},
					"clarification_values": map[string]any{"template_mode": "none"},
					"questions":            []any{map[string]any{"id": "topic", "text": "Presentation topic?", "type": "text", "required": true, "validation": map[string]any{"min_length": 1}}}}
			}
		case "/api/core/workflow-preparations/ready:consume":
			consumeCalls++
			var request struct {
				SessionID string `json:"session_id"`
			}
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				t.Error(err)
			}
			if request.SessionID != sessionIDForKey("startup-answered-1234") {
				t.Errorf("retry changed session identity: %s", request.SessionID)
			}
			created[request.SessionID] = true
			response = map[string]any{"session_id": "run-1", "workflow_id": "ppt-workflow", "workflow_revision_id": "revision-5"}
		case "/api/core/workflow-sessions/run-1/projection":
			response = map[string]any{"session_id": "run-1", "control": map[string]any{"continuation": "continue"}}
		default:
			t.Errorf("unready preparation must not be consumed: %s", r.URL.Path)
			w.WriteHeader(http.StatusConflict)
			return
		}
		_ = json.NewEncoder(w).Encode(response)
	})
	session := inputToolSession(t, client)
	args := map[string]any{"workflow_id": "ppt-workflow", "request_context": "Genshin, 9 slides, no AI backgrounds", "idempotency_key": "startup-pending-1234"}
	result := inputToolCall(t, session, "workflow.start", args)
	if result.IsError {
		t.Fatalf("needs_input must be a normal tool result: %+v", result.Content)
	}
	encoded, _ := json.Marshal(result.StructuredContent)
	var pending map[string]any
	_ = json.Unmarshal(encoded, &pending)
	if pending["status"] != "needs_input" || pending["revision_id"] != "revision-5" || len(created) != 0 || consumeCalls != 0 {
		t.Fatalf("lost preparation state: %s", encoded)
	}
	for _, field := range []string{"session_id", "state", "control"} {
		if _, exists := pending[field]; exists {
			t.Fatalf("uncreated run exposes panel/session state %q: %s", field, encoded)
		}
	}
	if !reflect.DeepEqual(pending["missing_clarification_fields"], []any{"topic", "slide_count", "generate_background_images"}) || len(pending["questions"].([]any)) != 1 || pending["clarification_values"].(map[string]any)["template_mode"] != "none" {
		t.Fatalf("lost questions/defaults: %s", encoded)
	}
	question := pending["questions"].([]any)[0].(map[string]any)
	if question["validation"].(map[string]any)["min_length"] != float64(1) {
		t.Fatalf("lost question validation: %s", encoded)
	}
	if !strings.Contains(pending["agent_instruction"].(string), "new idempotency_key") {
		t.Fatalf("missing safe answer-submission guidance: %s", encoded)
	}
	args["revision_id"] = pending["revision_id"]
	args["clarification_values"] = map[string]any{"topic": "Genshin", "slide_count": "9", "generate_background_images": "disabled", "template_mode": "none"}
	conflict := inputToolCall(t, session, "workflow.start", args)
	conflictJSON, _ := json.Marshal(conflict)
	if !conflict.IsError || !strings.Contains(string(conflictJSON), "IDEMPOTENCY_CONFLICT") || consumeCalls != 0 {
		t.Fatal("changed answers must not silently reuse a preparation key")
	}
	args["idempotency_key"] = "startup-answered-1234"
	for range 2 {
		result = inputToolCall(t, session, "workflow.start", args)
		if result.IsError {
			t.Fatalf("confirmed answers rejected by MCP: %+v", result.Content)
		}
		encoded, _ = json.Marshal(result.StructuredContent)
		var started map[string]any
		_ = json.Unmarshal(encoded, &started)
		if started["status"] != "started" || started["session_id"] != "run-1" || started["revision_id"] != "revision-5" {
			t.Fatalf("lost started session: %s", encoded)
		}
	}
	if len(created) != 1 || consumeCalls != 2 || len(requests) != 2 {
		t.Fatalf("retries changed preparation identity: created=%v consumes=%d requests=%v", created, consumeCalls, requests)
	}
}

func TestStartNeedsInputDoesNotBindController(t *testing.T) {
	for _, provider := range []string{"codex", "workbuddy"} {
		t.Run(provider, func(t *testing.T) {
			bound := false
			client := readinessClient(t, func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/api/core/workflow-control/capabilities":
					_, _ = w.Write([]byte(`{"protocol":"workflow.control.v1","schema_ready":true}`))
				case "/api/core/workflow-preparations":
					_, _ = w.Write([]byte(`{"preparation_id":"pending","status":"needs_input","missing_inputs":["source"],"questions":[{"id":"source","attachment":{"material_id":"source","required":true}}]}`))
				default:
					t.Errorf("needs_input contacted session endpoint: %s", r.URL.Path)
					w.WriteHeader(http.StatusConflict)
				}
			})
			client.HostProvider, client.RequireHostBinding = provider, true
			client.BindController = func(context.Context, string, string) error { bound = true; return nil }
			result, err := client.Start(t.Context(), StartInput{WorkflowID: "wf", DriverSessionID: threadUUID})
			encoded, _ := json.Marshal(result)
			if err != nil || bound || !strings.Contains(string(encoded), `"missing_inputs":["source"]`) || !strings.Contains(string(encoded), `"attachment"`) {
				t.Fatalf("needs_input lost attachments or bound an uncreated run: %s %v", encoded, err)
			}
		})
	}
}

func TestStartRejectsUnsupportedPreparationState(t *testing.T) {
	for _, response := range []string{`{"preparation_id":"pending","status":"failed"}`, `{"status":"ready"}`} {
		t.Run(response, func(t *testing.T) {
			client := readinessClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/api/core/workflow-preparations" {
					t.Errorf("invalid preparation was consumed: %s", r.URL.Path)
				}
				_, _ = w.Write([]byte(response))
			})
			if _, err := client.Start(t.Context(), StartInput{WorkflowID: "wf"}); err == nil {
				t.Fatal("unsupported/incomplete preparation was accepted")
			}
		})
	}
}
