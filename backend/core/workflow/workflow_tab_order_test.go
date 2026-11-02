package workflow

import (
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/gorilla/mux"
	"gopkg.in/yaml.v3"

	"lazymind/core/common/orm"
	"lazymind/core/workflow/graphengine"
)

const orderedWorkflowState = `steps:
  compose_report:
    mode: human
    prompt: Read the copy and review and save the final report.
    inputs: [{material: copy, required: true}, {material: review_result, required: true}]
    outputs: [final_report]
  prepare:
    mode: human
    prompt: Prepare and save the brief.
    outputs: [brief]
  review:
    mode: human
    prompt: Read the copy and save the review result.
    inputs: [{material: copy, required: true}]
    outputs: [review_result]
  write:
    mode: human
    prompt: Read the brief and save the copy.
    inputs: [{material: brief, required: true}]
    outputs: [copy]
transitions:
  __start__: [{to: prepare}]
  prepare: [{to: write}]
  write: [{to: review}]
  review: [{to: compose_report}]
  compose_report: [{to: __end__}]
`

const unorderedWorkflowDeclarations = `id: tab-order-smoke
name: Tab order smoke
steps: [{id: compose_report}, {id: prepare}, {id: write}, {id: review}]
slots: [{id: brief, type: text}, {id: copy, type: text}, {id: review_result, type: text}, {id: final_report, type: text}]
`

func workflowTabStepIDs(t *testing.T, content string) []string {
	t.Helper()
	var doc map[string]any
	if err := yaml.Unmarshal([]byte(content), &doc); err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, tab := range listValue(mapValue(doc["ui"])["tabs"]) {
		ids = append(ids, scalarAny(mapValue(tab)["step_id"]))
	}
	return ids
}

func TestTabAlignmentSeparatesDeclarationsFlowAndPresentationOrder(t *testing.T) {
	for _, tc := range []struct {
		name, tabs string
		want       []string
	}{
		{"explicit flow order", `[{id: prepare, slots: [brief]}, {id: write, slots: [copy]}, {id: review, slots: [review_result]}, {id: compose_report, label: Results, slots: [final_report]}]`, []string{"prepare", "write", "review", "compose_report"}},
		{"missing added node", `[{id: prepare, slots: [brief]}, {id: write, slots: [copy]}, {id: compose_report, label: Results, slots: [final_report]}]`, []string{"prepare", "write", "review", "compose_report"}},
		{"result-only legacy UI", `[{id: results, label: Results, slots: [final_report]}]`, []string{"prepare", "write", "review", "compose_report"}},
		{"no UI", `[]`, []string{"prepare", "write", "review", "compose_report"}},
		{"explicit result-first UI", `[{id: compose_report, label: Results, slots: [final_report]}, {id: prepare, slots: [brief]}, {id: write, slots: [copy]}, {id: review, slots: [review_result]}]`, []string{"compose_report", "prepare", "write", "review"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			before := unorderedWorkflowDeclarations + "ui:\n  tabs: " + tc.tabs + "\n"
			aligned, _, err := alignWorkflowUITabsWithStateSteps(before, orderedWorkflowState)
			if err != nil {
				t.Fatal(err)
			}
			if got := workflowTabStepIDs(t, aligned); !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("tabs=%v want=%v\n%s", got, tc.want, aligned)
			}
			original := graphengine.Compile(before, orderedWorkflowState, "", graphengine.ProfileEditor)
			result := graphengine.Compile(aligned, orderedWorkflowState, "", graphengine.ProfilePublish)
			if !result.Valid || original.Graph.GraphHash != result.Graph.GraphHash {
				t.Fatalf("alignment altered the runtime graph or failed validation: %+v", result.Diagnostics)
			}
			if tc.tabs != "[]" && !strings.Contains(aligned, "label: Results") {
				t.Fatal("lost the custom result label")
			}
			if again, changed, err := alignWorkflowUITabsWithStateSteps(aligned, orderedWorkflowState); err != nil || changed || again != aligned {
				t.Fatalf("alignment is not idempotent: changed=%v err=%v", changed, err)
			}
		})
	}
}

func TestTabAlignmentPreservesParallelBranchDisplayOrder(t *testing.T) {
	state := strings.ReplaceAll(orderedWorkflowState, "prepare: [{to: write}]", "prepare: [{to: write}, {to: review}]")
	state = strings.ReplaceAll(state, "write: [{to: review}]", "write: [{to: compose_report}]")
	before := unorderedWorkflowDeclarations + `ui:
  tabs: [{id: review, label: Review branch first, slots: [review_result]}, {id: write, slots: [copy]}, {id: compose_report, slots: [final_report]}]
`
	aligned, _, err := alignWorkflowUITabsWithStateSteps(before, state)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"prepare", "review", "write", "compose_report"}
	if got := workflowTabStepIDs(t, aligned); !reflect.DeepEqual(got, want) {
		t.Fatalf("branch display order=%v want=%v", got, want)
	}
}

func TestPublishKeepsRepairedTabOrderInImmutableRevision(t *testing.T) {
	db := newHandlerTestDB(t)
	workflow := unorderedWorkflowDeclarations + `ui:
  tabs: [{id: prepare, slots: [brief]}, {id: write, slots: [copy]}, {id: review, slots: [review_result]}, {id: compose_report, label: Results, slots: [final_report]}]
`
	draft := orm.WorkflowDraft{ID: uuid.NewString(), Name: "Tab order smoke", CreatedBy: "owner", SourceType: "ai", GenerateStatus: "done", Version: 1,
		WorkflowYAMLContent: workflow, StateYAMLContent: orderedWorkflowState, ScriptsContent: "{}",
		ScenarioContent: "### prepare\nPrepare the brief output.\n\n### write\nRead the brief and save the copy output.\n\n### review\nRead the copy input and save review_result.\n\n### compose_report\nRead copy and review_result and save final_report.",
	}
	if err := db.Create(&draft).Error; err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/", nil)
	req.Header.Set("X-User-Id", "owner")
	req = mux.SetURLVars(req, map[string]string{"draft_id": draft.ID})
	rec := httptest.NewRecorder()
	PublishWorkflowDraft(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("publish=%d %s", rec.Code, rec.Body)
	}
	var revision orm.WorkflowRevision
	if err := db.First(&revision).Error; err != nil {
		t.Fatal(err)
	}
	var entry orm.WorkflowRevisionEntry
	if err := db.First(&entry, "revision_id=? AND path=?", revision.ID, "workflow.yaml").Error; err != nil {
		t.Fatal(err)
	}
	var blob orm.WorkflowBlob
	if err := db.First(&blob, "hash=?", entry.BlobHash).Error; err != nil {
		t.Fatal(err)
	}
	want := []string{"prepare", "write", "review", "compose_report"}
	if got := workflowTabStepIDs(t, string(blob.Content)); !reflect.DeepEqual(got, want) {
		t.Fatalf("published tabs=%v want=%v", got, want)
	}
}
