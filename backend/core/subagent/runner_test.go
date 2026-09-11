package subagent

import (
	"context"
	"encoding/json"
	"gorm.io/gorm"
	"lazymind/core/common/orm"
	"lazymind/core/localworkspace"
	"lazymind/core/state"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestAlgoServiceURL returns a non-empty service endpoint.
func TestAlgoServiceURL(t *testing.T) {
	got := algoServiceURL()
	if got == "" {
		t.Fatal("expected non-empty algo service URL")
	}
}

func TestWorkspaceSubagentGenerationFencesOldEventsAndSurvivesParentCompletion(t *testing.T) {
	db := newTestDB(t)
	ss, err := state.NewSQLiteStore(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer ss.Close()
	task, err := CreateTask(t.Context(), db.DB, CreateTaskInput{TaskID: "child", ConversationID: "parent", CreateUserID: "owner", AgentType: "research", Mode: "auto"})
	if err != nil {
		t.Fatal(err)
	}
	req := localworkspace.OperationRequest{UserID: "owner", ConversationID: task.ConversationID, TaskID: task.ID, Generation: "launch-1"}
	if err := ss.Set(t.Context(), workspaceRunKey(task.ID), []byte(req.Generation), time.Hour); err != nil {
		t.Fatal(err)
	}
	// A detached task authorizes using its own live state, with no parent run.
	if _, err := ValidateWorkspaceRun(t.Context(), db.DB, ss, req); err != nil {
		t.Fatal(err)
	}
	boundWithoutSnapshot := req
	boundWithoutSnapshot.WorkspaceID = "workspace"
	if _, err := ValidateWorkspaceRun(t.Context(), db.DB, ss, boundWithoutSnapshot); err == nil {
		t.Fatal("bound subagent without a Core workspace snapshot was accepted")
	}
	for _, bad := range []localworkspace.OperationRequest{
		{UserID: "other", ConversationID: req.ConversationID, TaskID: task.ID, Generation: req.Generation},
		{UserID: req.UserID, ConversationID: "other", TaskID: task.ID, Generation: req.Generation},
		{UserID: req.UserID, ConversationID: req.ConversationID, TaskID: task.ID, Generation: "old"},
	} {
		if _, err := ValidateWorkspaceRun(t.Context(), db.DB, ss, bad); err == nil {
			t.Fatal("wrong task identity accepted")
		}
	}
	if err := withWorkspaceRunUpdate(t.Context(), db.DB, task.ID, func(tx *gorm.DB) error {
		if err := UpdateStatus(t.Context(), tx, task.ID, StatusRunning); err != nil {
			return err
		}
		return ss.Set(t.Context(), workspaceRunKey(task.ID), []byte("launch-2"), time.Hour)
	}); err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{"done", "error"} {
		if err := routeRunEvent(t.Context(), db.DB, ss, "launch-1", TaskEvent{TaskID: task.ID, Type: kind}); err == nil {
			t.Fatalf("stale %s accepted", kind)
		}
	}
	current, err := GetTask(t.Context(), db.DB, task.ID)
	if err != nil || current.Status != StatusRunning {
		t.Fatalf("new run changed: %v %v", current, err)
	}
	if _, err := ValidateWorkspaceRun(t.Context(), db.DB, ss, req); err == nil {
		t.Fatal("old generation authorized")
	}
	req.Generation = "launch-2"
	if _, err := ValidateWorkspaceRun(t.Context(), db.DB, ss, req); err != nil {
		t.Fatal(err)
	}
	if err := UpdateStatus(t.Context(), db.DB, task.ID, StatusInterrupted); err != nil {
		t.Fatal(err)
	}
	if _, err := ValidateWorkspaceRun(t.Context(), db.DB, ss, req); err == nil {
		t.Fatal("interrupted child authorized")
	}
}

type blockingGenerationStore struct {
	state.Store
	entered, release chan struct{}
}

func (s *blockingGenerationStore) Get(ctx context.Context, key string) ([]byte, error) {
	if key == workspaceRunKey("child") {
		close(s.entered)
		select {
		case <-s.release:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return s.Store.Get(ctx, key)
}

func TestWorkspaceSubagentEventAndResumeShareTaskLock(t *testing.T) {
	db := newTestDB(t)
	ss, err := state.NewSQLiteStore(filepath.Join(t.TempDir(), "state.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer ss.Close()
	if _, err := CreateTask(t.Context(), db.DB, CreateTaskInput{TaskID: "child", ConversationID: "parent", CreateUserID: "owner", AgentType: "research", Mode: "auto"}); err != nil {
		t.Fatal(err)
	}
	if err := ss.Set(t.Context(), workspaceRunKey("child"), []byte("old"), time.Hour); err != nil {
		t.Fatal(err)
	}
	blocked := &blockingGenerationStore{Store: ss, entered: make(chan struct{}), release: make(chan struct{})}
	finished, resumed := make(chan error, 1), make(chan error, 1)
	go func() {
		finished <- routeRunEvent(t.Context(), db.DB, blocked, "old", TaskEvent{TaskID: "child", Type: "done"})
	}()
	select {
	case <-blocked.entered:
	case <-time.After(time.Second):
		t.Fatal("event did not enter generation check")
	}
	go func() {
		resumed <- withWorkspaceRunUpdate(t.Context(), db.DB, "child", func(tx *gorm.DB) error {
			if err := UpdateStatus(t.Context(), tx, "child", StatusRunning); err != nil {
				return err
			}
			return ss.Set(t.Context(), workspaceRunKey("child"), []byte("new"), time.Hour)
		})
	}()
	select {
	case err := <-resumed:
		close(blocked.release)
		t.Fatalf("resume bypassed event lock: %v", err)
	case <-time.After(40 * time.Millisecond):
	}
	close(blocked.release)
	if err := <-finished; err != nil {
		t.Fatal(err)
	}
	if err := <-resumed; err != nil {
		t.Fatal(err)
	}
	task, err := GetTask(t.Context(), db.DB, "child")
	if err != nil || task.Status != StatusRunning {
		t.Fatalf("old completion overwrote resumed state: %+v %v", task, err)
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
