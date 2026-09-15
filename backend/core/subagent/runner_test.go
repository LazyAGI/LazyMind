package subagent

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"lazymind/core/common/orm"
)

// TestAlgoServiceURL returns a non-empty service endpoint.
func TestAlgoServiceURL(t *testing.T) {
	got := algoServiceURL()
	if got == "" {
		t.Fatal("expected non-empty algo service URL")
	}
}

func TestHydrateRunRequestUsesCoreOwnedSnapshotWithoutDSN(t *testing.T) {
	db := newTestDB(t)
	now := time.Now().UTC()
	task := &orm.SubAgentTask{
		ID: "task-snapshot", ConversationID: "conv-1", TriggerHistoryID: "history-1",
		SeqInConversation: 2, AgentType: "research", Title: "Research", Objective: "Investigate",
		Params: json.RawMessage(`{"depth":"high"}`), Mode: "auto", Status: StatusRunning,
		WorkspacePath: "/workspace/task-snapshot", InputSlots: json.RawMessage(`["brief"]`),
		OutputSlots: json.RawMessage(`["report"]`), Sources: orm.RawJSON(`[]`),
		CreateUserID: "user-1", LastHeartbeat: now, CreatedAt: now, UpdatedAt: now,
	}
	if err := db.Create(task).Error; err != nil {
		t.Fatal(err)
	}
	if err := AppendRemoteStep(context.Background(), db.DB, task.ID, "text",
		json.RawMessage(`{"content":"checkpoint"}`)); err != nil {
		t.Fatal(err)
	}
	if err := SaveArtifact(context.Background(), db.DB, task.ID, "report", "text",
		json.RawMessage(`{"text":"draft"}`), 2); err != nil {
		t.Fatal(err)
	}

	req := RunRequest{TaskID: task.ID, AgentType: task.AgentType, WorkspacePath: task.WorkspacePath, Resume: true}
	if err := hydrateRunRequest(context.Background(), db.DB, &req); err != nil {
		t.Fatal(err)
	}
	if req.TaskSpec["id"] != task.ID || len(req.InitialSteps) != 1 {
		t.Fatalf("request not hydrated: %#v", req)
	}
	artifacts, ok := req.TaskSpec["artifacts"].([]map[string]any)
	if !ok || len(artifacts) != 1 || artifacts[0]["seq"] != 2 {
		t.Fatalf("artifacts=%#v", req.TaskSpec["artifacts"])
	}
	wire, err := json.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(wire), "db_dsn") || strings.Contains(string(wire), "core.db") {
		t.Fatalf("database access leaked to Algorithm: %s", wire)
	}
}

func TestRouteEventPersistsStreamedStepInCore(t *testing.T) {
	db := newTestDB(t)
	now := time.Now().UTC()
	if err := db.Create(&orm.SubAgentTask{ID: "task-event", ConversationID: "conv-1",
		AgentType: "research", Title: "Research", Objective: "Investigate", Mode: "auto",
		Status: StatusRunning, Params: json.RawMessage(`{}`), InputSlots: json.RawMessage(`[]`),
		OutputSlots: json.RawMessage(`[]`), LastHeartbeat: now, CreatedAt: now, UpdatedAt: now}).Error; err != nil {
		t.Fatal(err)
	}
	if err := routeEvent(context.Background(), db.DB, nil,
		TaskEvent{Type: "text", TaskID: "task-event", Text: "result chunk"}); err != nil {
		t.Fatal(err)
	}
	steps, err := LoadSteps(context.Background(), db.DB, "task-event")
	if err != nil || len(steps) != 1 || steps[0].Role != "text" {
		t.Fatalf("steps=%#v err=%v", steps, err)
	}
}

func TestHydrationFailureMarksExistingTaskFailed(t *testing.T) {
	db := newTestDB(t)
	now := time.Now().UTC()
	task := &orm.SubAgentTask{ID: "task-hydration-error", ConversationID: "conv-1",
		AgentType: "research", Title: "Research", Objective: "Investigate", Mode: "auto",
		Status: StatusRunning, Params: json.RawMessage(`{}`), InputSlots: json.RawMessage(`[]`),
		OutputSlots: json.RawMessage(`[]`), LastHeartbeat: now, CreatedAt: now, UpdatedAt: now}
	if err := db.Create(task).Error; err != nil {
		t.Fatal(err)
	}
	// Break only the hydration read; the task table remains available so the
	// runner can persist its terminal failure state.
	if err := db.Exec("DROP TABLE sub_agent_steps").Error; err != nil {
		t.Fatal(err)
	}

	err := RunObserved(context.Background(), db.DB, nil, RunRequest{TaskID: task.ID}, nil)
	if err == nil || !strings.Contains(err.Error(), "prepare subagent run") {
		t.Fatalf("error=%v", err)
	}
	stored, err := GetTask(context.Background(), db.DB, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Status != StatusFailed || !strings.Contains(stored.Summary, "prepare subagent run") {
		t.Fatalf("task was left non-terminal after hydration failure: %#v", stored)
	}
}
