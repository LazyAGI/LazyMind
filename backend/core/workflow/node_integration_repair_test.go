package workflow

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/mux"
	"gorm.io/gorm"

	"lazymind/core/algo"
	"lazymind/core/asyncjob"
	"lazymind/core/common/orm"
	"lazymind/core/workflow/graphengine"
)

func TestCurrentWorkflowRepairPolicyKeepsScopeSeparate(t *testing.T) {
	for _, tc := range []struct {
		name, source, status, target string
		edit                         *algo.WorkflowEditContext
		preserve                     bool
	}{
		{"converted flow", "skill", "done", "statemachine", nil, true},
		{"converted full", "skill", "done", "full", nil, true},
		{"added node in AI flow", "ai", "done", "statemachine", &algo.WorkflowEditContext{NodeIDs: []string{"review"}}, true},
		{"added node in AI full", "ai", "done", "full", &algo.WorkflowEditContext{NodeIDs: []string{"review"}}, true},
		{"ordinary flow", "ai", "done", "statemachine", nil, false},
		{"ordinary full", "ai", "done", "full", nil, false},
		{"conversion recovery", "skill", "failed", "full", nil, false},
		{"UI scope", "skill", "done", "ui", &algo.WorkflowEditContext{NodeIDs: []string{"review"}}, false},
		{"legacy integration", "ai", "done", "node_integration", nil, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			draft := orm.WorkflowDraft{SourceType: tc.source, GenerateStatus: tc.status}
			if got := shouldPreserveCurrentWorkflow(draft, tc.target, tc.edit); got != tc.preserve {
				t.Fatalf("preserve=%v, want %v", got, tc.preserve)
			}
		})
	}
}

func nodeIntegrationFixture() (workflowRepairSnapshot, workflowRepairSnapshot) {
	base := workflowRepairSnapshot{
		WorkflowYAML: `id: review_flow
name: Review flow
steps: [{id: gather}, {id: review}, {id: export}]
slots: [{id: report, type: text}, {id: final, type: text}]
ui:
  tabs: [{id: result, label: Result, layout: vertical, slots: [{id: final}]}]
`,
		StateYAML: `steps:
  - {id: gather, mode: auto, prompt: Generate a report, outputs: [report]}
  - {id: review, mode: human, prompt: Review the report before export, inputs: [{material: report, required: true}]}
  - {id: export, mode: auto, prompt: Export the reviewed report, inputs: [{material: report, required: true}], outputs: [final]}
transitions:
  __start__: [{to: gather}]
  gather: [{to: export}]
  export: [{to: __end__}]
`,
		ScenarioMD: "### gather\nGenerate the report and store its contents in the report output.\n\n### export\nRead the report and export its contents to the final output.",
		Scripts:    map[string]string{},
	}
	candidate := base
	candidate.StateYAML = strings.Replace(base.StateYAML, "gather: [{to: export}]", "gather: [{to: review}]\n  review: [{to: export}]", 1)
	candidate.ScenarioMD += "\n\n### review\nRead the report input and wait for the user to approve it before continuing to export."
	return base, candidate
}

func TestNodeIntegrationCandidateContracts(t *testing.T) {
	base, valid := nodeIntegrationFixture()
	if diagnostics := diagnoseNodeIntegration(base, valid); hasDiagnosticErrors(diagnostics) {
		t.Fatalf("valid integration rejected: %+v", diagnostics)
	}
	for _, tc := range []struct {
		name, code string
		mutate     func(*workflowRepairSnapshot)
	}{
		{"drop new node", "E_REPAIR_NODE_REMOVED", func(c *workflowRepairSnapshot) {
			c.StateYAML = strings.ReplaceAll(c.StateYAML, "  - {id: review, mode: human, prompt: Review the report before export, inputs: [{material: report, required: true}]}\n", "")
		}},
		{"remove approval", "E_REPAIR_MODE_CHANGED", func(c *workflowRepairSnapshot) {
			c.StateYAML = strings.ReplaceAll(c.StateYAML, "mode: human", "mode: auto")
		}},
		{"weaken input", "E_REPAIR_INPUT_CHANGED", func(c *workflowRepairSnapshot) {
			c.StateYAML = strings.ReplaceAll(c.StateYAML, "required: true", "required: false")
		}},
		{"missing edge", "E_NODE_UNREACHABLE", func(c *workflowRepairSnapshot) { c.StateYAML = base.StateYAML }},
		{"missing instructions", "E_REPAIR_SCENARIO_INCOMPLETE", func(c *workflowRepairSnapshot) { c.ScenarioMD = base.ScenarioMD }},
		{"missing script", "W_TOOL_SCRIPT_MISSING", func(c *workflowRepairSnapshot) {
			c.WorkflowYAML += "tool_scripts: [{path: scripts/missing.py, functions: [run]}]\n"
		}},
		{"invalid UI", "E_UI_MATERIAL_UNKNOWN", func(c *workflowRepairSnapshot) {
			c.WorkflowYAML = strings.Replace(c.WorkflowYAML, "slots: [{id: final}]", "slots: [{id: absent}]", 1)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			candidate := valid
			tc.mutate(&candidate)
			diagnostics := diagnoseNodeIntegration(base, candidate)
			if !slices.ContainsFunc(diagnostics, func(d repairDiagnostic) bool { return d.Code == tc.code && d.Severity == "error" }) {
				t.Fatalf("missing %s: %+v", tc.code, diagnostics)
			}
		})
	}
}

