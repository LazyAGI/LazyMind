package subagent

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gorilla/mux"

	"lazymind/core/common/orm"
	"lazymind/core/store"
)

// newSubagentHTTPTestDB creates a SQLite DB with subagent models and initializes store.
func newSubagentHTTPTestDB(t *testing.T) *orm.DB {
	t.Helper()
	db := newTestDB(t)
	store.Init(db.DB, nil, nil)
	t.Cleanup(func() { store.Init(nil, nil, nil) })
	return db
}

// seedSubagentTask inserts a task for handler tests.
func seedSubagentTask(t *testing.T, db *orm.DB, taskID, convID, userID, status string) {
	t.Helper()
	now := time.Now().UTC()
	db.DB.Create(&orm.SubAgentTask{
		ID: taskID, ConversationID: convID, TriggerHistoryID: "hist-1",
		SeqInConversation: 1, AgentType: "code-gen", Title: "Test Task",
		Objective: "Test objective", Mode: "auto", Status: status,
		ProgressPct: 50, CurrentPhase: "working", EstimatedSec: 60,
		CreateUserID: userID, WorkspacePath: "/ws/" + taskID,
		InputSlots: json.RawMessage("[]"), OutputSlots: json.RawMessage("[]"),
		LastHeartbeat: now, CreatedAt: now, UpdatedAt: now,
	})
}

// seedSubagentStep inserts a step for a task.
func seedSubagentStep(t *testing.T, db *orm.DB, taskID string, seq int, role string, content string) {
	t.Helper()
	now := time.Now().UTC()
	db.DB.Create(&orm.SubAgentStep{
		TaskID: taskID, Seq: seq, Role: role,
		Content:   json.RawMessage(content),
		CreatedAt: now,
	})
}

// seedSubagentArtifact inserts a visible artifact for a task.
func seedSubagentArtifact(t *testing.T, db *orm.DB, taskID, slot string, seq int, contentType string, value string) {
	t.Helper()
	now := time.Now().UTC()
	db.DB.Create(&orm.SubAgentArtifact{
		ID:          taskID + "-" + slot,
		TaskID:      taskID,
		Slot:        slot,
		Seq:         seq,
		ContentType: contentType,
		Value:       json.RawMessage(value),
		CreatedAt:   now,
	})
}

// getData extracts the "data" field from common.ReplyOK-style JSON responses.
func getData(body []byte) map[string]any {
	var resp map[string]any
	json.Unmarshal(body, &resp)
	data, _ := resp["data"].(map[string]any)
	return data
}

// --- ListConversationTasks ---

// TestListConversationTasks_MissingConvID returns 400 when conversation_id is empty.
func TestListConversationTasks_MissingConvID(t *testing.T) {
	newSubagentHTTPTestDB(t)
	req := httptest.NewRequest(http.MethodGet, "/conversations//tasks", nil)
	rec := httptest.NewRecorder()
	ListConversationTasks(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("got %d, want %d", rec.Code, http.StatusBadRequest)
	}
}

// TestListConversationTasks_EmptyTasks returns 200 with empty task list.
func TestListConversationTasks_EmptyTasks(t *testing.T) {
	newSubagentHTTPTestDB(t)
	req := httptest.NewRequest(http.MethodGet, "/conversations/conv-empty/tasks", nil)
	req = mux.SetURLVars(req, map[string]string{"conversation_id": "conv-empty"})
	req.Header.Set("X-User-Id", "user-1")
	rec := httptest.NewRecorder()
	ListConversationTasks(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("got %d, want %d", rec.Code, http.StatusOK)
	}
	data := getData(rec.Body.Bytes())
	tasks, _ := data["tasks"].([]any)
	if len(tasks) != 0 {
		t.Fatalf("expected empty tasks, got %d", len(tasks))
	}
}

