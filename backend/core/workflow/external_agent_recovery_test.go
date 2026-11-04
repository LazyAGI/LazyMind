package workflow

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"lazymind/core/common/orm"
	"lazymind/core/workflow/graphengine"
)

func seedRecoveredExternalTask(t *testing.T) (*orm.DB, orm.ExternalAgentWorkflowTask, orm.WorkflowSession) {
	t.Helper()
	db := newHandlerTestDB(t)
	if err := db.AutoMigrate(&orm.Conversation{}); err != nil {
		t.Fatal(err)
	}
	task := orm.ExternalAgentWorkflowTask{ID: "recovered-task", OwnerUserID: "owner", AgentType: "codex", IdempotencyKey: "recovery", SessionID: "recovered-session", ConversationID: "conversation", WorkflowRef: "user:owner:report", WorkflowRevisionID: "pinned-revision", Status: externalTaskStatusFailed, Stage: "execute", ErrorCode: "WORKFLOW_EXECUTION_FAILED", ErrorMessage: "original failure", RequestJSON: mustJSON(map[string]any{}), ResultSummaryJSON: mustJSON(map[string]any{}), ResultArtifactsJSON: mustJSON([]any{})}
	session := orm.WorkflowSession{ID: task.SessionID, CreateUserID: task.OwnerUserID, ConversationID: task.ConversationID, WorkflowID: "report", WorkflowRef: task.WorkflowRef, WorkflowRevisionID: task.WorkflowRevisionID, Status: "completed", OriginHost: "external-agent", ControllerHost: "lazymind", StateVersion: 7}
	graph := graphengine.CompiledStateGraph{SchemaVersion: graphengine.SchemaVersion, GraphHash: "recovery-graph", StartRoute: "all",
		Nodes:        map[string]graphengine.CompiledNode{"report": {ID: "report", Outputs: []string{"final_report"}, RequiredOutputs: []string{"final_report"}}},
		ControlEdges: []graphengine.CompiledEdge{{From: "__start__", To: "report"}, {From: "report", To: "__end__"}}}
	session.GraphHash, session.GraphSchemaVersion = graph.GraphHash, graph.SchemaVersion
	if err := db.Create(&orm.WorkflowRevision{ID: task.WorkflowRevisionID, WorkflowResourceID: "resource", CompiledGraph: graph.JSON(), GraphHash: graph.GraphHash, GraphSchemaVersion: graph.SchemaVersion}).Error; err != nil {
		t.Fatal(err)
	}
	history := orm.ChatHistory{ID: externalWorkflowHistoryID(task.ID), ConversationID: task.ConversationID, RunStatus: "failed", Result: "<!-- workflow-step-feedback:original -->original failure"}
	oldAttempt := orm.WorkflowSessionStep{ID: "old-failed", SessionID: task.SessionID, StepID: "report", Attempt: 1, TaskID: "failed-task", Status: "failed", Validity: "stale"}
	retry := orm.WorkflowSessionStep{ID: "successful-retry", SessionID: task.SessionID, StepID: "report", Attempt: 2, TaskID: "retry-task", Status: "succeeded", Validity: "effective"}
	artifact := orm.WorkflowSlotRevision{ID: "report-output", ProducerAttemptID: retry.ID, SessionID: task.SessionID, SlotID: "final_report", Slot: "final_report", Revision: 1, Selected: true, Validity: "effective", ContentSnapshot: mustJSON(map[string]any{"text": "Real saved report"})}
	for _, row := range []any{&task, &session, &history, &oldAttempt, &retry, &artifact} {
		if err := db.Create(row).Error; err != nil {
			t.Fatal(err)
		}
	}
	return db, task, session
}

func TestExternalTaskCollectsRecoveredSessionWithoutExecuting(t *testing.T) {
	db, task, session := seedRecoveredExternalTask(t)
	history := orm.ChatHistory{ID: externalWorkflowHistoryID(task.ID)}
	var beforeSteps []orm.WorkflowSessionStep
	var beforeArtifacts []orm.WorkflowSlotRevision
	db.Order("id").Find(&beforeSteps)
	db.Order("id").Find(&beforeArtifacts)
	before, _ := json.Marshal([]any{session, beforeSteps, beforeArtifacts})
	for i := 0; i < 2; i++ {
		if err := recoverExternalWorkflowTasks(context.Background(), db.DB); err != nil {
			t.Fatal(err)
		}
		if err := db.First(&task, "id=?", task.ID).Error; err != nil {
			t.Fatal(err)
		}
		if task.Status != externalTaskStatusSucceeded || task.ErrorCode != "" {
			t.Fatalf("completed recovered Session not collected: status=%s code=%s", task.Status, task.ErrorCode)
		}
		if len(decodeJSONList(task.ResultArtifactsJSON)) != 1 || !strings.Contains(string(task.ResultArtifactsJSON), "Real saved report") {
			t.Fatalf("missing actual report: %s", task.ResultArtifactsJSON)
		}
	}
	progress, _ := decodeJSONMap(task.RequestJSON)["progress"].(map[string]any)
	if progress["completed_steps"] != float64(1) || progress["total_steps"] != float64(1) {
		t.Fatalf("recovered progress not refreshed: %#v", progress)
	}
	var afterSteps []orm.WorkflowSessionStep
	var afterArtifacts []orm.WorkflowSlotRevision
	db.First(&session, "id=?", task.SessionID)
	db.Order("id").Find(&afterSteps)
	db.Order("id").Find(&afterArtifacts)
	after, _ := json.Marshal([]any{session, afterSteps, afterArtifacts})
	if string(before) != string(after) {
		t.Fatal("result collection changed Session, Attempt history or Artifact")
	}
	var jobs int64
	db.Model(&orm.AsyncJob{}).Count(&jobs)
	if jobs != 0 {
		t.Fatalf("collection scheduled execution: %d jobs", jobs)
	}
	db.First(&history, "id=?", history.ID)
	if history.RunStatus != "completed" || !strings.Contains(history.Result, "original failure") {
		t.Fatalf("chat lost completion or original step evidence: %s", history.Result)
	}
}