func TestNodeIntegrationPreservesConditionalDestinations(t *testing.T) {
	base := workflowRepairSnapshot{
		WorkflowYAML: `id: branch_probe
name: Branch probe
steps: [{id: gather}, {id: choose}, {id: review}, {id: accept}, {id: reject}]
slots: [{id: report, type: text}]
`,
		StateYAML: `steps:
  gather: {mode: auto, prompt: Generate the report, outputs: [report]}
  choose: {mode: auto, route: choice, prompt: Select the correct decision branch}
  review: {mode: human, prompt: Review the report, inputs: [{material: report, required: true}]}
  accept: {mode: auto, prompt: Deliver accepted result}
  reject: {mode: auto, prompt: Handle rejected result}
transitions:
  __start__: [{to: gather}]
  gather: [{to: choose}]
  choose:
    - {to: accept, when: review passed}
    - {to: reject, when: review failed}
  accept: [{to: __end__}]
  reject: [{to: __end__}]
`,
		ScenarioMD: "### gather\nGenerate a report with meaningful source material.\n### choose\nSelect the branch matching the actual review outcome.\n### accept\nDeliver the accepted result to the user accurately.\n### reject\nHandle the rejected result without claiming success.",
		Scripts:    map[string]string{},
	}
	valid := base
	valid.StateYAML = strings.Replace(base.StateYAML, "gather: [{to: choose}]", "gather: [{to: review}]\n  review: [{to: choose}]", 1)
	valid.ScenarioMD += "\n### review\nRead the report and request human approval before selecting a branch."
	if ds := diagnoseNodeIntegration(base, valid); hasDiagnosticErrors(ds) {
		t.Fatalf("control integration rejected: %+v", ds)
	}
	t.Run("allows inserting review inside the success branch", func(t *testing.T) {
		candidate := valid
		candidate.StateYAML = strings.Replace(base.StateYAML, "{to: accept, when: review passed}", "{to: review, when: review passed}", 1) + "  review: [{to: accept}]\n"
		if ds := diagnoseNodeIntegration(base, candidate); hasDiagnosticErrors(ds) {
			t.Fatalf("branch subdivision rejected: %+v", ds)
		}
	})
	t.Run("protects the default branch as well as named conditions", func(t *testing.T) {
		withDefault := base
		withDefault.StateYAML = strings.Replace(base.StateYAML, ", when: review failed", "", 1)
		candidate := valid
		candidate.StateYAML = strings.Replace(valid.StateYAML, ", when: review failed", "", 1)
		if ds := diagnoseNodeIntegration(withDefault, candidate); hasDiagnosticErrors(ds) {
			t.Fatalf("unchanged default branch rejected: %+v", ds)
		}
		candidate.StateYAML = strings.Replace(candidate.StateYAML, "- {to: reject}", "- {to: accept}", 1)
		if ds := diagnoseNodeIntegration(withDefault, candidate); !slices.ContainsFunc(ds, func(d repairDiagnostic) bool { return d.Code == "E_REPAIR_BRANCH_CHANGED" }) {
			t.Fatalf("redirected default branch accepted: %+v", ds)
		}
	})
	for _, tc := range []struct{ name, from, to string }{
		{"swapped targets", "{to: accept, when: review passed}\n    - {to: reject, when: review failed}", "{to: reject, when: review passed}\n    - {to: accept, when: review failed}"},
		{"condition changed", "when: review passed", "when: different decision"},
		{"success bypasses target", "{to: accept, when: review passed}", "{to: __end__, when: review passed}"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			candidate := valid
			candidate.StateYAML = strings.Replace(candidate.StateYAML, tc.from, tc.to, 1)
			ds := diagnoseNodeIntegration(base, candidate)
			if !slices.ContainsFunc(ds, func(d repairDiagnostic) bool { return d.Code == "E_REPAIR_BRANCH_CHANGED" && d.Severity == "error" }) {
				t.Fatalf("branch rewrite accepted: %+v", ds)
			}
		})
	}
}