// TestListConversationTasks_WithData returns tasks with steps and artifacts.
func TestListConversationTasks_WithData(t *testing.T) {
	db := newSubagentHTTPTestDB(t)
	seedSubagentTask(t, db, "task-1", "conv-1", "user-1", "running")
	seedSubagentStep(t, db, "task-1", 1, "text", `{"content":"hello"}`)
	seedSubagentArtifact(t, db, "task-1", "output", 1, "image/png", `{"url":"http://example.com/img.png"}`)

	req := httptest.NewRequest(http.MethodGet, "/conversations/conv-1/tasks", nil)
	req = mux.SetURLVars(req, map[string]string{"conversation_id": "conv-1"})
	req.Header.Set("X-User-Id", "user-1")
	rec := httptest.NewRecorder()
	ListConversationTasks(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("got %d, want %d, body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	data := getData(rec.Body.Bytes())
	tasks := data["tasks"].([]any)
	if len(tasks) != 1 {
		t.Fatalf("expected 1 task, got %d", len(tasks))
	}
}

// --- GetTaskDetail ---

// TestGetTaskDetail_MissingTaskID returns 400.
func TestGetTaskDetail_MissingTaskID(t *testing.T) {
	newSubagentHTTPTestDB(t)
	req := httptest.NewRequest(http.MethodGet, "/tasks/", nil)
	rec := httptest.NewRecorder()
	GetTaskDetail(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("got %d, want %d", rec.Code, http.StatusBadRequest)
	}
}

// TestGetTaskDetail_NotFound returns 404.
func TestGetTaskDetail_NotFound(t *testing.T) {
	newSubagentHTTPTestDB(t)
	req := httptest.NewRequest(http.MethodGet, "/tasks/nonexistent", nil)
	req = mux.SetURLVars(req, map[string]string{"task_id": "nonexistent"})
	req.Header.Set("X-User-Id", "user-1")
	rec := httptest.NewRecorder()
	GetTaskDetail(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("got %d, want %d", rec.Code, http.StatusNotFound)
	}
}

// TestGetTaskDetail_UserMismatch returns 404 (security: same as not found).
func TestGetTaskDetail_UserMismatch(t *testing.T) {
	db := newSubagentHTTPTestDB(t)
	seedSubagentTask(t, db, "task-2", "conv-2", "user-a", "running")

	req := httptest.NewRequest(http.MethodGet, "/tasks/task-2", nil)
	req = mux.SetURLVars(req, map[string]string{"task_id": "task-2"})
	req.Header.Set("X-User-Id", "user-b")
	rec := httptest.NewRecorder()
	GetTaskDetail(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("got %d, want %d (user mismatch should be 404)", rec.Code, http.StatusNotFound)
	}
}

// TestGetTaskDetail_Success returns 200 with full DTO.
func TestGetTaskDetail_Success(t *testing.T) {
	db := newSubagentHTTPTestDB(t)
	seedSubagentTask(t, db, "task-3", "conv-3", "user-1", "running")

	req := httptest.NewRequest(http.MethodGet, "/tasks/task-3", nil)
	req = mux.SetURLVars(req, map[string]string{"task_id": "task-3"})
	req.Header.Set("X-User-Id", "user-1")
	rec := httptest.NewRecorder()
	GetTaskDetail(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("got %d, want %d, body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	data := getData(rec.Body.Bytes())
	task, ok := data["task"].(map[string]any)
	if !ok || task["task_id"] != "task-3" {
		t.Fatalf("unexpected task: %v", data)
	}
}

// --- GetTaskArtifacts ---

// TestGetTaskArtifacts_MissingTaskID returns 400.
func TestGetTaskArtifacts_MissingTaskID(t *testing.T) {
	newSubagentHTTPTestDB(t)
	req := httptest.NewRequest(http.MethodGet, "/tasks//artifacts", nil)
	rec := httptest.NewRecorder()
	GetTaskArtifacts(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("got %d, want %d", rec.Code, http.StatusBadRequest)
	}
}

// TestGetTaskArtifacts_NotFound returns 404.
func TestGetTaskArtifacts_NotFound(t *testing.T) {
	newSubagentHTTPTestDB(t)
	req := httptest.NewRequest(http.MethodGet, "/tasks/nonexistent/artifacts", nil)
	req = mux.SetURLVars(req, map[string]string{"task_id": "nonexistent"})
	req.Header.Set("X-User-Id", "user-1")
	rec := httptest.NewRecorder()
	GetTaskArtifacts(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("got %d, want %d", rec.Code, http.StatusNotFound)
	}
}

// TestGetTaskArtifacts_Empty returns 200 with empty list.
func TestGetTaskArtifacts_Empty(t *testing.T) {
	db := newSubagentHTTPTestDB(t)
	seedSubagentTask(t, db, "task-4", "conv-4", "user-1", "running")

	req := httptest.NewRequest(http.MethodGet, "/tasks/task-4/artifacts", nil)
	req = mux.SetURLVars(req, map[string]string{"task_id": "task-4"})
	req.Header.Set("X-User-Id", "user-1")
	rec := httptest.NewRecorder()
	GetTaskArtifacts(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("got %d, want %d", rec.Code, http.StatusOK)
	}
	data := getData(rec.Body.Bytes())
	arts, _ := data["artifacts"].([]any)
	if len(arts) != 0 {
		t.Fatalf("expected empty artifacts, got %d", len(arts))
	}
}

// TestGetTaskArtifacts_WithData returns artifacts.
func TestGetTaskArtifacts_WithData(t *testing.T) {
	db := newSubagentHTTPTestDB(t)
	seedSubagentTask(t, db, "task-5", "conv-5", "user-1", "running")
	seedSubagentArtifact(t, db, "task-5", "chart", 1, "image/png", `{"url":"http://example.com/chart.png"}`)
	seedSubagentArtifact(t, db, "task-5", "data", 2, "text/csv", `{"url":"http://example.com/data.csv"}`)

	req := httptest.NewRequest(http.MethodGet, "/tasks/task-5/artifacts", nil)
	req = mux.SetURLVars(req, map[string]string{"task_id": "task-5"})
	req.Header.Set("X-User-Id", "user-1")
	rec := httptest.NewRecorder()
	GetTaskArtifacts(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("got %d, want %d, body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	data := getData(rec.Body.Bytes())
	arts := data["artifacts"].([]any)
	if len(arts) != 2 {
		t.Fatalf("expected 2 artifacts, got %d", len(arts))
	}
}

// --- InternalGetTaskEvents ---

// TestInternalGetTaskEvents_MissingTaskID returns 400.
func TestInternalGetTaskEvents_MissingTaskID(t *testing.T) {
	newSubagentHTTPTestDB(t)
	req := httptest.NewRequest(http.MethodGet, "/internal/subagent/tasks//events", nil)
	rec := httptest.NewRecorder()
	InternalGetTaskEvents(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("got %d, want %d", rec.Code, http.StatusBadRequest)
	}
}

// TestInternalGetTaskEvents_NoEvents returns empty batch.
func TestInternalGetTaskEvents_NoEvents(t *testing.T) {
	db := newSubagentHTTPTestDB(t)
	seedSubagentTask(t, db, "task-6", "conv-6", "user-1", "running")

	req := httptest.NewRequest(http.MethodGet, "/internal/subagent/tasks/task-6/events?from=0", nil)
	req = mux.SetURLVars(req, map[string]string{"task_id": "task-6"})
	rec := httptest.NewRecorder()
	InternalGetTaskEvents(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("got %d, want %d, body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	data := getData(rec.Body.Bytes())
	events, _ := data["events"].([]any)
	if len(events) != 0 {
		t.Fatalf("expected empty events, got %d", len(events))
	}
}

// --- InternalGetTaskStatus ---

// TestInternalGetTaskStatus_MissingTaskID returns 400.
func TestInternalGetTaskStatus_MissingTaskID(t *testing.T) {
	newSubagentHTTPTestDB(t)
	req := httptest.NewRequest(http.MethodGet, "/internal/subagent/tasks//status", nil)
	rec := httptest.NewRecorder()
	InternalGetTaskStatus(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("got %d, want %d", rec.Code, http.StatusBadRequest)
	}
}

// TestInternalGetTaskStatus_ReturnsStatus returns current task status.
func TestInternalGetTaskStatus_ReturnsStatus(t *testing.T) {
	db := newSubagentHTTPTestDB(t)
	seedSubagentTask(t, db, "task-7", "conv-7", "user-1", "running")

	req := httptest.NewRequest(http.MethodGet, "/internal/subagent/tasks/task-7/status", nil)
	req = mux.SetURLVars(req, map[string]string{"task_id": "task-7"})
	rec := httptest.NewRecorder()
	InternalGetTaskStatus(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("got %d, want %d, body=%s", rec.Code, http.StatusOK, rec.Body.String())
	}
	data := getData(rec.Body.Bytes())
	if data["task_id"] != "task-7" {
		t.Fatalf("unexpected response: %v", data)
	}
}
