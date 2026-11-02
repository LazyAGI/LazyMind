package workflow

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/mux"

	"lazymind/core/algo"
	"lazymind/core/asyncjob"
	"lazymind/core/common/orm"
)

func TestWorkflowRepairCorrectsRejectedCandidateWithoutApplyingIntermediateChanges(t *testing.T) {
	for _, outcome := range []string{"corrected", "still_invalid", "weakened_input", "canceled", "needs_input", "provider_failure"} {
		t.Run(outcome, func(t *testing.T) {
			db := newHandlerTestDB(t)
			base, valid := nodeIntegrationFixture()
			base.StateYAML = strings.Replace(base.StateYAML, "gather: [{to: export}]", "gather: [{to: export, when: report ready}]", 1)
			valid.StateYAML = strings.Replace(valid.StateYAML, "gather: [{to: review}]", "gather: [{to: review, when: report ready}]", 1)
			invalid := valid
			invalid.StateYAML = strings.Replace(valid.StateYAML, "when: report ready", "when: different condition", 1)
			if outcome == "weakened_input" {
				// The second candidate must not inherit the first candidate's
				// weakened contract as its new protection baseline.
				invalid.StateYAML = strings.ReplaceAll(invalid.StateYAML, "required: true", "required: false")
			}
			draft := orm.WorkflowDraft{ID: uuid.NewString(), CreatedBy: "owner", Version: 4, GenerateStatus: "repairing", WorkflowYAMLContent: base.WorkflowYAML, StateYAMLContent: base.StateYAML, ScenarioContent: base.ScenarioMD, ScriptsContent: "{}"}
			run := orm.WorkflowRepairRun{ID: uuid.NewString(), DraftID: draft.ID, UserID: "owner", DraftVersionBefore: 4, Status: "queued", Target: "statemachine", ChangesJSON: "{}", CreatedAt: time.Now().UTC()}
			if err := db.Create(&draft).Error; err != nil {
				t.Fatal(err)
			}
			if err := db.Create(&run).Error; err != nil {
				t.Fatal(err)
			}
			calls := 0
			manualRetry := false
			var firstBudget float64
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				var request struct {
					Input struct {
						Data  map[string]json.RawMessage       `json:"data"`
						Files []struct{ Path, Content string } `json:"files"`
					} `json:"input"`
					Options struct {
						MaxRetries int     `json:"max_retries"`
						Budget     float64 `json:"total_timeout_seconds"`
					} `json:"options"`
				}
				if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
					t.Error(err)
					return
				}
				var saved orm.WorkflowDraft
				if err := db.First(&saved, "id = ?", draft.ID).Error; err != nil {
					t.Error(err)
				}
				if saved.Version != 4 || saved.StateYAMLContent != base.StateYAML {
					t.Error("intermediate candidate was applied")
				}
				candidate := invalid
				if calls == 1 {
					var repairContext map[string]json.RawMessage
					if err := json.Unmarshal(request.Input.Data["repair_context"], &repairContext); err != nil {
						t.Error(err)
					}
					if manualRetry && !strings.Contains(string(repairContext["previous_failure"]), "candidate") {
						t.Error("manual retry did not send the rejected candidate")
					}
					firstBudget = request.Options.Budget
					if request.Options.MaxRetries != 2 {
						t.Error("initial JSON retry budget changed")
					}
					if outcome == "provider_failure" {
						http.Error(w, "unavailable", http.StatusServiceUnavailable)
						return
					}
					if outcome == "needs_input" {
						_ = json.NewEncoder(w).Encode(map[string]any{"status": "succeeded", "output": map[string]any{"needs_input": "Specify the rejection destination"}})
						return
					}
					if outcome == "canceled" {
						if err := db.Model(&run).Update("status", "canceled").Error; err != nil {
							t.Error(err)
						}
						if err := db.Model(&draft).Updates(map[string]any{"generate_status": "done", "version": 5}).Error; err != nil {
							t.Error(err)
						}
					}
				} else {
					if calls > 2 {
						t.Error("unbounded semantic retries")
					}
					if request.Options.MaxRetries != 1 || request.Options.Budget > firstBudget {
						t.Error("correction multiplied the call or timeout budget")
					}
					if !strings.Contains(string(request.Input.Data["diagnostics"]), "E_REPAIR_BRANCH_CHANGED") || !strings.Contains(string(request.Input.Data["repair_context"]), "report ready") {
						t.Error("correction lost authoritative failure/original contracts")
					}
					if !slices.ContainsFunc(request.Input.Files, func(file struct{ Path, Content string }) bool {
						return file.Path == "scenario/state.yml" && file.Content == invalid.StateYAML
					}) {
						t.Error("correction edits are not relative to the candidate")
					}
					if outcome != "still_invalid" {
						candidate = valid
					}
					if outcome == "weakened_input" {
						candidate.StateYAML = strings.ReplaceAll(candidate.StateYAML, "required: true", "required: false")
					}
				}
				_ = json.NewEncoder(w).Encode(map[string]any{"status": "succeeded", "output": map[string]any{"workflow_yaml": candidate.WorkflowYAML, "state_yaml": candidate.StateYAML, "scenario_md": candidate.ScenarioMD}})
			}))
			defer server.Close()
			t.Setenv("LAZYMIND_CHAT_SERVICE_URL", server.URL)
			payload := workflowDraftRepairPayload{DraftID: draft.ID, UserID: "owner", Target: "statemachine", PreserveCurrentWorkflow: true, DraftVersion: 4, PrevStatus: "done", RepairRunID: run.ID, LLMConfig: map[string]any{}}
			body, _ := json.Marshal(payload)
			_, err := handleWorkflowDraftRepairJob(context.Background(), asyncjob.Job{PayloadJSON: body}, nil)
			if err := db.First(&draft, "id = ?", draft.ID).Error; err != nil {
				t.Fatal(err)
			}
			if err := db.First(&run, "id = ?", run.ID).Error; err != nil {
				t.Fatal(err)
			}
			if outcome == "corrected" {
				if err != nil || calls != 2 || run.Status != "succeeded" || draft.StateYAMLContent != valid.StateYAML || draft.Version != 5 {
					t.Fatalf("correction failed: calls=%d err=%v run=%+v draft=%+v", calls, err, run, draft)
				}
			} else {
				if err == nil || draft.StateYAMLContent != base.StateYAML {
					t.Fatal("rejected candidate was applied")
				}
				if outcome == "canceled" && (calls != 1 || run.Status != "canceled" || draft.Version != 5 || draft.GenerateStatus != "done") {
					t.Fatal("canceled owner called the model again or overwrote state")
				}
				if (outcome == "needs_input" || outcome == "provider_failure") && calls != 1 {
					t.Fatal("non-correctable failure retried")
				}
			}
			if outcome == "corrected" || outcome == "still_invalid" || outcome == "weakened_input" {
				var changes workflowRepairChanges
				if err := json.Unmarshal([]byte(run.ChangesJSON), &changes); err != nil {
					t.Fatal(err)
				}
				if len(changes.Attempts) != 2 || len(changes.Attempts[0].Changes) == 0 {
					t.Fatal("candidate differences or attempts were not retained")
				}
				if outcome != "corrected" {
					feedback, err := previousWorkflowRepairFeedback(db.DB, workflowDraftRepairPayload{DraftID: draft.ID, UserID: "owner", DraftVersion: 4, Target: "statemachine", RepairRunID: "next-run"}, base)
					if err != nil || feedback == nil || len(feedback.Diagnostics) == 0 {
						t.Fatalf("same-snapshot retry lost feedback: %v", err)
					}
					changed := base
					changed.ScenarioMD += "\nNew user instructions"
					feedback, err = previousWorkflowRepairFeedback(db.DB, workflowDraftRepairPayload{DraftID: draft.ID, UserID: "owner", DraftVersion: 4, Target: "statemachine", RepairRunID: "next-run"}, changed)
					if err != nil || feedback != nil {
						t.Fatal("stale candidate reused with different content")
					}
					manualRetry, calls = true, 0
					nextRun := orm.WorkflowRepairRun{ID: uuid.NewString(), DraftID: draft.ID, UserID: "owner", DraftVersionBefore: 4, Status: "queued", Target: "statemachine", ChangesJSON: "{}", CreatedAt: time.Now().UTC()}
					if err := db.Create(&nextRun).Error; err != nil {
						t.Fatal(err)
					}
					if err := db.Model(&draft).Update("generate_status", "repairing").Error; err != nil {
						t.Fatal(err)
					}
					payload.RepairRunID = nextRun.ID
					body, _ := json.Marshal(payload)
					if _, err := handleWorkflowDraftRepairJob(context.Background(), asyncjob.Job{PayloadJSON: body}, nil); err == nil {
						t.Fatal("invalid manual retry was accepted")
					}
					if calls != 2 {
						t.Fatal("manual retry lost its bounded correction")
					}
				}
			}
		})
	}
}