func TestBranchDestinationDoesNotAcceptCyclesOrMergedBranches(t *testing.T) {
	protected := map[string]bool{"accept": true, "reject": true}
	for _, edges := range [][]graphengine.CompiledEdge{
		{{From: "inserted", To: "inserted"}},
		{{From: "inserted", To: "reject"}, {From: "reject", To: "accept"}},
		{{From: "inserted", To: "accept", When: "sometimes"}},
		{{From: "inserted", To: "accept"}, {From: "inserted", To: "reject"}},
	} {
		if preservesBranchDestination("inserted", "accept", edges, protected) {
			t.Fatalf("unsafe branch path accepted: %+v", edges)
		}
	}
}

func TestNodeIntegrationRuntimeWaitsForHumanReview(t *testing.T) {
	_, candidate := nodeIntegrationFixture()
	compiled := graphengine.Compile(candidate.WorkflowYAML, candidate.StateYAML, candidate.ScenarioMD, graphengine.ProfileRuntimeLoad)
	graph := compiled.Graph
	if graph == nil {
		t.Fatalf("compile failed: %+v", compiled.Diagnostics)
	}
	snapshot := graphengine.RuntimeSnapshot{
		Attempts:  []graphengine.AttemptFact{{StepID: "gather", Status: "succeeded", Validity: "effective"}},
		Materials: []graphengine.MaterialValue{{MaterialID: "report", RevisionID: "r1", Valid: true}},
	}
	projection := graphengine.Project(graph, snapshot)
	if !slices.Contains(projection.Ready, "review") || slices.Contains(projection.Ready, "export") || !projection.Nodes["review"].RequiresApproval {
		t.Fatalf("review was bypassed: %+v", projection)
	}
	// A failed no-output step cannot be treated as a completed approval gate.
	snapshot.Attempts = append(snapshot.Attempts, graphengine.AttemptFact{StepID: "review", Status: "failed", Validity: "effective"})
	projection = graphengine.Project(graph, snapshot)
	if projection.Completed || slices.Contains(projection.Ready, "export") {
		t.Fatalf("failed review released downstream work: %+v", projection)
	}
	snapshot.Attempts[len(snapshot.Attempts)-1].Status = "succeeded"
	projection = graphengine.Project(graph, snapshot)
	if !slices.Contains(projection.Ready, "export") {
		t.Fatalf("export not ready after approval: %+v", projection)
	}
	snapshot.Attempts = append(snapshot.Attempts, graphengine.AttemptFact{StepID: "export", Status: "succeeded", Validity: "effective"})
	snapshot.Materials = append(snapshot.Materials, graphengine.MaterialValue{MaterialID: "final", RevisionID: "r2", Valid: true})
	if projection = graphengine.Project(graph, snapshot); !projection.Completed {
		t.Fatalf("workflow did not finish: %+v", projection)
	}
}

func TestNodeIntegrationMayAddInputsWithoutWeakeningExistingOnes(t *testing.T) {
	base, candidate := nodeIntegrationFixture()
	candidate.WorkflowYAML = strings.Replace(candidate.WorkflowYAML, "slots: [{id: report, type: text}, {id: final, type: text}]", "slots: [{id: report, type: text}, {id: final, type: text}, {id: approval, type: text}]", 1)
	candidate.StateYAML = strings.Replace(candidate.StateYAML, "mode: human, prompt:", "mode: human, outputs: [approval], prompt:", 1)
	candidate.StateYAML = strings.Replace(candidate.StateYAML, "prompt: Export the reviewed report, inputs: [{material: report, required: true}]", "prompt: Export the reviewed report, inputs: [{material: report, required: true}, {material: approval, required: true}]", 1)
	if diagnostics := diagnoseNodeIntegration(base, candidate); hasDiagnosticErrors(diagnostics) {
		t.Fatalf("new dependency rejected: %+v", diagnostics)
	}
	graph := graphengine.Compile(candidate.WorkflowYAML, candidate.StateYAML, candidate.ScenarioMD, graphengine.ProfileRuntimeLoad).Graph
	snapshot := graphengine.RuntimeSnapshot{
		Attempts:  []graphengine.AttemptFact{{StepID: "gather", Status: "succeeded", Validity: "effective"}, {StepID: "review", Status: "succeeded", Validity: "effective"}},
		Materials: []graphengine.MaterialValue{{MaterialID: "report", RevisionID: "r1", Valid: true}},
	}
	if slices.Contains(graphengine.Project(graph, snapshot).Ready, "export") {
		t.Fatal("export must wait for the approval output")
	}
	snapshot.Materials = append(snapshot.Materials, graphengine.MaterialValue{MaterialID: "approval", RevisionID: "r2", Valid: true})
	if !slices.Contains(graphengine.Project(graph, snapshot).Ready, "export") {
		t.Fatal("approval output did not unblock export")
	}
	// An extra OR alternative would make the previously required report optional.
	candidate.StateYAML = strings.Replace(candidate.StateYAML, "{material: report, required: true}, {material: approval, required: true}", "{material: report, required: true, alternatives: [approval]}", 1)
	if !slices.ContainsFunc(diagnoseNodeIntegration(base, candidate), func(d repairDiagnostic) bool { return d.Code == "E_REPAIR_INPUT_CHANGED" }) {
		t.Fatal("a weaker OR input was accepted")
	}
}

