package workflow

import (
	"context"
	"encoding/json"
	"io"
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

type repairTestTransport func(*http.Request) (*http.Response, error)

func (f repairTestTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func seedCancelableRepair(t *testing.T, target, previousStatus, jobStatus string) (*orm.DB, orm.WorkflowDraft, orm.WorkflowRepairRun, asyncjob.Job) {
	t.Helper()
	db := newHandlerTestDB(t)
	base, _ := nodeIntegrationFixture()
	now := time.Now().UTC()
	draft := orm.WorkflowDraft{ID: uuid.NewString(), CreatedBy: "owner", Version: 4, GenerateStatus: generateStatusRepairing, GenerateError: "original error", GenerateWarning: "original warning", WorkflowYAMLContent: base.WorkflowYAML, StateYAMLContent: base.StateYAML, ScenarioContent: base.ScenarioMD, ScriptsContent: "{}", CreatedAt: now, UpdatedAt: now}
	run := orm.WorkflowRepairRun{ID: uuid.NewString(), DraftID: draft.ID, UserID: "owner", DraftVersionBefore: draft.Version, Target: target, Status: "queued", CreatedAt: now, UpdatedAt: now}
	payload := workflowDraftRepairPayload{DraftID: draft.ID, UserID: "owner", DraftVersion: draft.Version, PrevStatus: previousStatus, Target: target, RepairRunID: run.ID, LLMConfig: map[string]any{"llm": map[string]any{"model": "test"}}}
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	lockUntil := now.Add(time.Minute)
	job := orm.AsyncJob{ID: uuid.NewString(), JobType: workflowDraftRepairJobType, ResourceType: "workflow_draft", ResourceID: draft.ID, PayloadJSON: body, Status: jobStatus, AttemptCount: 1, MaxAttempts: 1, LockedBy: "worker", LockUntil: &lockUntil, CreatedAt: now, UpdatedAt: now}
	for _, row := range []any{&draft, &run, &job} {
		if err := db.Create(row).Error; err != nil {
			t.Fatal(err)
		}
	}
	return db, draft, run, asyncjob.Job{ID: job.ID, PayloadJSON: body}
}

func cancelRepairRequest(draftID, userID string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/workflow-drafts/"+draftID+":cancel-repair", nil)
	req = mux.SetURLVars(req, map[string]string{"draft_id": draftID})
	req.Header.Set("X-User-Id", userID)
	rec := httptest.NewRecorder()
	CancelWorkflowDraftRepair(rec, req)
	return rec
}

func assertRepairCanceled(t *testing.T, db *orm.DB, original orm.WorkflowDraft, runID, jobID, previousStatus string) {
	t.Helper()
	var draft orm.WorkflowDraft
	var run orm.WorkflowRepairRun
	var job orm.AsyncJob
	if err := db.First(&draft, "id = ?", original.ID).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.First(&run, "id = ?", runID).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.First(&job, "id = ?", jobID).Error; err != nil {
		t.Fatal(err)
	}
	if draft.GenerateStatus != previousStatus || draft.Version != original.Version+1 || draft.GenerateError != original.GenerateError || draft.GenerateWarning != original.GenerateWarning || draft.WorkflowYAMLContent != original.WorkflowYAMLContent || draft.StateYAMLContent != original.StateYAMLContent || draft.ScenarioContent != original.ScenarioContent || draft.ScriptsContent != original.ScriptsContent {
		t.Fatalf("cancellation or late response changed draft content/state: %+v", draft)
	}
	if run.Status != "canceled" || job.Status != string(asyncjob.StatusCanceled) || job.LockedBy != "" || job.LockUntil != nil {
		t.Fatalf("repair owner/job not retired: run=%s job=%+v", run.Status, job)
	}
}

func TestCancelWorkflowDraftRepairFencesQueuedAndRunningJobs(t *testing.T) {
	for _, target := range []string{"node_integration", "statemachine", "ui", "scenario", "scripts", "full"} {
		for _, previousStatus := range []string{generateStatusDone, generateStatusFailed} {
			for _, status := range []string{string(asyncjob.StatusPending), string(asyncjob.StatusRunning)} {
				t.Run(target+"/"+previousStatus+"/"+status, func(t *testing.T) {
					db, draft, run, job := seedCancelableRepair(t, target, previousStatus, status)
					if rec := cancelRepairRequest(draft.ID, "other-user"); rec.Code != http.StatusNotFound {
						t.Fatalf("other user canceled repair: %d", rec.Code)
					}
					if rec := cancelRepairRequest(draft.ID, "owner"); rec.Code != http.StatusOK {
						t.Fatalf("cancel failed: %d %s", rec.Code, rec.Body.String())
					}
					// A queue runner that already captured the payload cannot resurrect it.
					if _, err := handleWorkflowDraftRepairJob(context.Background(), job, nil); err == nil {
						t.Fatal("retired repair executed")
					}
					assertRepairCanceled(t, db, draft, run.ID, job.ID, previousStatus)
					if rec := cancelRepairRequest(draft.ID, "owner"); rec.Code != http.StatusConflict {
						t.Fatalf("terminal repair was canceled again: %d", rec.Code)
					}
				})
			}
		}
	}
}

func TestCancelWorkflowDraftRepairRejectsLateModelResults(t *testing.T) {
	for _, scope := range []struct {
		target   string
		preserve bool
	}{
		{"node_integration", false}, {"ui", false}, {"scenario", false}, {"scripts", false}, {"full", false},
		{"statemachine", true}, {"full", true},
	} {
		target := scope.target
		for _, modelFails := range []bool{false, true} {
			name := target + "/success"
			if modelFails {
				name = target + "/error"
			}
			if scope.preserve {
				name += "/current_workflow"
			}
			t.Run(name, func(t *testing.T) {
				db, draft, run, job := seedCancelableRepair(t, target, generateStatusDone, string(asyncjob.StatusRunning))
				if scope.preserve {
					var payload workflowDraftRepairPayload
					if err := json.Unmarshal(job.PayloadJSON, &payload); err != nil {
						t.Fatal(err)
					}
					payload.PreserveCurrentWorkflow = true
					var err error
					job.PayloadJSON, err = json.Marshal(payload)
					if err != nil {
						t.Fatal(err)
					}
					if err := db.Model(&orm.AsyncJob{}).Where("id=?", job.ID).Update("payload_json", job.PayloadJSON).Error; err != nil {
						t.Fatal(err)
					}
				}
				_, candidate := nodeIntegrationFixture()
				canceled := false
				transport := http.DefaultTransport
				t.Cleanup(func() { http.DefaultTransport = transport })
				http.DefaultTransport = repairTestTransport(func(r *http.Request) (*http.Response, error) {
					if !canceled {
						canceled = true
						if rec := cancelRepairRequest(draft.ID, "owner"); rec.Code != http.StatusOK {
							t.Errorf("cancel while model runs: %d %s", rec.Code, rec.Body.String())
						}
					}
					if modelFails {
						return &http.Response{StatusCode: http.StatusBadRequest, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("provider failure after cancellation")), Request: r}, nil
					}
					body, err := json.Marshal(map[string]any{"status": "succeeded", "output": map[string]any{"workflow_yaml": candidate.WorkflowYAML, "state_yaml": candidate.StateYAML, "scenario_md": candidate.ScenarioMD}})
					if err != nil {
						return nil, err
					}
					return &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(string(body))), Request: r}, nil
				})
				t.Setenv("LAZYMIND_CHAT_SERVICE_URL", "http://repair-test.invalid")
				_, _ = handleWorkflowDraftRepairJob(context.Background(), job, nil)
				if !canceled {
					t.Fatal("model request was not exercised")
				}
				assertRepairCanceled(t, db, draft, run.ID, job.ID, generateStatusDone)
			})
		}
	}
}
