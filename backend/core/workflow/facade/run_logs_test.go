package facade

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/mux"
	"lazymind/core/common/orm"
	"lazymind/core/workflow/controlstore"
)

func TestRunLogsExportFiltersOwnerCutoffAndPayloads(t *testing.T) {
	h, db := testHandler(t)
	if err := db.AutoMigrate(&orm.WorkflowSession{}, &orm.WorkflowSessionStep{}, &orm.SubAgentTask{}, &orm.SubAgentStep{}); err != nil {
		t.Fatal(err)
	}
	start := time.Now().UTC().Add(-time.Minute).Truncate(time.Millisecond)
	cutoff := start.Add(3 * time.Second)
	for _, row := range []any{
		&orm.WorkflowSession{ID: "run", CreateUserID: "owner", Status: "completed", CreatedAt: start, UpdatedAt: cutoff.Add(time.Second)},
		&orm.WorkflowSessionStep{ID: "attempt", SessionID: "run", StepID: "search", Attempt: 1, TaskID: "task",
			Status: "succeeded", CreatedAt: start, UpdatedAt: cutoff.Add(time.Second)},
		&orm.SubAgentTask{ID: "task", CreateUserID: "owner", CreatedAt: start, StartedAt: &start,
			InputSlots: json.RawMessage(`[]`), OutputSlots: json.RawMessage(`[]`)},
		&orm.SubAgentStep{ID: "call", TaskID: "task", Role: "assistant", CreatedAt: start.Add(time.Second),
			Content: json.RawMessage(`{"tool_calls":[{"id":"c1","function":{"name":"search","arguments":"PRIVATE_ARGUMENT"}}],"text":"PRIVATE_MESSAGE"}`)},
		&orm.SubAgentStep{ID: "failure", TaskID: "task", Role: "tool", Seq: 1, CreatedAt: start.Add(2 * time.Second),
			Content: json.RawMessage(`{"tool_results":[{"tool_call_id":"c1","result":{"isError":true,"error":"PRIVATE_RESULT"}}]}`)},
		&orm.SubAgentStep{ID: "late", TaskID: "task", Role: "assistant", Seq: 2, CreatedAt: cutoff.Add(time.Second),
			Content: json.RawMessage(`{"tool_calls":[{"id":"c2","name":"LATE_TOOL"}]}`)},
		&orm.WorkflowEvent{SessionID: "run", OwnerUserID: "owner", EventType: "control.changed", CreatedAt: start.Add(time.Second),
			PayloadJSON: json.RawMessage(`{"kind":"resume","message":"PRIVATE_EVENT"}`)},
	} {
		if err := db.Create(row).Error; err != nil {
			t.Fatal(err)
		}
	}
	handler := h.SessionAccess(http.HandlerFunc(h.RunLogs))
	read := func(owner string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		req := request(http.MethodGet, "/workflow-sessions/run/log-export?cutoff_ms="+strconv.FormatInt(cutoff.UnixMilli(), 10), owner, nil)
		handler.ServeHTTP(w, mux.SetURLVars(req, map[string]string{"session_id": "run"}))
		return w
	}
	if w := read("another-owner"); w.Code != http.StatusForbidden {
		t.Fatalf("another owner got %d", w.Code)
	}
	w := read("owner")
	body := w.Body.String()
	if w.Code != http.StatusOK || !strings.Contains(body, "control.changed.resume") ||
		!strings.Contains(body, `"tool":"search"`) || !strings.Contains(body, `"error_code":"TOOL_ERROR"`) ||
		!strings.Contains(body, `"duration_ms":1000`) || strings.Contains(body, "PRIVATE_") ||
		strings.Contains(body, "LATE_TOOL") || strings.Contains(body, `"status":"succeeded"`) {
		t.Fatalf("unexpected export %d: %s", w.Code, body)
	}
}

func TestHostLogGrantIsScopedToRunOwnerAndBinding(t *testing.T) {
	now := time.Unix(1800000000, 0)
	binding := controlstore.Binding{Provider: "codex", ConnectorID: "connector", DriverSession: "driver", CredentialHash: "private-hash"}
	grant := issueHostLogGrant("run", "owner", binding, now, now)
	if cutoff, ok := verifyHostLogGrant(grant, "run", "owner", binding, now); !ok || cutoff != now.UnixMilli() {
		t.Fatalf("valid grant rejected, cutoff %d", cutoff)
	}
	if _, ok := verifyHostLogGrant(grant, "another-run", "owner", binding, now); ok {
		t.Fatal("accepted grant from another run")
	}
	if _, ok := verifyHostLogGrant(grant, "run", "another-owner", binding, now); ok {
		t.Fatal("accepted grant from another owner")
	}
	if _, ok := verifyHostLogGrant(grant, "run", "owner", binding, now.Add(hostLogGrantLifetime)); ok {
		t.Fatal("accepted expired grant")
	}
	binding.DriverSession = "new-driver"
	if _, ok := verifyHostLogGrant(grant, "run", "owner", binding, now); ok {
		t.Fatal("accepted grant after a binding change")
	}
}
