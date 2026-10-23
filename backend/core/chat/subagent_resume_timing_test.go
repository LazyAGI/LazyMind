package chat

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"lazymind/core/common/orm"
	"lazymind/core/state"
	"lazymind/core/subagent"
	"lazymind/core/tasktiming"
)

func TestResumedSubagentBelongsToNewChatRound(t *testing.T) {
	t.Setenv("LAZYMIND_RUNTIME_MODE", "cloud")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Error(w, "offline", 503) }))
	t.Cleanup(server.Close)
	t.Setenv("LAZYMIND_CHAT_SERVICE_URL", server.URL)
	db := orm.MigrateTestDB(t,
		&orm.Conversation{}, &orm.ConversationWorkspaceBinding{}, &orm.UserEnvironmentVariable{},
		&orm.SubAgentArtifact{}, &orm.SubAgentStep{}, &orm.SubAgentTask{},
		&orm.TaskAttemptTiming{}, &orm.TaskExecutionTiming{}, &orm.WorkflowEvent{}, &orm.WorkflowSessionStep{},
	)
	shared, err := state.NewSQLiteStore(filepath.Join(t.TempDir(), "resume.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = shared.Close() })
	start := time.Date(2026, 10, 20, 0, 0, 0, 123456000, time.UTC)
	if err := db.Create(&orm.Conversation{ID: "conv", BaseModel: orm.BaseModel{CreateUserID: "owner"}}).Error; err != nil {
		t.Fatal(err)
	}
	if err := tasktiming.Begin(t.Context(), db.DB, "old-root", "old-history", "conv", "owner", start); err != nil {
		t.Fatal(err)
	}
	oldStart, oldEnd := start.Add(time.Second), start.Add(10*time.Second)
	task := orm.SubAgentTask{ID: "resumed", ConversationID: "conv", TriggerHistoryID: "old-history", CreateUserID: "owner", AgentType: "research", Mode: "manual", InputSlots: []byte(`[]`), OutputSlots: []byte(`[]`), Status: subagent.StatusFailed, ExecutionID: "old-attempt", StartedAt: &oldStart, FinishedAt: &oldEnd, Params: []byte(`{}`)}
	if err := db.Create(&task).Error; err != nil {
		t.Fatal(err)
	}
	if err := tasktiming.RecordSubagent(t.Context(), db.DB, task); err != nil {
		t.Fatal(err)
	}
	if err := tasktiming.FinishChat(t.Context(), db.DB, "old-root", "completed", oldEnd); err != nil {
		t.Fatal(err)
	}
	if err := tasktiming.Begin(t.Context(), db.DB, "new-root", "new-history", "conv", "owner", start.Add(20*time.Second)); err != nil {
		t.Fatal(err)
	}
	notice, err := handleTaskCreated(t.Context(), db.DB, shared, "conv", "new-history", "owner", &TaskCreatedEvent{TaskID: task.ID, AgentType: "research", Resume: true}, nil, nil, "dynamic")
	if err != nil {
		t.Fatal(err)
	}
	if notice.TaskID != task.ID || notice.TriggerHistoryID != "new-history" {
		t.Fatalf("resume retained old round: %+v", notice)
	}
	saved, err := subagent.GetTask(t.Context(), db.DB, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	view, err := subagent.OrdinaryTask(t.Context(), db.DB, saved)
	if err != nil || view.RunID != "new-history" || view.TriggerHistoryID != "new-history" || saved.ExecutionID == "old-attempt" {
		t.Fatalf("resumed task is hidden from current round: %+v %v", view, err)
	}
	var previous orm.TaskAttemptTiming
	if err := db.First(&previous, "id=?", "task:resumed:old-attempt").Error; err != nil || previous.ExecutionID != "old-root" || previous.FinishedAt == nil || !previous.FinishedAt.Equal(oldEnd) {
		t.Fatalf("old attempt changed: %+v %v", previous, err)
	}
}