func TestWorkflowRepairInputCorrectionRequiresExactAuthorization(t *testing.T) {
	base, valid := nodeIntegrationFixture()
	base.StateYAML = strings.Replace(base.StateYAML, "inputs: [{material: report, required: true}]", "inputs: [{material: report_typo, required: true}]", 1)
	authorized := &algo.WorkflowEditContext{InputCorrections: []algo.WorkflowInputCorrection{{NodeID: "review", From: "report_typo", To: "report"}}}
	for _, tc := range []struct {
		name               string
		edit               *algo.WorkflowEditContext
		weaken, changeMode bool
		valid              bool
	}{
		{"not authorized", nil, false, false, false},
		{"exact correction", authorized, false, false, true},
		{"cannot remove requirement", authorized, true, false, false},
		{"cannot remove approval", authorized, false, true, false},
		{"cannot replace valid input", &algo.WorkflowEditContext{InputCorrections: []algo.WorkflowInputCorrection{{NodeID: "export", From: "report", To: "final"}}}, false, false, false},
		{"cannot invent replacement", &algo.WorkflowEditContext{InputCorrections: []algo.WorkflowInputCorrection{{NodeID: "review", From: "report_typo", To: "unknown"}}}, false, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			candidate := valid
			if tc.weaken {
				candidate.StateYAML = strings.ReplaceAll(candidate.StateYAML, "required: true", "required: false")
			}
			if tc.changeMode {
				candidate.StateYAML = strings.ReplaceAll(candidate.StateYAML, "mode: human", "mode: auto")
			}
			diagnostics := diagnoseAuthorizedNodeIntegration(base, candidate, tc.edit)
			if hasDiagnosticErrors(diagnostics) == tc.valid {
				t.Fatalf("authorization result invalid: %+v", diagnostics)
			}
		})
	}
}