func TestNodeIntegrationJobAppliesOnlyValidCurrentCandidates(t *testing.T) {
	for _, target := range []string{"node_integration", "statemachine", "full"} {
		t.Run(target, func(t *testing.T) {
			for _, previousStatus := range []string{"done", "failed"} {
				t.Run(previousStatus, func(t *testing.T) {
					for _, outcome := range []string{"success", "unchanged", "invalid", "invalid_ui", "needs_input", "invalid_script", "model_failure", "stale", "stale_before_start", "commit_failure"} {
						t.Run(outcome, func(t *testing.T) {
							db := newHandlerTestDB(t)
							base, candidate := nodeIntegrationFixture()
							// Exposed outputs must remain single-placement after the repair is
							// finalized for validation and publish, including no-output review steps.
							base.WorkflowYAML = strings.Replace(base.WorkflowYAML, "id: final, type: text", "id: final, type: text, exposed: true", 1)
							candidate.WorkflowYAML = base.WorkflowYAML
							if target == "full" && outcome == "success" {
								// Full scope also applies UI and script repairs, not just edges.
								candidate.WorkflowYAML = strings.Replace(candidate.WorkflowYAML, "label: Result", "label: Reviewed report", 1)
								candidate.Scripts = map[string]string{"scripts/check.py": "def run():\n    return True\n"}
							}
							if outcome == "unchanged" {
								base = candidate
							}
							draft := orm.WorkflowDraft{ID: uuid.NewString(), CreatedBy: "owner", SourceType: "skill", Version: 4, GenerateStatus: generateStatusRepairing, WorkflowYAMLContent: base.WorkflowYAML, StateYAMLContent: base.StateYAML, ScenarioContent: base.ScenarioMD, ScriptsContent: "{}"}
							seedSkillForWorkflowConversion(t, db, "owner", "review-skill", "# Review\nGenerate a report, review it, and export the approved report.")
							draft.SourceSkillID, draft.SourceSkillRevisionID, draft.SourceSkillTreeHash = "review-skill", "review-skill-rev", "tree-review-skill"
							if previousStatus == "failed" {
								draft.GenerateError = `{"phase":"validation","message":"generation validation failed"}`
							}
							if target == "full" && outcome == "success" {
								draft.SourceAnalysisID = uuid.NewString()
							}
							if err := db.Create(&draft).Error; err != nil {
								t.Fatal(err)
							}
							if draft.SourceAnalysisID != "" {
								if err := db.Create(&orm.WorkflowGenerationAnalysis{ID: draft.SourceAnalysisID, DraftID: draft.ID, UserID: "owner", ScriptReportJSON: "{}"}).Error; err != nil {
									t.Fatal(err)
								}
							}
							run := orm.WorkflowRepairRun{ID: uuid.NewString(), DraftID: draft.ID, UserID: "owner", Status: "queued", Target: target, CreatedAt: time.Now().UTC()}
							if err := db.Create(&run).Error; err != nil {
								t.Fatal(err)
							}
							if outcome == "commit_failure" {
								if err := db.Callback().Update().Before("gorm:update").Register("fail_node_repair_commit", func(tx *gorm.DB) {
									updates, ok := tx.Statement.Dest.(map[string]any)
									if ok && tx.Statement.Table == (orm.WorkflowRepairRun{}).TableName() && updates["status"] == "succeeded" {
										tx.AddError(errors.New("injected commit failure"))
									}
								}); err != nil {
									t.Fatal(err)
								}
								t.Cleanup(func() { db.Callback().Update().Remove("fail_node_repair_commit") })
							}
							server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
								var request struct {
									Input struct {
										Data map[string]json.RawMessage `json:"data"`
									} `json:"input"`
								}
								if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
									t.Error(err)
								}
								if string(request.Input.Data["target"]) != fmt.Sprintf("%q", target) || string(request.Input.Data["preserve_current_workflow"]) != "true" || !strings.Contains(string(request.Input.Data["edit_context"]), "review") {
									t.Errorf("lost integration context: %+v", request)
								}
								if outcome == "model_failure" {
									http.Error(w, "provider unavailable", http.StatusBadRequest)
									return
								}
								if outcome == "invalid" {
									candidate.StateYAML = strings.ReplaceAll(candidate.StateYAML, "mode: human", "mode: auto")
								}
								if outcome == "invalid_ui" {
									candidate.WorkflowYAML = strings.Replace(candidate.WorkflowYAML, "slots: [{id: final}]", "slots: []", 1)
								}
								if outcome == "stale" {
									if err := db.Model(&orm.WorkflowDraft{}).Where("id=?", draft.ID).Updates(map[string]any{"version": 5, "scenario_content": "Newer user edit"}).Error; err != nil {
										t.Error(err)
									}
								}
								output := map[string]any{"workflow_yaml": candidate.WorkflowYAML, "state_yaml": candidate.StateYAML, "scenario_md": candidate.ScenarioMD, "scripts": candidate.Scripts}
								if outcome == "invalid_script" {
									output["script_errors"] = []map[string]string{{"path": "scripts/review.py", "message": "invalid syntax at line 1"}}
								}
								if outcome == "needs_input" {
									output["needs_input"] = "请说明审核失败时应该进入哪个步骤"
								}
								_ = json.NewEncoder(w).Encode(map[string]any{"status": "succeeded", "output": output})
							}))
							defer server.Close()
							t.Setenv("LAZYMIND_CHAT_SERVICE_URL", server.URL)
							payload := workflowDraftRepairPayload{DraftID: draft.ID, UserID: "owner", DraftVersion: 4, PrevStatus: previousStatus, Target: target, PreserveCurrentWorkflow: target != "node_integration", RepairRunID: run.ID, LLMConfig: map[string]any{"llm": map[string]any{"model": "test"}}, EditContext: &algo.WorkflowEditContext{NodeIDs: []string{"review"}}}
							body, _ := json.Marshal(payload)
							if outcome == "stale_before_start" {
								if err := db.Model(&orm.WorkflowDraft{}).Where("id=?", draft.ID).Updates(map[string]any{"version": 5, "scenario_content": "Newer user edit"}).Error; err != nil {
									t.Fatal(err)
								}
							}
							_, err := handleWorkflowDraftRepairJob(t.Context(), asyncjob.Job{PayloadJSON: body}, nil)
							var saved orm.WorkflowDraft
							if db.First(&saved, "id=?", draft.ID).Error != nil {
								t.Fatal("draft missing")
							}
							if db.First(&run, "id=?", run.ID).Error != nil {
								t.Fatal("run missing")
							}
							if outcome == "success" || outcome == "unchanged" {
								if saved.GenerateStatus != "done" || saved.GenerateError != "" {
									t.Fatalf("validated repair did not clear generation failure: status=%s error=%s", saved.GenerateStatus, saved.GenerateError)
								}
								if err != nil || run.Status != "succeeded" || saved.Version != 5 || saved.StateYAMLContent != candidate.StateYAML || saved.ScenarioContent != candidate.ScenarioMD {
									t.Fatalf("valid candidate not committed: %v, %+v", err, saved)
								}
								if target == "full" && outcome == "success" && (saved.WorkflowYAMLContent != candidate.WorkflowYAML || !strings.Contains(saved.ScriptsContent, "scripts/check.py") || !strings.Contains(run.ChangesJSON, "scripts/check.py")) {
									t.Fatalf("full repair lost UI/script changes: draft=%+v run=%+v", saved, run)
								}
								req := httptest.NewRequest(http.MethodPost, "/", nil)
								req.Header.Set("X-User-Id", "owner")
								req = mux.SetURLVars(req, map[string]string{"draft_id": draft.ID})
								rec := httptest.NewRecorder()
								ValidateWorkflowDraft(rec, req)
								if rec.Code != http.StatusOK || strings.Contains(rec.Body.String(), "E_UI_MATERIAL_DUPLICATE") {
									t.Fatalf("repaired draft fails validation: %d %s", rec.Code, rec.Body.String())
								}
								rec = httptest.NewRecorder()
								PublishWorkflowDraft(rec, req)
								if rec.Code != http.StatusOK {
									t.Fatalf("repaired draft cannot publish: %d %s", rec.Code, rec.Body.String())
								}
							} else {
								if saved.GenerateStatus != previousStatus || saved.GenerateError != draft.GenerateError {
									t.Fatalf("failed repair changed generation state: status=%s error=%s", saved.GenerateStatus, saved.GenerateError)
								}
								if err == nil || saved.StateYAMLContent != base.StateYAML || run.Status == "succeeded" || !strings.Contains(saved.GenerateWarning, "[修复失败]") {
									t.Fatalf("failed candidate applied: %v, %+v", err, saved)
								}
								if strings.HasPrefix(outcome, "stale") && (saved.ScenarioContent != "Newer user edit" || run.Status != "stale") {
									t.Fatalf("overwrote newer draft: %+v", saved)
								}
								if !strings.HasPrefix(outcome, "stale") && saved.Version != 4 {
									t.Fatalf("failed repair changed version: %d", saved.Version)
								}
								if run.DiagnosticsAfterJSON == "[]" || run.DiagnosticsAfterJSON == "" {
									t.Fatal("failure has no diagnostics")
								}
							}
						})
					}
				})
			}
		})
	}
}

