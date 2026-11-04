package workflow

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"lazymind/core/common/orm"
	"lazymind/core/workflow/graphengine"
)

func TestPaperDirectReviewFreezesEntryChoiceWithoutPrepareAttempt(t *testing.T) {
	db, _ := setupBatchTransitionSession(t)
	root := filepath.Join("..", "..", "..", "workflows", "paper-writing")
	config, err := os.ReadFile(filepath.Join(root, "workflow.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	state, err := os.ReadFile(filepath.Join(root, "scenario", "state.yml"))
	if err != nil {
		t.Fatal(err)
	}
	compiled := graphengine.Compile(string(config), string(state), "", graphengine.ProfileRuntimeLoad)
	if !compiled.Valid {
		t.Fatal(compiled.Diagnostics)
	}
	graph := compiled.Graph
	if err := db.Model(&orm.WorkflowRevision{}).Where("id = ?", "batch-revision").Updates(map[string]any{
		"compiled_graph": graph.JSON(), "graph_hash": graph.GraphHash,
	}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&orm.WorkflowSession{}).Where("id = ?", "batch-session").Update("graph_hash", graph.GraphHash).Error; err != nil {
		t.Fatal(err)
	}
	w, data := runBatchTransition(t, db, graph.GraphHash, "execute_batch", []map[string]any{
		{"target_step_id": "peer_review", "task_id": "review-task", "objective": "审稿", "user_input": "只审稿"},
	})
	if w.Code != http.StatusOK || data["accepted"] != true {
		t.Fatalf("direct review rejected: %s", w.Body.String())
	}
	snapshot, err := loadRuntimeSnapshot(context.Background(), db.DB, "batch-session", graph)
	if err != nil {
		t.Fatal(err)
	}
	projection := graphengine.Project(graph, snapshot)
	for _, id := range []string{"research_topic", "literature_review", "experiment", "write_document", "reading_reflection"} {
		if projection.Nodes[id].Branch != "pruned" {
			t.Fatalf("unchosen entry %s was not pruned: %+v", id, projection.Nodes[id])
		}
	}
	if projection.Nodes["data_analysis"].Reachability != "unreachable" {
		t.Fatal("analysis must remain unreachable until F3 is selected")
	}
	if projection.Nodes["peer_review"].Branch != "active" || projection.Nodes["revise_document"].Branch == "pruned" {
		t.Fatal("review and its future revision must remain visible")
	}
	var count int64
	db.Model(&orm.WorkflowRouteDecision{}).Where("session_id = ? AND from_step_id = ? AND validity = ?",
		"batch-session", "__start__", "effective").Count(&count)
	if count != 1 {
		t.Fatalf("expected one entry decision, got %d", count)
	}
	// Re-selecting the same target must not duplicate or reopen the entry decision.
	if err := selectLLMChoiceRoutes(context.Background(), db.DB, "batch-session", graph,
		[]transitionTarget{{TargetStepID: "peer_review"}}); err != nil {
		t.Fatal(err)
	}
	db.Model(&orm.WorkflowRouteDecision{}).Where("session_id = ? AND from_step_id = ? AND validity = ?",
		"batch-session", "__start__", "effective").Count(&count)
	if count != 1 {
		t.Fatal("retry duplicated the entry decision")
	}
}

func TestPaperDirectWritingRejectsMissingInputsBeforeCreatingAttempt(t *testing.T) {
	db, _ := setupBatchTransitionSession(t)
	root := filepath.Join("..", "..", "..", "workflows", "paper-writing")
	config, err := os.ReadFile(filepath.Join(root, "workflow.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	state, err := os.ReadFile(filepath.Join(root, "scenario", "state.yml"))
	if err != nil {
		t.Fatal(err)
	}
	compiled := graphengine.Compile(string(config), string(state), "", graphengine.ProfileRuntimeLoad)
	if !compiled.Valid {
		t.Fatal(compiled.Diagnostics)
	}
	graph := compiled.Graph
	if err := db.Model(&orm.WorkflowRevision{}).Where("id = ?", "batch-revision").Updates(map[string]any{
		"compiled_graph": graph.JSON(), "graph_hash": graph.GraphHash,
	}).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&orm.WorkflowSession{}).Where("id = ?", "batch-session").Update("graph_hash", graph.GraphHash).Error; err != nil {
		t.Fatal(err)
	}
	w, data := runBatchTransition(t, db, graph.GraphHash, "execute_batch", []map[string]any{
		{"target_step_id": "write_document", "task_id": "empty-writing", "objective": "写论文", "user_input": "按附件写论文"},
	})
	failure, _ := data["error"].(map[string]any)
	if w.Code != http.StatusConflict || failure["code"] != "STEP_NOT_READY" {
		t.Fatalf("empty input launched F5: %s", w.Body.String())
	}
	var count int64
	if err := db.Model(&orm.WorkflowSessionStep{}).Where("session_id = ? AND step_id = ?", "batch-session", "write_document").Count(&count).Error; err != nil || count != 0 {
		t.Fatalf("empty input created attempt: count=%d err=%v", count, err)
	}
}
