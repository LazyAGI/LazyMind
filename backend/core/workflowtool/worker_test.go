package workflowtool

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/mux"
	"lazymind/core/common/orm"
	"lazymind/core/workflow/executor"
	"lazymind/core/workflow/graphengine"
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

func TestPaperInternalLivenessIsBoundToCurrentLease(t *testing.T) {
	service, _ := workflowToolFixture(t, func(*http.Request) (*http.Response, error) { return toolResponse(`{}`), nil })
	var revision orm.WorkflowRevision
	if err := service.DB.First(&revision, "id = ?", "pinned-revision").Error; err != nil {
		t.Fatal(err)
	}
	var graph graphengine.CompiledStateGraph
	if err := json.Unmarshal(revision.CompiledGraph, &graph); err != nil {
		t.Fatal(err)
	}
	graph.Nodes["write"].Execution.OutputValidator = graphengine.PaperAgentOutputValidator
	if err := service.DB.Model(&revision).Update("compiled_graph", graph.JSON()).Error; err != nil {
		t.Fatal(err)
	}
	t.Setenv("LAZYMIND_AUTH_SERVICE_INTERNAL_TOKEN", "internal-test")
	call := func(token, lease string, extra string) int {
		payload, _ := json.Marshal(map[string]any{"tool_name": "execution_status", "arguments": map[string]any{}, "lease_hash": lease})
		request := httptest.NewRequest(http.MethodPost, "/internal/workflow-attempts/attempt/paper-data", strings.NewReader(string(payload)+extra))
		request.Header.Set("X-LazyMind-Internal-Token", token)
		request = mux.SetURLVars(request, map[string]string{"attempt_id": "attempt"})
		response := httptest.NewRecorder()
		(Handler{Service: service}).PaperData(response, request)
		return response.Code
	}
	hash := fmt.Sprintf("%x", sha256.Sum256([]byte("lease")))
	if got := call("internal-test", hash, ""); got != http.StatusOK {
		t.Fatalf("active execution rejected: %d", got)
	}
	if call("bad", hash, "") != http.StatusUnauthorized {
		t.Fatal("internal identity bypass")
	}
	if call("internal-test", "different", "") != http.StatusConflict {
		t.Fatal("reclaimed lease survived")
	}
	if call("internal-test", hash, " {}") != http.StatusUnprocessableEntity {
		t.Fatal("trailing JSON accepted")
	}
	if err := service.DB.Model(&orm.WorkflowSession{}).Where("id = ?", "run").Update("status", "stopped").Error; err != nil {
		t.Fatal(err)
	}
	if call("internal-test", hash, "") != http.StatusConflict {
		t.Fatal("stopped execution reported active")
	}
}

func TestPaperToolBudgetIsPinnedAtomicAndIdempotent(t *testing.T) {
	var executed atomic.Int32
	service, input := workflowToolFixture(t, func(r *http.Request) (*http.Response, error) {
		executed.Add(1)
		return toolResponse(`{"result":{"actual":true}}`), nil
	})
	var revision orm.WorkflowRevision
	if err := service.DB.First(&revision, "id = ?", "pinned-revision").Error; err != nil {
		t.Fatal(err)
	}
	var graph graphengine.CompiledStateGraph
	if err := json.Unmarshal(revision.CompiledGraph, &graph); err != nil {
		t.Fatal(err)
	}
	graph.Nodes["write"].Execution.OutputValidator = graphengine.PaperAgentOutputValidator
	graph.Runtime.ExecutionLimits = map[string]graphengine.ExecutionLimit{"write": {Rounds: 32, Timeout: 1200, ToolCalls: map[string]int{"inspect_output": 2}}}
	if err := service.DB.Model(&revision).Update("compiled_graph", graph.JSON()).Error; err != nil {
		t.Fatal(err)
	}
	contract, err := executor.AuthorizeExternalExecution(t.Context(), service.DB, "owner", "run", "attempt", "lease")
	if err != nil {
		t.Fatal(err)
	}
	if contract.Execution.Limits == nil || contract.Execution.Limits.ToolCalls["inspect_output"] != 2 {
		t.Fatal("worker contract lost pinned limits")
	}
	first, err := service.Call(t.Context(), "owner", "run", "attempt", input)
	if err != nil || first.Error != nil {
		t.Fatalf("first=%+v err=%v", first, err)
	}
	replay, err := service.Call(t.Context(), "owner", "run", "attempt", input)
	if err != nil || replay.Error != nil || executed.Load() != 1 {
		t.Fatalf("replay spent quota or executed: %+v %v %d", replay, err, executed.Load())
	}
	var wg sync.WaitGroup
	results := make(chan error, 4)
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			call := input
			call.CommandID = fmt.Sprintf("concurrent-%d", i)
			_, err := service.Call(t.Context(), "owner", "run", "attempt", call)
			results <- err
		}(i)
	}
	wg.Wait()
	close(results)
	accepted := 0
	for err := range results {
		if err == nil {
			accepted++
		} else {
			requireControlError(t, err, "TOOL_BUDGET_EXHAUSTED")
		}
	}
	if accepted != 1 || executed.Load() != 2 {
		t.Fatalf("concurrent budget overrun: accepted=%d actual=%d", accepted, executed.Load())
	}
	_, err = service.Call(t.Context(), "owner", "run", "attempt", input)
	if err != nil || executed.Load() != 2 {
		t.Fatalf("budget exhausted blocked existing receipt: %v", err)
	}
}