func TestExternalTaskRecoveryRejectsUnrelatedOrUnfinishedSessions(t *testing.T) {
	for _, test := range []struct {
		name, model, column string
		value               any
	}{
		{"session_failed", "session", "status", "failed"},
		{"session_running", "session", "status", "active"},
		{"session_waiting_approval", "session", "status", "waiting"},
		{"session_stopped", "session", "status", "stopped"},
		{"session_dismissed", "session", "dismissed", true},
		{"owner_mismatch", "session", "create_user_id", "other"},
		{"conversation_mismatch", "session", "conversation_id", "other"},
		{"revision_mismatch", "session", "plugin_revision_id", "other"}, // workflow-naming: persistence — legacy physical columns.
		{"workflow_mismatch", "session", "plugin_ref", "other"},         // workflow-naming: persistence — legacy physical columns.
		{"not_hosted", "session", "origin_host", "external"},
		{"foreign_controller", "session", "controller_host", "other"},
		{"conversion_failure", "task", "error_code", "CONVERSION_FAILED"},
		{"worker_failure", "task", "error_code", "TASK_WORKER_FAILED"},
		{"wrong_stage", "task", "stage", "generate"},
		{"cancelled", "task", "status", "canceled"},
		{"missing_revision", "task", "workflow_revision_id", ""},
		{"missing_session", "task", "session_id", "absent"},
	} {
		t.Run(test.name, func(t *testing.T) {
			db, task, session := seedRecoveredExternalTask(t)
			var model any = &task
			if test.model == "session" {
				model = &session
			}
			if err := db.Model(model).Update(test.column, test.value).Error; err != nil {
				t.Fatal(err)
			}
			db.First(&task, "id=?", task.ID)
			before, _ := json.Marshal(task)
			if err := recoverExternalWorkflowTasks(context.Background(), db.DB); err != nil {
				t.Fatal(err)
			}
			db.First(&task, "id=?", task.ID)
			after, _ := json.Marshal(task)
			if string(before) != string(after) {
				t.Fatalf("ineligible task changed: %s", after)
			}
			var jobs int64
			db.Model(&orm.AsyncJob{}).Count(&jobs)
			if jobs != 0 {
				t.Fatal("recovery scheduled execution")
			}
		})
	}
}

func TestExternalTaskRecoveryRollsBackWhenResultProjectionFails(t *testing.T) {
	db, task, _ := seedRecoveredExternalTask(t)
	if err := db.Where("id=?", externalWorkflowHistoryID(task.ID)).Delete(&orm.ChatHistory{}).Error; err != nil {
		t.Fatal(err)
	}
	before, _ := json.Marshal(task)
	if err := recoverExternalWorkflowTasks(context.Background(), db.DB); err == nil {
		t.Fatal("missing chat anchor must fail collection")
	}
	db.First(&task, "id=?", task.ID)
	after, _ := json.Marshal(task)
	if string(before) != string(after) {
		t.Fatalf("failed collection partially committed: %s", after)
	}
}

func TestExternalTaskRecoveryRequiresCompletedProjection(t *testing.T) {
	db, task, _ := seedRecoveredExternalTask(t)
	// A stale Session label must not substitute for current Runtime completion.
	if err := db.Model(&orm.WorkflowSessionStep{}).Where("id=?", "successful-retry").Update("status", "awaiting_approval").Error; err != nil {
		t.Fatal(err)
	}
	if err := recoverExternalWorkflowTasks(context.Background(), db.DB); err != nil {
		t.Fatal(err)
	}
	if err := db.First(&task, "id=?", task.ID).Error; err != nil {
		t.Fatal(err)
	}
	if task.Status != externalTaskStatusFailed {
		t.Fatal("incomplete projection was collected as success")
	}
}
