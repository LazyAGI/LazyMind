package subagent

import (
	"context"
	"gorm.io/gorm"
	"lazymind/core/localworkspace"
	"lazymind/core/state"
	"path/filepath"
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
	if err := ValidateWorkspaceRun(t.Context(), db.DB, ss, req); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []localworkspace.OperationRequest{
		{UserID: "other", ConversationID: req.ConversationID, TaskID: task.ID, Generation: req.Generation},
		{UserID: req.UserID, ConversationID: "other", TaskID: task.ID, Generation: req.Generation},
		{UserID: req.UserID, ConversationID: req.ConversationID, TaskID: task.ID, Generation: "old"},
	} {
		if err := ValidateWorkspaceRun(t.Context(), db.DB, ss, bad); err == nil {
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
	if err := ValidateWorkspaceRun(t.Context(), db.DB, ss, req); err == nil {
		t.Fatal("old generation authorized")
	}
	req.Generation = "launch-2"
	if err := ValidateWorkspaceRun(t.Context(), db.DB, ss, req); err != nil {
		t.Fatal(err)
	}
	if err := UpdateStatus(t.Context(), db.DB, task.ID, StatusInterrupted); err != nil {
		t.Fatal(err)
	}
	if err := ValidateWorkspaceRun(t.Context(), db.DB, ss, req); err == nil {
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
