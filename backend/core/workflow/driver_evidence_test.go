package workflow

import (
	"encoding/json"
	"strings"
	"testing"

	"lazymind/core/common/orm"
	"lazymind/core/workflow/graphengine"
)

func TestDriverPostStepChecksUseCurrentAttempt(t *testing.T) {
	db := newTestDB(t)
	if err := db.AutoMigrate(&orm.WorkflowRevision{}); err != nil {
		t.Fatal(err)
	}
	graph := graphengine.CompiledStateGraph{SchemaVersion: graphengine.SchemaVersion, GraphHash: "evidence-graph", Runtime: graphengine.RuntimePolicy{PostStepChecks: []graphengine.PostStepCheck{{StepID: "analyze", Tool: "check_capabilities"}}}}
	if err := db.Create(&orm.WorkflowRevision{ID: "revision", WorkflowResourceID: "resource", RevisionNo: 1, CompiledGraph: graph.JSON(), GraphSchemaVersion: graphengine.SchemaVersion, GraphHash: "evidence-graph"}).Error; err != nil {
		t.Fatal(err)
	}
	session := orm.WorkflowSession{ID: "session", WorkflowRevisionID: "revision"}
	for _, row := range []orm.WorkflowSessionStep{
		{ID: "old", SessionID: "session", StepID: "analyze", TaskID: "old-task", Attempt: 1, Validity: "stale"},
		{ID: "new", SessionID: "session", StepID: "analyze", TaskID: "new-task", Attempt: 2, Validity: "effective"},
	} {
		if err := db.Create(&row).Error; err != nil {
			t.Fatal(err)
		}
	}
	for _, row := range []orm.SubAgentStep{
		{ID: "old-result", TaskID: "old-task", Role: "tool", Seq: 1, Content: json.RawMessage(`{"tool_results":[{"tool_call_id":"post-step-check-1","name":"check_capabilities","result":{"ok":true,"value":{"status":"old-ready"}}}]}`)},
		{ID: "unrelated", TaskID: "new-task", Role: "tool", Seq: 1, Content: json.RawMessage(`{"tool_results":[{"tool_call_id":"ordinary-call","name":"check_capabilities","result":"unrelated"}]}`)},
		{ID: "new-result", TaskID: "new-task", Role: "tool", Seq: 2, Content: json.RawMessage(`{"tool_results":[{"tool_call_id":"post-step-check-1","name":"check_capabilities","result":{"ok":true,"value":{"status":"ready","missing":[]}}}]}`)},
	} {
		if err := db.Create(&row).Error; err != nil {
			t.Fatal(err)
		}
	}
	summary, err := buildWorkflowPostStepChecks(t.Context(), db.DB, &session, "analyze")
	if err != nil || !strings.Contains(summary, `"status":"ready"`) || strings.Contains(summary, "old-ready") || strings.Contains(summary, "unrelated") {
		t.Fatalf("summary=%s err=%v", summary, err)
	}
	if err := db.Model(&orm.SubAgentStep{}).Where("id = ?", "new-result").Update("content", json.RawMessage(`{"tool_results":[{"tool_call_id":"post-step-check-1","name":"check_capabilities","result":{"ok":false,"value":"unavailable"}}]}`)).Error; err != nil {
		t.Fatal(err)
	}
	summary, err = buildWorkflowPostStepChecks(t.Context(), db.DB, &session, "analyze")
	if err != nil || !strings.Contains(summary, `"ok":false`) || strings.Contains(summary, `"status":"ready"`) {
		t.Fatalf("failed check lost: %s err=%v", summary, err)
	}
	if err := db.Delete(&orm.SubAgentStep{}, "id = ?", "new-result").Error; err != nil {
		t.Fatal(err)
	}
	summary, err = buildWorkflowPostStepChecks(t.Context(), db.DB, &session, "analyze")
	if err != nil || !strings.Contains(summary, "no recorded result") || strings.Contains(summary, "old-ready") {
		t.Fatalf("missing check fabricated: %s err=%v", summary, err)
	}
}
