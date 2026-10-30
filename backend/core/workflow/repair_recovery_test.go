package workflow

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/mux"

	"lazymind/core/asyncjob"
	"lazymind/core/common/orm"
)

func TestNodeIntegrationRecoveryReconcilesOnlyInterruptedOwner(t *testing.T) {
	for _, target := range []string{"node_integration", "statemachine", "full"} {
		t.Run(target, func(t *testing.T) {
			for _, outcome := range []string{"expired", "failed", "canceled", "running", "pending", "succeeded", "different_owner"} {
				t.Run(outcome, func(t *testing.T) {
					db := newHandlerTestDB(t)
					base, _ := nodeIntegrationFixture()
					now := time.Now().UTC()
					draft := orm.WorkflowDraft{ID: uuid.NewString(), CreatedBy: "owner", Version: 4, GenerateStatus: generateStatusRepairing, WorkflowYAMLContent: base.WorkflowYAML, StateYAMLContent: base.StateYAML, ScenarioContent: base.ScenarioMD, ScriptsContent: "{}"}
					run := orm.WorkflowRepairRun{ID: uuid.NewString(), DraftID: draft.ID, UserID: "owner", Target: target, Status: "repairing", CreatedAt: now}
					payload := workflowDraftRepairPayload{DraftID: draft.ID, RepairRunID: run.ID, Target: target, PreserveCurrentWorkflow: target != "node_integration", UserID: "owner", DraftVersion: 4, PrevStatus: "done"}
					job := orm.AsyncJob{ID: uuid.NewString(), JobType: workflowDraftRepairJobType, ResourceType: "workflow_draft", ResourceID: draft.ID, Status: outcome, AttemptCount: 1, MaxAttempts: 1, CreatedAt: now, UpdatedAt: now}
					switch outcome {
					case "expired":
						job.Status = string(asyncjob.StatusRunning)
						expired := now.Add(-time.Minute)
						job.LockUntil = &expired
					case "running":
						future := now.Add(time.Minute)
						job.LockUntil = &future
					case "different_owner":
						job.Status = string(asyncjob.StatusFailed)
						payload.RepairRunID = uuid.NewString()
					case "succeeded":
						run.Status, draft.GenerateStatus = "succeeded", "done"
					}
					job.PayloadJSON, _ = json.Marshal(payload)
					for _, row := range []any{&draft, &run, &job} {
						if err := db.Create(row).Error; err != nil {
							t.Fatal(err)
						}
					}
					// Simulate restart: queue recovery runs without the original handler.
					if err := asyncjob.RecoverStaleJobs(t.Context(), db.DB, now); err != nil {
						t.Fatal(err)
					}
					for i := 0; i < 2; i++ {
						if err := recoverWorkflowRepairs(t.Context(), db.DB); err != nil {
							t.Fatal(err)
						}
					}
					if err := db.First(&draft, "id=?", draft.ID).Error; err != nil {
						t.Fatal(err)
					}
					if err := db.First(&run, "id=?", run.ID).Error; err != nil {
						t.Fatal(err)
					}
					if draft.Version != 4 || draft.StateYAMLContent != base.StateYAML || draft.ScenarioContent != base.ScenarioMD {
						t.Fatal("recovery changed user content")
					}
					interrupted := outcome == "expired" || outcome == "failed" || outcome == "canceled"
					if interrupted {
						if draft.GenerateStatus != "done" || run.Status != "failed" || !strings.Contains(run.DiagnosticsAfterJSON, "E_REPAIR_INTERRUPTED") || !strings.HasPrefix(draft.GenerateWarning, "[修复失败]") {
							t.Fatalf("interrupted repair not recovered: draft=%+v run=%+v", draft, run)
						}
						req := httptest.NewRequest(http.MethodPut, "/", strings.NewReader(`{"version":4,"scenario_content":"Manual correction after interruption"}`))
						req.Header.Set("X-User-Id", "owner")
						req = mux.SetURLVars(req, map[string]string{"draft_id": draft.ID})
						rec := httptest.NewRecorder()
						SaveWorkflowDraft(rec, req)
						if rec.Code != http.StatusOK {
							t.Fatalf("recovered draft still locked: %s", rec.Body)
						}
					} else if outcome == "succeeded" {
						if draft.GenerateStatus != "done" || run.Status != "succeeded" {
							t.Fatal("recovery rewrote successful repair")
						}
					} else if draft.GenerateStatus != generateStatusRepairing || run.Status != "repairing" {
						t.Fatal("recovery unlocked an active or different repair")
					}
				})
			}
		})
	}
}

func TestCurrentWorkflowRecoveryDoesNotChangeUnprotectedRepairs(t *testing.T) {
	for _, target := range []string{"statemachine", "full"} {
		t.Run(target, func(t *testing.T) {
			db, draft, run, _ := seedCancelableRepair(t, target, "done", string(asyncjob.StatusFailed))
			if err := recoverWorkflowRepairs(t.Context(), db.DB); err != nil {
				t.Fatal(err)
			}
			if err := db.First(&draft, "id=?", draft.ID).Error; err != nil {
				t.Fatal(err)
			}
			if err := db.First(&run, "id=?", run.ID).Error; err != nil {
				t.Fatal(err)
			}
			if draft.GenerateStatus != generateStatusRepairing || run.Status != "queued" {
				t.Fatal("current-workflow recovery changed an unrelated repair")
			}
		})
	}
}
