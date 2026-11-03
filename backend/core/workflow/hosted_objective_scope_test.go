package workflow

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"lazymind/core/common/orm"
	"lazymind/core/store"
	"lazymind/core/workflow/executor"
	"lazymind/core/workflow/graphengine"
)

// This exercises the durable launch boundary, without calling a model.
func TestHostedObjectiveUsesPinnedStepOnLaunchAndRetry(t *testing.T) {
	db, hash := setupBatchTransitionSession(t)
	oldDB, oldState := store.DB(), store.State()
	store.Init(db.DB, db.DB, nil)
	t.Cleanup(func() { store.Init(oldDB, oldDB, oldState) })
	var revision orm.WorkflowRevision
	if err := db.First(&revision, "id = ?", "batch-revision").Error; err != nil {
		t.Fatal(err)
	}
	var graph graphengine.CompiledStateGraph
	if err := json.Unmarshal(revision.CompiledGraph, &graph); err != nil {
		t.Fatal(err)
	}
	node := graph.Nodes["branch_b"]
	node.Prompt = "Normalize metrics only; save normalized_metrics. Runtime context: {{user_input}}"
	node.Outputs, node.RequiredOutputs = []string{"normalized_metrics"}, []string{"normalized_metrics"}
	node.Acceptance = []string{"Compute changes before rounding."}
	graph.Nodes[node.ID] = node
	if err := db.Model(&revision).Update("compiled_graph", graph.JSON()).Error; err != nil {
		t.Fatal(err)
	}
	const input = "October vs September; CPA <= 20 USD; ultimately deliver a Markdown report."
	intent, _ := json.Marshal(map[string]string{"text": input})
	if err := db.Model(&orm.WorkflowSession{}).Where("id = ?", "batch-session").Update("intent_context", string(intent)).Error; err != nil {
		t.Fatal(err)
	}
	var session orm.WorkflowSession
	if err := db.First(&session, "id = ?", "batch-session").Error; err != nil {
		t.Fatal(err)
	}
	if err := advanceExternalStep(context.Background(), db.DB, orm.ExternalAgentWorkflowTask{ID: "hosted-task", TaskDescription: input}, session,
		projectionResponse{StateVersion: 4, GraphHash: hash}, node.ID); err != nil {
		t.Fatal(err)
	}
	var first orm.WorkflowSessionStep
	if err := db.Where("session_id = ? AND step_id = ?", session.ID, node.ID).First(&first).Error; err != nil {
		t.Fatal(err)
	}
	var task orm.SubAgentTask
	if err := db.First(&task, "id = ?", first.TaskID).Error; err != nil {
		t.Fatal(err)
	}
	want := workflowStepObjectiveWithRuntimeBoundaries(node.Prompt, "", input, nil, nil, nil)
	if task.Objective != want {
		t.Errorf("hosted launch added a whole-workflow objective: %q", task.Objective)
	}
	var params map[string]any
	if err := json.Unmarshal(task.Params, &params); err != nil {
		t.Fatal(err)
	}
	if params["user_input"] != input {
		t.Fatalf("business constraints lost: %v", params["user_input"])
	}
	ctx, err := (executor.DBContextLoader{DB: db.DB}).LoadAttemptContext(t.Context(), first.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(ctx.RequiredOutputs, node.RequiredOutputs) || !reflect.DeepEqual(ctx.Acceptance, node.Acceptance) {
		t.Fatalf("lost pinned contract: %+v", ctx)
	}
	if err := db.Model(&first).Update("status", StepStatusFailed).Error; err != nil {
		t.Fatal(err)
	}
	response, _ := transitionWorkflowSession(t.Context(), db.DB, session.ID, transitionCommandRequest{
		CommandID: "retry-scope", Operation: "advance", ExpectedStateVersion: 5, GraphHash: hash,
		Targets: []transitionTarget{{TargetStepID: node.ID}},
	})
	if !response.Accepted {
		t.Fatalf("retry rejected: %+v", response.Error)
	}
	var retried orm.SubAgentTask
	if err := db.First(&retried, "id = ?", response.TaskID).Error; err != nil {
		t.Fatal(err)
	}
	if retried.Objective != want {
		t.Fatalf("retry did not derive pinned prompt: %q", retried.Objective)
	}
	var retryStep orm.WorkflowSessionStep
	if err := db.First(&retryStep, "task_id = ?", response.TaskID).Error; err != nil {
		t.Fatal(err)
	}
	retryContext, err := (executor.DBContextLoader{DB: db.DB}).LoadAttemptContext(t.Context(), retryStep.ID)
	if err != nil {
		t.Fatal(err)
	}
	if retryStep.Attempt != 2 || retryContext.WorkflowRevision != "batch-revision" ||
		!reflect.DeepEqual(retryContext.RequiredOutputs, node.RequiredOutputs) || !reflect.DeepEqual(retryContext.Acceptance, node.Acceptance) {
		t.Fatalf("retry lost the pinned step contract: %+v", retryContext)
	}
	var historical orm.SubAgentTask
	if err := db.First(&historical, "id = ?", first.TaskID).Error; err != nil {
		t.Fatal(err)
	}
	if historical.Objective != task.Objective {
		t.Fatal("retry rewrote historical objective")
	}
	if err := db.First(&first, "id = ?", first.ID).Error; err != nil {
		t.Fatal(err)
	}
	if first.Validity != "stale" {
		t.Fatal("retry must invalidate old attempt")
	}
}

func TestHostedStepOutputContractRemainsStrict(t *testing.T) {
	for _, slot := range []string{"normalized_metrics", "performance_report"} {
		t.Run(slot, func(t *testing.T) {
			db := newTestDB(t)
			attempt := executor.AttemptContext{SessionID: "report-session", AttemptID: "report-attempt", StepID: slot,
				DeclaredOutputs: []string{slot}, RequiredOutputs: []string{slot}}
			if err := executor.ValidateRequiredOutputs(t.Context(), db.DB, attempt); err == nil {
				t.Fatal("missing required deliverable accepted")
			}
			if err := db.Create(&orm.WorkflowSlotRevision{ID: "output", SessionID: attempt.SessionID, SlotID: slot, Slot: slot,
				ProducerAttemptID: attempt.AttemptID, Validity: "effective", Selected: true, ContentSnapshot: json.RawMessage(`{"value":"report"}`)}).Error; err != nil {
				t.Fatal(err)
			}
			if err := executor.ValidateRequiredOutputs(t.Context(), db.DB, attempt); err != nil {
				t.Fatal(err)
			}
			// The final report must never be substituted by a prior step's metrics.
			attempt.RequiredOutputs = []string{"missing_final_report"}
			if err := executor.ValidateRequiredOutputs(t.Context(), db.DB, attempt); err == nil || !strings.Contains(err.Error(), "missing_final_report") {
				t.Fatalf("wrong artifact accepted: %v", err)
			}
		})
	}
}