func TestWorkflowRepairPreviewShowsGlobalBlockersAndUnknownInputChoices(t *testing.T) {
	db := newHandlerTestDB(t)
	_, base := nodeIntegrationFixture()
	base.StateYAML = strings.Replace(base.StateYAML, "inputs: [{material: report, required: true}]", "inputs: [{material: report_typo, required: true}]", 1)
	draft := orm.WorkflowDraft{ID: uuid.NewString(), CreatedBy: "owner", SourceType: "skill", Version: 4, GenerateStatus: "done", WorkflowYAMLContent: base.WorkflowYAML, StateYAMLContent: base.StateYAML, ScenarioContent: "TODO", ScriptsContent: "{}"}
	if err := db.Create(&draft).Error; err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"target":"statemachine","draft_version":4}`))
	r.Header.Set("X-User-Id", "owner")
	r = mux.SetURLVars(r, map[string]string{"draft_id": draft.ID})
	w := httptest.NewRecorder()
	PreviewWorkflowRepair(w, r)
	if w.Code != http.StatusOK {
		t.Fatal(w.Body.String())
	}
	var body struct {
		Data struct {
			Blockers    []repairDiagnostic              `json:"blocking_diagnostics"`
			Corrections []workflowInputCorrectionOption `json:"input_corrections"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if !slices.ContainsFunc(body.Data.Blockers, func(d repairDiagnostic) bool { return d.Code == "E_REPAIR_SCENARIO_INCOMPLETE" }) {
		t.Fatal("preview hid a publication blocker")
	}
	if len(body.Data.Corrections) != 1 || body.Data.Corrections[0].MaterialID != "report_typo" || !slices.Contains(body.Data.Corrections[0].Choices, "report") {
		t.Fatalf("missing explicit input correction: %+v", body.Data.Corrections)
	}
}
