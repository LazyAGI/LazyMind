package workflowtool

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/mux"
	"lazymind/core/common/orm"
	"lazymind/core/workflow/executor"
)

func TestWorkerToolGrantCannotBecomeControlAuthority(t *testing.T) {
	service, _ := workflowToolFixture(t, func(*http.Request) (*http.Response, error) {
		return toolResponse(`{"result":{"verified":true}}`), nil
	})
	ctx := context.Background()
	token, err := executor.GrantToolWorker(ctx, service.DB, "owner", "run", "attempt", "lease", "worker-1")
	if err != nil {
		t.Fatal(err)
	}
	again, err := executor.GrantToolWorker(ctx, service.DB, "owner", "run", "attempt", "lease", "worker-1")
	if err != nil || again != token {
		t.Fatalf("grant not idempotent: %v", err)
	}
	if strings.Contains(token, "lease") {
		t.Fatal("worker received parent lease")
	}
	if _, err := executor.AuthorizeExternalExecution(ctx, service.DB, "owner", "run", "attempt", token); err == nil {
		t.Fatal("worker credential accepted as control authority")
	}
	if _, err := executor.GrantToolWorker(ctx, service.DB, "owner", "run", "attempt", token, "nested"); err == nil {
		t.Fatal("worker delegated another grant")
	}
	for _, other := range []struct{ owner, run, attempt string }{
		{"other", "run", "attempt"}, {"owner", "other", "attempt"}, {"owner", "run", "other"},
	} {
		if _, err := executor.ResolveToolWorker(ctx, service.DB, other.owner, other.run, other.attempt, token); err == nil {
			t.Fatal("worker escaped its principal/attempt scope")
		}
	}
	body, _ := json.Marshal(map[string]any{"execution_handle": token, "tool_name": "inspect_output",
		"command_id": "worker-call", "arguments": map[string]any{"file": "artifact-ref"}})
	req := httptest.NewRequest(http.MethodPost, "/tools:call", bytes.NewReader(body))
	req.Header.Set("X-User-Id", "owner")
	req = mux.SetURLVars(req, map[string]string{"session_id": "run", "attempt_id": "attempt"})
	response := httptest.NewRecorder()
	(Handler{Service: service}).Call(response, req)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "verified") {
		t.Fatalf("direct scoped tool call failed: %d %s", response.Code, response.Body.String())
	}
	if err := service.DB.Model(&orm.WorkflowSessionStep{}).Where("id = ?", "attempt").Update("lease_token", "replacement").Error; err != nil {
		t.Fatal(err)
	}
	if _, err := executor.ResolveToolWorker(ctx, service.DB, "owner", "run", "attempt", token); err == nil {
		t.Fatal("old worker survived controller re-claim")
	}
}

func TestWorkerGrantIsFencedByStopExpiryAndRevision(t *testing.T) {
	for _, change := range []string{"stop", "expire", "revision", "complete", "invalidate"} {
		t.Run(change, func(t *testing.T) {
			service, _ := workflowToolFixture(t, func(*http.Request) (*http.Response, error) { return toolResponse(`{}`), nil })
			token, err := executor.GrantToolWorker(t.Context(), service.DB, "owner", "run", "attempt", "lease", "worker")
			if err != nil {
				t.Fatal(err)
			}
			switch change {
			case "stop":
				service.DB.Model(&orm.WorkflowSession{}).Where("id = ?", "run").Update("status", "stopped")
			case "expire":
				service.DB.Model(&orm.WorkflowSessionStep{}).Where("id = ?", "attempt").Update("lease_expires_at", time.Now().Add(-time.Minute))
			case "complete":
				service.DB.Model(&orm.WorkflowSessionStep{}).Where("id = ?", "attempt").Update("status", "succeeded")
			case "invalidate":
				service.DB.Model(&orm.WorkflowSessionStep{}).Where("id = ?", "attempt").Update("validity", "stale")
			case "revision":
				service.DB.Model(&orm.WorkflowSession{}).Where("id = ?", "run").Update("plugin_revision_id", "different")
			}
			if _, err := executor.ResolveToolWorker(t.Context(), service.DB, "owner", "run", "attempt", token); err == nil {
				t.Fatal("worker credential survived fencing")
			}
		})
	}
}
