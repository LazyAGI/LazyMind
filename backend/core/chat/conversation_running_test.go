package chat

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"gorm.io/gorm"
	"lazymind/core/common/orm"
	"lazymind/core/state"
	"lazymind/core/store"
	"lazymind/core/workflow"
)

func runningStatusDB(t *testing.T, ids ...string) (*gorm.DB, state.Store) {
	t.Helper()
	db := orm.MigrateTestDB(t, &orm.Conversation{}, &orm.ChatHistory{}, &orm.MultiAnswersChatHistory{},
		&orm.ExternalChatRun{}, &orm.SubAgentTask{}, &orm.WorkflowSession{}, &orm.WorkflowSessionStep{}, &orm.TaskCenterTask{}).DB
	cache, err := state.NewSQLiteStore(t.TempDir() + "/state.db")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cache.Close() })
	for _, id := range ids {
		if err := db.Create(&orm.Conversation{ID: id, DisplayName: id, BaseModel: orm.BaseModel{CreateUserID: "u1"}}).Error; err != nil {
			t.Fatal(err)
		}
	}
	return db, cache
}

func assertRunningStatuses(t *testing.T, db *gorm.DB, cache state.Store, want map[string]string) {
	t.Helper()
	ids := make([]string, 0, len(want))
	for id := range want {
		ids = append(ids, id)
	}
	rows, err := batchConversationRunningStatus(context.Background(), db, cache, "u1", ids, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != len(want) {
		t.Fatalf("statuses=%v, want %v", rows, want)
	}
	for _, row := range rows {
		if row.Status != want[row.ConversationID] {
			t.Errorf("%s = %s, want %s", row.ConversationID, row.Status, want[row.ConversationID])
		}
	}
}

func seedRunningRecord(t *testing.T, db *gorm.DB, row any) {
	t.Helper()
	if task, ok := row.(*orm.SubAgentTask); ok {
		task.InputSlots, task.OutputSlots = json.RawMessage(`[]`), json.RawMessage(`[]`)
	}
	if err := db.Create(row).Error; err != nil {
		t.Fatal(err)
	}
}

func TestConversationRunningAggregatesIndependentExecutions(t *testing.T) {
	db, cache := runningStatusDB(t, "chat", "external", "subagent", "work", "future", "waiting", "parent", "child", "idle")
	now := time.Now()
	future := now.Add(time.Hour)
	seedRunningRecord(t, db, &orm.ExternalChatRun{ID: "external-run", ConversationID: "external", ActorUserID: "u1", Status: "pending"})
	seedRunningRecord(t, db, &orm.SubAgentTask{ID: "task", ConversationID: "subagent", AgentType: "research", Status: "running"})
	seedRunningRecord(t, db, &orm.SubAgentTask{ID: "waiting-task", ConversationID: "waiting", AgentType: "research", Status: "waiting"})
	seedRunningRecord(t, db, &orm.TaskCenterTask{ID: "work-task", ConversationID: "work", UserID: "u1", TaskType: "scheduled", Status: "running", CreatedAt: now})
	seedRunningRecord(t, db, &orm.TaskCenterTask{ID: "future-task", ConversationID: "future", UserID: "u1", TaskType: "scheduled", Status: "pending", ScheduledFireAt: &future, CreatedAt: now})
	seedRunningRecord(t, db, &orm.TaskCenterTask{ID: "waiting-input", ConversationID: "waiting", UserID: "u1", TaskType: "scheduled", Status: "waiting_inputs", CreatedAt: now})
	for _, id := range []string{"chat", "child"} {
		if err := setChatRuntimeStatus(context.Background(), cache, id, "history-"+id, "generating", "", "run-"+id, nil); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Model(&orm.Conversation{}).Where("id = ?", "child").Updates(map[string]any{"parent_conversation_id": "parent", "relation_type": "sidechat"}).Error; err != nil {
		t.Fatal(err)
	}
	assertRunningStatuses(t, db, cache, map[string]string{"chat": "running", "external": "running", "subagent": "running", "work": "running", "future": "idle", "waiting": "idle", "parent": "idle", "child": "running", "idle": "idle"})
	// Finishing the primary reply must not hide a still-running subtask.
	seedRunningRecord(t, db, &orm.SubAgentTask{ID: "chat-child-task", ConversationID: "chat", AgentType: "research", Status: "running"})
	if err := setChatRuntimeStatus(context.Background(), cache, "chat", "history-chat", "completed", "", "run-chat", nil); err != nil {
		t.Fatal(err)
	}
	assertRunningStatuses(t, db, cache, map[string]string{"chat": "running"})
	for _, terminal := range []string{"succeeded", "failed", "interrupted", "canceled", "cancelled"} {
		if err := db.Model(&orm.SubAgentTask{}).Where("id = ?", "chat-child-task").Update("status", terminal).Error; err != nil {
			t.Fatal(err)
		}
		assertRunningStatuses(t, db, cache, map[string]string{"chat": "idle"})
	}
}

func TestConversationRunningReconcilesRunsWithoutChangingHistory(t *testing.T) {
	db, cache := runningStatusDB(t, "finished", "new-run", "multi", "missing-cache")
	for _, id := range []string{"finished", "new-run", "multi", "missing-cache"} {
		seedRunningRecord(t, db, &orm.ChatHistory{ID: id + "-history", ConversationID: id, RunID: "old", RunStatus: "completed"})
		if id != "missing-cache" {
			run := "old"
			if id == "new-run" {
				run = "new"
			}
			if err := setChatRuntimeStatus(context.Background(), cache, id, id+"-history", "generating", "private output", run, nil); err != nil {
				t.Fatal(err)
			}
		}
	}
	seedRunningRecord(t, db, &orm.MultiAnswersChatHistory{ID: "second", ConversationID: "multi", RunID: "second-run", RunStatus: "generating"})
	if err := setChatRuntimeStatus(context.Background(), cache, "multi", "second", "generating", "", "second-run", nil); err != nil {
		t.Fatal(err)
	}
	if err := db.Model(&orm.ChatHistory{}).Where("id = ?", "missing-cache-history").Update("run_status", "generating").Error; err != nil {
		t.Fatal(err)
	}
	assertRunningStatuses(t, db, cache, map[string]string{"finished": "idle", "new-run": "running", "multi": "running", "missing-cache": "unknown"})
	value, err := getChatStatus(context.Background(), cache, "finished", "finished-history")
	if err != nil || value.Status != "generating" {
		t.Fatalf("read-only endpoint changed cache: %+v %v", value, err)
	}
	if err := db.Model(&orm.MultiAnswersChatHistory{}).Where("id = ?", "second").Update("run_status", "cancelled").Error; err != nil {
		t.Fatal(err)
	}
	assertRunningStatuses(t, db, cache, map[string]string{"multi": "idle"})
}

func TestConversationRunningUsesEffectiveWorkflowAttempts(t *testing.T) {
	db, cache := runningStatusDB(t, "native", "hosted", "superseded", "orphan", "finished", "waiting", "driver", "failed-sibling")
	now := time.Now()
	for _, id := range []string{"native", "hosted", "superseded", "orphan", "finished", "waiting", "driver", "failed-sibling"} {
		status := "active"
		if id == "finished" {
			status = "completed"
		}
		if id == "waiting" {
			status = "waiting"
		}
		if id == "failed-sibling" || id == "driver" {
			status = "failed"
		}
		seedRunningRecord(t, db, &orm.WorkflowSession{ID: id + "-session", ConversationID: id, Status: status, CreateUserID: "u1", CreatedAt: now})
		sessionID := id + "-session"
		seedRunningRecord(t, db, &orm.TaskCenterTask{ID: id + "-task-center", ConversationID: id, UserID: "u1", WorkflowSessionID: &sessionID, TaskType: "workflow_run", Status: "running", CreatedAt: now})
		if id == "waiting" || id == "driver" {
			continue
		}
		seedRunningRecord(t, db, &orm.WorkflowSessionStep{ID: id + "-attempt", SessionID: sessionID, StepID: "step", TaskID: id + "-adapter", Attempt: 1, Status: "running", Validity: "effective"})
	}
	seedRunningRecord(t, db, &orm.SubAgentTask{ID: "native-adapter", ConversationID: "native", AgentType: "workflow_step", Status: "running"})
	seedRunningRecord(t, db, &orm.SubAgentTask{ID: "orphan-adapter", ConversationID: "orphan", AgentType: "workflow_step", Status: "succeeded"})
	seedRunningRecord(t, db, &orm.WorkflowSessionStep{ID: "new-attempt", SessionID: "superseded-session", StepID: "step", Attempt: 2, Status: "succeeded", Validity: "effective"})
	driver, _ := json.Marshal(workflow.DriverActivity{SessionID: "driver-session", ExpiresAt: now.Add(time.Minute).Unix()})
	if err := cache.HSet(context.Background(), workflow.DriverActivityKey("driver"), map[string]any{"token": string(driver)}, time.Minute); err != nil {
		t.Fatal(err)
	}
	assertRunningStatuses(t, db, cache, map[string]string{"native": "running", "hosted": "running", "superseded": "idle", "orphan": "idle", "finished": "idle", "waiting": "idle", "driver": "running", "failed-sibling": "running"})
	if err := db.Model(&orm.WorkflowSession{}).Where("id = ?", "driver-session").Update("status", "stopped").Error; err != nil {
		t.Fatal(err)
	}
	assertRunningStatuses(t, db, cache, map[string]string{"driver": "idle"})
}

func TestConversationRunningUnavailableStateIsNotCompletion(t *testing.T) {
	db, cache := runningStatusDB(t, "unknown", "known")
	seedRunningRecord(t, db, &orm.ExternalChatRun{ID: "known-run", ConversationID: "known", ActorUserID: "u1", Status: "running"})
	cache.Close()
	assertRunningStatuses(t, db, cache, map[string]string{"unknown": "unknown", "known": "running"})
	assertRunningStatuses(t, db, nil, map[string]string{"unknown": "unknown", "known": "running"})
}

func TestConversationRunningKeepsAmbiguousHistoricalTasksRunning(t *testing.T) {
	db, cache := runningStatusDB(t, "single", "ambiguous", "later-turn", "foreign")
	now := time.Now().Add(-time.Minute)
	for _, id := range []string{"single", "ambiguous", "later-turn", "foreign"} {
		seedRunningRecord(t, db, &orm.TaskCenterTask{ID: id + "-task", ConversationID: id, UserID: "u1", TaskType: "background_chat", Status: "running", CreatedAt: now})
		owner := "u1"
		if id == "foreign" {
			owner = "u2"
		}
		seedRunningRecord(t, db, &orm.WorkflowSession{ID: id + "-session", ConversationID: id, CreateUserID: owner, Status: "completed", CreatedAt: now.Add(10 * time.Second)})
	}
	seedRunningRecord(t, db, &orm.WorkflowSession{ID: "second-session", ConversationID: "ambiguous", CreateUserID: "u1", Status: "completed", CreatedAt: now.Add(20 * time.Second)})
	seedRunningRecord(t, db, &orm.TaskCenterTask{ID: "next-turn", ConversationID: "later-turn", UserID: "u1", TaskType: "background_chat", Status: "succeeded", CreatedAt: now.Add(time.Second)})
	assertRunningStatuses(t, db, cache, map[string]string{"single": "idle", "ambiguous": "running", "later-turn": "running", "foreign": "running"})
}

func TestConversationRunningCacheMetadataFailuresStayScoped(t *testing.T) {
	db, cache := runningStatusDB(t, "malformed", "running", "stale", "terminal")
	now := time.Now()
	for _, id := range []string{"malformed", "running", "stale", "terminal"} {
		fields := map[string]any{}
		if id == "malformed" || id == "running" {
			fields["broken"] = "invalid json"
		}
		if id != "malformed" {
			value := ChatStatus{Status: "generating", RunID: id + "-run", LastUpdate: now.Unix(), CurrentResult: "private content"}
			if id == "stale" {
				value.LastUpdate = now.Add(-chatCacheExpireTime).Unix()
			}
			if id == "terminal" {
				value.Status = "completed"
			}
			raw, _ := json.Marshal(value)
			fields[id+"-history"] = string(raw)
			seedRunningRecord(t, db, &orm.ChatHistory{ID: id + "-history", ConversationID: id, RunID: value.RunID, RunStatus: "generating"})
		}
		if err := cache.HSet(t.Context(), chatStatusKey(id), fields, time.Hour); err != nil {
			t.Fatal(err)
		}
	}
	assertRunningStatuses(t, db, cache, map[string]string{"malformed": "unknown", "running": "running", "stale": "unknown", "terminal": "idle"})
}

func TestBatchConversationStatusRequestScopeAndBounds(t *testing.T) {
	db, cache := runningStatusDB(t, "mine", "foreign", "archived", "deleted")
	store.Init(db, nil, cache)
	t.Cleanup(func() { store.Init(nil, nil, nil) })
	for id, patch := range map[string]map[string]any{
		"foreign": {"create_user_id": "u2"}, "archived": {"archived_at": time.Now()}, "deleted": {"deleted_at": time.Now()},
	} {
		if err := db.Model(&orm.Conversation{}).Where("id = ?", id).Updates(patch).Error; err != nil {
			t.Fatal(err)
		}
	}
	call := func(body string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodPost, "/api/core/conversations:batchStatus", strings.NewReader(body))
		r.Header.Set("X-User-Id", "u1")
		w := httptest.NewRecorder()
		BatchConversationStatus(w, r)
		return w
	}
	before := orm.Conversation{}
	db.First(&before, "id = ?", "mine")
	w := call(`{"conversation_ids":["mine","mine","foreign","missing","archived","deleted"]}`)
	if w.Code != 200 || strings.TrimSpace(w.Body.String()) != `{"statuses":[{"conversation_id":"mine","status":"idle"}]}` {
		t.Fatalf("response %d %s", w.Code, w.Body.String())
	}
	after := orm.Conversation{}
	db.First(&after, "id = ?", "mine")
	if !before.UpdatedAt.Equal(after.UpdatedAt) || after.HistoryOrder != nil || after.PinnedAt != nil {
		t.Fatal("status query changed ordering")
	}
	for _, body := range []string{`{}`, `null`, `{"conversation_ids":[]}`, `{"conversation_ids":[""]}`, `{"conversation_ids":[" mine"]}`, `{"conversation_ids":["mine"]} {}`, `{"conversation_ids":["mine"],"extra":true}`,
		`{"conversation_ids":[` + strings.Repeat(`"mine",`, 100) + `"mine"]}`, `{"conversation_ids":["` + strings.Repeat("a", 33000) + `"]}`} {
		if got := call(body); got.Code != http.StatusBadRequest {
			t.Errorf("invalid request accepted: %d", got.Code)
		}
	}
}
