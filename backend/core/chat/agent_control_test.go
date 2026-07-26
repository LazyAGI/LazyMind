package chat

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestNotifyToolLimitDecision(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("unexpected method: %s", r.Method)
		}
		if r.URL.Path != "/api/agent/tool-limit-decision" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		var body map[string]string
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		if body["conversation_id"] != "conversation-1" ||
			body["decision_id"] != "decision-1" || body["action"] != "continue" {
			t.Fatalf("unexpected body: %#v", body)
		}
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer server.Close()
	t.Setenv("LAZYMIND_CHAT_SERVICE_URL", server.URL)

	if err := notifyToolLimitDecision("conversation-1", "decision-1", "continue"); err != nil {
		t.Fatalf("notify tool-limit decision: %v", err)
	}
}

func TestNotifyToolLimitDecisionRejectsInactiveDecision(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"ok":false}`))
	}))
	defer server.Close()
	t.Setenv("LAZYMIND_CHAT_SERVICE_URL", server.URL)

	if err := notifyToolLimitDecision("conversation-1", "expired", "continue"); err == nil {
		t.Fatal("expected inactive decision error")
	}
}
