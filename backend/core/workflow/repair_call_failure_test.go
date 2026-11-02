package workflow

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"lazymind/core/algo"
	"lazymind/core/asyncjob"
	"lazymind/core/common/orm"
)

func TestRepairCallFailureIsPersistedWithoutChangingDraft(t *testing.T) {
	for _, tc := range []struct {
		target   string
		preserve bool
		failAt   int
	}{
		{"node_integration", true, 1}, {"statemachine", true, 1}, {"full", true, 1},
		{"statemachine", false, 1}, {"full", false, 1}, {"scenario", false, 1}, {"ui", false, 1}, {"scripts", false, 1},
		{"full", false, 2}, {"full", false, 3},
	} {
		name := tc.target + "/ordinary"
		if tc.preserve {
			name = tc.target + "/current"
		}
		t.Run(name, func(t *testing.T) {
			db := newHandlerTestDB(t)
			base, candidate := nodeIntegrationFixture()
			draft := orm.WorkflowDraft{ID: uuid.NewString(), CreatedBy: "owner", Version: 4, GenerateStatus: generateStatusRepairing,
				WorkflowYAMLContent: base.WorkflowYAML, StateYAMLContent: base.StateYAML, ScenarioContent: base.ScenarioMD, ScriptsContent: "{}"}
			if err := db.Create(&draft).Error; err != nil {
				t.Fatal(err)
			}
			run := orm.WorkflowRepairRun{ID: uuid.NewString(), DraftID: draft.ID, UserID: "owner", Status: "queued", Target: tc.target}
			if err := db.Create(&run).Error; err != nil {
				t.Fatal(err)
			}
			calls := 0
			lastBudget := 1.8
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				var request struct {
					Options map[string]float64 `json:"options"`
				}
				if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
					t.Error(err)
				}
				budget := request.Options["total_timeout_seconds"]
				if budget <= 0 || budget > lastBudget {
					t.Errorf("repair phases reset their budget: %v after %v", budget, lastBudget)
				}
				lastBudget = budget
				if calls < tc.failAt {
					_ = json.NewEncoder(w).Encode(map[string]any{"status": "succeeded", "output": map[string]any{
						"state_yaml": candidate.StateYAML, "scenario_md": candidate.ScenarioMD,
					}})
					return
				}
				w.WriteHeader(http.StatusBadGateway)
				_ = json.NewEncoder(w).Encode(map[string]any{"detail": map[string]any{
					"error_code": "request_timeout", "diagnostic_id": "diag-test", "retryable": true,
					"error": "secret-provider-body",
				}})
			}))
			defer server.Close()
			t.Setenv("LAZYMIND_CHAT_SERVICE_URL", server.URL)
			body, _ := json.Marshal(workflowDraftRepairPayload{DraftID: draft.ID, UserID: "owner", DraftVersion: 4,
				PrevStatus: "done", Target: tc.target, PreserveCurrentWorkflow: tc.preserve, RepairRunID: run.ID,
				LLMConfig: map[string]any{"llm": map[string]any{"model": "test"}}})
			ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
			defer cancel()
			_, err := handleWorkflowDraftRepairJob(ctx, asyncjob.Job{PayloadJSON: body}, nil)
			if err == nil || calls != tc.failAt {
				t.Fatalf("expected failure at call %d, got %d: %v", tc.failAt, calls, err)
			}
			var saved orm.WorkflowDraft
			if err := db.First(&saved, "id=?", draft.ID).Error; err != nil {
				t.Fatal(err)
			}
			if err := db.First(&run, "id=?", run.ID).Error; err != nil {
				t.Fatal(err)
			}
			if saved.Version != 4 || saved.GenerateStatus != "done" || saved.StateYAMLContent != base.StateYAML || saved.WorkflowYAMLContent != base.WorkflowYAML || saved.ScenarioContent != base.ScenarioMD {
				t.Fatalf("failed call changed draft: %+v", saved)
			}
			if run.Status != "failed" || !strings.Contains(run.DiagnosticsAfterJSON, "E_REPAIR_MODEL_TIMEOUT") || !strings.Contains(run.DiagnosticsAfterJSON, "diag-test") || !strings.Contains(saved.GenerateWarning, "超时") {
				t.Fatalf("lost failure feedback: %+v warning=%s", run, saved.GenerateWarning)
			}
			if strings.Contains(run.DiagnosticsAfterJSON+saved.GenerateWarning, "secret-provider-body") {
				t.Fatal("provider body leaked into failure feedback")
			}
		})
	}
}

func TestRepairCallDiagnosticClassification(t *testing.T) {
	for code, diagnostic := range map[string]string{
		"transport_error": "E_REPAIR_MODEL_TRANSPORT", "authentication_failed": "E_REPAIR_MODEL_CONFIG",
		"rate_limited": "E_REPAIR_MODEL_LIMIT", "quota_exhausted": "E_REPAIR_MODEL_LIMIT",
		"invalid_model_output": "E_REPAIR_MODEL_OUTPUT", "provider_rejected": "E_REPAIR_MODEL_FAILED",
	} {
		if got := workflowRepairCallDiagnostic(&algo.WorkflowTaskError{Code: code}); got.Code != diagnostic {
			t.Errorf("%s: got %+v, want %s", code, got, diagnostic)
		}
	}
}
