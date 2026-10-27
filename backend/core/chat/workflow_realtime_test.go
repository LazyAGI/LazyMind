package chat

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"

	"lazymind/core/realtime"
)

// Exercise the actual browser transport and Chat entry point, not just
// handleStreamChat. An empty query deliberately stops after control admission,
// without starting a model request or depending on database configuration.
func TestRealtimeChatWorkflowControlAdmission(t *testing.T) {
	const interactiveOnly = "workflow controls require an interactive single Chat run"
	for _, tc := range []struct {
		name       string
		kind       string
		invocation string
		basic      bool
		dual       bool
		nonStream  bool
		want       string
	}{
		{name: "resume", kind: "resume", want: "Query is required"},
		{name: "approve", kind: "approve", want: "Query is required"},
		{name: "ordinary-chat", want: "Query is required"},
		{name: "ordinary-invocation", invocation: "agent-call", want: "Query is required"},
		{name: "invocation-cannot-resume", kind: "resume", invocation: "agent-call", want: interactiveOnly},
		{name: "invocation-cannot-approve", kind: "approve", invocation: "agent-call", want: interactiveOnly},
		{name: "basic-chat-cannot-control", kind: "resume", basic: true, want: interactiveOnly},
		{name: "dual-chat-cannot-control", kind: "approve", dual: true, want: interactiveOnly},
		{name: "non-stream-cannot-control", kind: "resume", nonStream: true, want: interactiveOnly},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(realtime.Handler{
				Routes: http.HandlerFunc(ChatConversations),
				Authorize: func(r *http.Request) int {
					if r.Header.Get("Authorization") != "Bearer test" {
						return http.StatusUnauthorized
					}
					r.Header.Set("X-User-Id", "owner")
					return http.StatusOK
				},
			})
			defer server.Close()
			dialer := websocket.Dialer{Subprotocols: []string{"lazymind.realtime.v1"}}
			headers := http.Header{"Origin": []string{"http://localhost:8090"}}
			if tc.invocation != "" {
				headers.Set("X-LazyMind-Invocation-Id", tc.invocation)
			}
			ws, _, err := dialer.Dial("ws"+strings.TrimPrefix(server.URL, "http"), headers)
			if err != nil {
				t.Fatal(err)
			}
			defer ws.Close()
			body := map[string]any{"query": "", "stream": !tc.nonStream, "basic_chat_only": tc.basic}
			if tc.kind != "" {
				body["workflow_control"] = map[string]any{"session_id": "workflow", "kind": tc.kind, "expected_state_version": 9}
			}
			if tc.dual {
				body["models"] = []string{"first", "second"}
			}
			payload, err := json.Marshal(body)
			if err != nil {
				t.Fatal(err)
			}
			if err := ws.WriteJSON(map[string]any{
				"type": "open", "id": "control", "method": "POST", "path": "/conversations:chat",
				"authorization": "Bearer test", "payload": string(payload),
			}); err != nil {
				t.Fatal(err)
			}
			_ = ws.SetReadDeadline(time.Now().Add(3 * time.Second))
			var response strings.Builder
			status := 0
			for {
				var frame struct {
					Type   string `json:"type"`
					Status int    `json:"status"`
					Data   []byte `json:"data"`
				}
				if err := ws.ReadJSON(&frame); err != nil {
					t.Fatal(err)
				}
				if frame.Type == "error" {
					t.Fatalf("transport refused request: %+v", frame)
				}
				if frame.Type == "headers" {
					status = frame.Status
				}
				response.Write(frame.Data)
				if frame.Type == "end" {
					break
				}
			}
			if status != http.StatusBadRequest || !strings.Contains(response.String(), tc.want) {
				t.Fatalf("status=%d body=%s; want %q", status, response.String(), tc.want)
			}
		})
	}
}