func TestNodeIntegrationAdmissionClaimsCurrentDraftAndBlocksOtherWriters(t *testing.T) {
	db := newHandlerTestDB(t)
	if err := db.AutoMigrate(&orm.UserSelectedModel{}, &orm.UserModelProviderGroupModel{}, &orm.UserModelProviderGroup{}); err != nil {
		t.Fatal(err)
	}
	base, _ := nodeIntegrationFixture()
	draft := orm.WorkflowDraft{ID: uuid.NewString(), CreatedBy: "owner", Version: 4, GenerateStatus: "done", WorkflowYAMLContent: base.WorkflowYAML, StateYAMLContent: base.StateYAML, ScenarioContent: base.ScenarioMD, ScriptsContent: "{}", SourceAnalysisID: "original-analysis"}
	if err := db.Create(&draft).Error; err != nil {
		t.Fatal(err)
	}
	request := func(handler http.HandlerFunc, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
		req.Header.Set("X-User-Id", "owner")
		req = mux.SetURLVars(req, map[string]string{"draft_id": draft.ID})
		recorder := httptest.NewRecorder()
		handler(recorder, req)
		return recorder
	}
	body := `{"target":"node_integration","mode":"source_aware","draft_version":4,"edit_context":{"node_ids":["review"]},"repair_hint":"Review before export"}`
	if result := request(AIRepairWorkflowDraft, body); result.Code != http.StatusOK {
		t.Fatalf("admission failed: %s", result.Body)
	}
	var job orm.AsyncJob
	if err := db.First(&job, "resource_id=?", draft.ID).Error; err != nil {
		t.Fatal(err)
	}
	var payload workflowDraftRepairPayload
	if err := json.Unmarshal(job.PayloadJSON, &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Mode != "workflow_local" || payload.DraftVersion != 4 || !slices.Equal(payload.EditContext.NodeIDs, []string{"review"}) {
		t.Fatalf("wrong edit context: %+v", payload)
	}
	if result := request(AIRepairWorkflowDraft, body); result.Code != http.StatusConflict {
		t.Fatalf("duplicate repair admitted: %s", result.Body)
	}
	if result := request(SaveWorkflowDraft, `{"version":4,"scenario_content":"Concurrent edit"}`); result.Code != http.StatusConflict {
		t.Fatalf("save during repair admitted: %s", result.Body)
	}
	if result := request(CancelWorkflowDraftGeneration, `{}`); result.Code != http.StatusConflict {
		t.Fatalf("generation cancellation stole the repair lock: %s", result.Body)
	}
	if result := request(AIGenerateWorkflowDraft, `{"description":"Regenerate this workflow"}`); result.Code != http.StatusConflict {
		t.Fatalf("generation stole the repair lock: %s", result.Body)
	}
	var count int64
	db.Model(&orm.WorkflowRepairRun{}).Where("draft_id=?", draft.ID).Count(&count)
	if count != 1 {
		t.Fatalf("expected one repair run, got %d", count)
	}
}

func TestNodeIntegrationSupersededResponseCannotWriteOrUnlock(t *testing.T) {
	for _, invalid := range []bool{false, true} {
		t.Run(fmt.Sprintf("invalid=%v", invalid), func(t *testing.T) {
			db := newHandlerTestDB(t)
			if err := db.AutoMigrate(&orm.UserSelectedModel{}, &orm.UserModelProviderGroupModel{}, &orm.UserModelProviderGroup{}); err != nil {
				t.Fatal(err)
			}
			base, candidate := nodeIntegrationFixture()
			draft := orm.WorkflowDraft{ID: uuid.NewString(), CreatedBy: "owner", Version: 4, GenerateStatus: generateStatusRepairing, WorkflowYAMLContent: base.WorkflowYAML, StateYAMLContent: base.StateYAML, ScenarioContent: base.ScenarioMD, ScriptsContent: "{}"}
			run := orm.WorkflowRepairRun{ID: uuid.NewString(), DraftID: draft.ID, UserID: "owner", Target: "node_integration", Status: "queued", CreatedAt: time.Now().UTC()}
			for _, row := range []any{&draft, &run} {
				if err := db.Create(row).Error; err != nil {
					t.Fatal(err)
				}
			}
			payload := workflowDraftRepairPayload{DraftID: draft.ID, UserID: "owner", DraftVersion: 4, PrevStatus: "done", Target: "node_integration", RepairRunID: run.ID, LLMConfig: map[string]any{"llm": map[string]any{"model": "test"}}}
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				// Recovery releases A while its provider response is still in flight.
				if err := finishNodeIntegrationFailure(db.DB, payload, "failed", "E_REPAIR_INTERRUPTED", "Interrupted"); err != nil {
					t.Error(err)
				}
				req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"target":"node_integration","draft_version":4,"repair_hint":"Review before export"}`))
				req.Header.Set("X-User-Id", "owner")
				req = mux.SetURLVars(req, map[string]string{"draft_id": draft.ID})
				rec := httptest.NewRecorder()
				AIRepairWorkflowDraft(rec, req)
				if rec.Code != http.StatusOK {
					t.Errorf("new owner admission: %s", rec.Body)
				}
				if invalid {
					candidate.StateYAML = strings.ReplaceAll(candidate.StateYAML, "mode: human", "mode: auto")
				}
				_ = json.NewEncoder(w).Encode(map[string]any{"status": "succeeded", "output": map[string]any{"workflow_yaml": candidate.WorkflowYAML, "state_yaml": candidate.StateYAML, "scenario_md": candidate.ScenarioMD}})
			}))
			defer server.Close()
			t.Setenv("LAZYMIND_CHAT_SERVICE_URL", server.URL)
			body, _ := json.Marshal(payload)
			for i := 0; i < 2; i++ {
				if _, err := handleWorkflowDraftRepairJob(t.Context(), asyncjob.Job{PayloadJSON: body}, nil); err == nil {
					t.Fatal("superseded repair succeeded")
				}
			}
			if calls != 1 {
				t.Fatalf("terminal run was resurrected: calls=%d", calls)
			}
			if err := db.First(&draft, "id=?", draft.ID).Error; err != nil {
				t.Fatal(err)
			}
			if err := db.First(&run, "id=?", run.ID).Error; err != nil {
				t.Fatal(err)
			}
			var next orm.WorkflowRepairRun
			if err := db.Where("draft_id=? AND id <> ?", draft.ID, run.ID).First(&next).Error; err != nil {
				t.Fatal(err)
			}
			if draft.GenerateStatus != generateStatusRepairing || draft.Version != 4 || draft.StateYAMLContent != base.StateYAML || draft.ScenarioContent != base.ScenarioMD || next.Status != "queued" || run.Status != "failed" || !strings.Contains(run.DiagnosticsAfterJSON, "E_REPAIR_INTERRUPTED") {
				t.Fatalf("old response changed new owner: draft=%+v old=%+v new=%+v", draft, run, next)
			}
		})
	}
}

func TestNodeIntegrationCompletedSkillRepairScopeSurvivesReload(t *testing.T) {
	for _, target := range []string{"statemachine", "full"} {
		t.Run(target, func(t *testing.T) {
			db := newHandlerTestDB(t)
			if err := db.AutoMigrate(&orm.UserSelectedModel{}, &orm.UserModelProviderGroupModel{}, &orm.UserModelProviderGroup{}); err != nil {
				t.Fatal(err)
			}
			_, base := nodeIntegrationFixture()
			draft := orm.WorkflowDraft{ID: uuid.NewString(), CreatedBy: "owner", Version: 4, GenerateStatus: "done", SourceType: "skill", SourceAnalysisID: "original-analysis", WorkflowYAMLContent: base.WorkflowYAML, StateYAMLContent: base.StateYAML, ScenarioContent: base.ScenarioMD, ScriptsContent: "{}"}
			if err := db.Create(&draft).Error; err != nil {
				t.Fatal(err)
			}
			for _, handler := range []http.HandlerFunc{PreviewWorkflowRepair, AIRepairWorkflowDraft} {
				req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(fmt.Sprintf(`{"target":%q,"mode":"source_aware","draft_version":4}`, target)))
				req.Header.Set("X-User-Id", "owner")
				req = mux.SetURLVars(req, map[string]string{"draft_id": draft.ID})
				rec := httptest.NewRecorder()
				handler(rec, req)
				if rec.Code != http.StatusOK {
					t.Fatalf("repair scope request failed: %s", rec.Body)
				}
				if strings.Contains(rec.Body.String(), `"planned_files"`) {
					var preview struct {
						Data struct {
							Target, Mode string
							PlannedFiles []string `json:"planned_files"`
						}
					}
					if err := json.Unmarshal(rec.Body.Bytes(), &preview); err != nil {
						t.Fatal(err)
					}
					if preview.Data.Target != target || preview.Data.Mode != "workflow_local" {
						t.Fatalf("preview merged repair scopes: %s", rec.Body)
					}
					if target == "full" && !slices.Contains(preview.Data.PlannedFiles, "workflow.yaml") {
						t.Fatal("full preview did not include the complete manifest")
					}
					if target == "statemachine" && (!slices.Contains(preview.Data.PlannedFiles, "workflow.yaml.steps") || slices.Contains(preview.Data.PlannedFiles, "workflow.yaml")) {
						t.Fatal("flow preview does not distinguish primary and linked changes")
					}
				}
			}
			var job orm.AsyncJob
			if err := db.First(&job, "resource_id=?", draft.ID).Error; err != nil {
				t.Fatal(err)
			}
			var payload workflowDraftRepairPayload
			if err := json.Unmarshal(job.PayloadJSON, &payload); err != nil {
				t.Fatal(err)
			}
			if payload.Target != target || !payload.PreserveCurrentWorkflow || payload.Mode != "workflow_local" || strings.Contains(payload.RepairHint, "Respect source analysis") {
				t.Fatalf("lost current-workflow contract: %+v", payload)
			}
		})
	}
}

func TestNodeIntegrationAdmissionRollsBackWhenQueueFails(t *testing.T) {
	db := newHandlerTestDB(t)
	if err := db.AutoMigrate(&orm.UserSelectedModel{}, &orm.UserModelProviderGroupModel{}, &orm.UserModelProviderGroup{}); err != nil {
		t.Fatal(err)
	}
	base, _ := nodeIntegrationFixture()
	draft := orm.WorkflowDraft{ID: uuid.NewString(), CreatedBy: "owner", Version: 4, GenerateStatus: "done", WorkflowYAMLContent: base.WorkflowYAML, StateYAMLContent: base.StateYAML, ScriptsContent: "{}"}
	if err := db.Create(&draft).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Callback().Create().Before("gorm:create").Register("fail_node_repair_queue", func(tx *gorm.DB) {
		if tx.Statement.Table == (orm.AsyncJob{}).TableName() {
			tx.AddError(errors.New("injected queue failure"))
		}
	}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Callback().Create().Remove("fail_node_repair_queue") })
	req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"target":"node_integration","draft_version":4,"edit_context":{"node_ids":["review"]}}`))
	req.Header.Set("X-User-Id", "owner")
	req = mux.SetURLVars(req, map[string]string{"draft_id": draft.ID})
	recorder := httptest.NewRecorder()
	AIRepairWorkflowDraft(recorder, req)
	if recorder.Code != http.StatusInternalServerError {
		t.Fatalf("expected enqueue failure: %s", recorder.Body)
	}
	if err := db.First(&draft, "id=?", draft.ID).Error; err != nil {
		t.Fatal(err)
	}
	var count int64
	db.Model(&orm.WorkflowRepairRun{}).Where("draft_id=?", draft.ID).Count(&count)
	if draft.GenerateStatus != "done" || draft.Version != 4 || count != 0 {
		t.Fatalf("partial admission persisted: status=%s version=%d runs=%d", draft.GenerateStatus, draft.Version, count)
	}
}

func TestNodeIntegrationAdmissionRejectsLockedAndStaleDrafts(t *testing.T) {
	db := newHandlerTestDB(t)
	base, _ := nodeIntegrationFixture()
	for _, status := range []string{"generating", "state_done", "repairing", "needs_confirmation", "done"} {
		draft := orm.WorkflowDraft{ID: uuid.NewString(), CreatedBy: "owner", Version: 4, GenerateStatus: status, WorkflowYAMLContent: base.WorkflowYAML, StateYAMLContent: base.StateYAML}
		if err := db.Create(&draft).Error; err != nil {
			t.Fatal(err)
		}
		version := 4
		if status == "done" {
			version = 3
		}
		body, _ := json.Marshal(map[string]any{"target": "node_integration", "draft_version": version})
		req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(string(body)))
		req.Header.Set("X-User-Id", "owner")
		req = mux.SetURLVars(req, map[string]string{"draft_id": draft.ID})
		recorder := httptest.NewRecorder()
		AIRepairWorkflowDraft(recorder, req)
		if recorder.Code != http.StatusConflict {
			t.Fatalf("%s accepted stale request: %s", status, recorder.Body)
		}
	}
}
