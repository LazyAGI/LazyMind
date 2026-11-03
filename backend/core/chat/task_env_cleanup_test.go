package chat

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"gorm.io/gorm"

	"lazymind/core/common/orm"
	"lazymind/core/store"
	"lazymind/core/taskcenter"
)

func TestChatExecutionIsTrackedBeforeUpstreamAndReleasedAfterCompletion(t *testing.T) {
	for _, streaming := range []bool{false, true} {
		t.Run(map[bool]string{false: "nonstream", true: "stream"}[streaming], func(t *testing.T) {
			db := newChatExecutionTestDB(t)
			shared := newRunDecisionTestStore(t)
			store.Init(db.DB, nil, shared)
			t.Cleanup(func() { store.Init(nil, nil, nil) })
			seedAvailableChatModel(t, db.DB, "user-1", "provider", "group", "model", "Test", "Test", "test-chat", "llm", true, "test-only-key")
			seedSelectedChatModel(t, db.DB, "user-1", "model", false)
			called := false
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/api/chat/stream" {
					_ = json.NewEncoder(w).Encode(map[string]any{"items": []any{}, "tool_groups": []any{}, "data": map[string]any{"items": []any{}}})
					return
				}
				var payload LazyChatRequest
				if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
					t.Error(err)
					return
				}
				called = true
				active, err := shared.HGetAll(r.Context(), "rag/chat/executions:tracked-chat")
				if err != nil || active[payload.Conversation.SessionID] != "active" {
					t.Errorf("upstream execution was not tracked: %v %v", active, err)
				}
				w.Header().Set("Content-Type", "application/x-ndjson")
				_ = json.NewEncoder(w).Encode(map[string]any{"code": 200, "data": map[string]any{"text": "answer"}})
				_ = json.NewEncoder(w).Encode(map[string]any{"code": 200, "data": map[string]any{"runtime_event": completedRunEvent(payload.Conversation.RunID, true)}})
			}))
			defer server.Close()
			for _, name := range []string{"LAZYMIND_CHAT_SERVICE_URL", "LAZYMIND_AUTH_SERVICE_URL", "LAZYMIND_SCAN_CONTROL_PLANE_URL"} {
				t.Setenv(name, server.URL)
			}
			body, _ := json.Marshal(map[string]any{"conversation_id": "tracked-chat", "query": "test execution", "stream": streaming})
			recorder := httptest.NewRecorder()
			ChatConversations(recorder, sidechatRequest(http.MethodPost, "/api/core/conversations:chat", "user-1", string(body), nil))
			if recorder.Code != http.StatusOK || !called || !strings.Contains(recorder.Body.String(), "answer") {
				t.Fatalf("status=%d upstream=%t body=%s", recorder.Code, called, recorder.Body.String())
			}
			active, err := shared.HGetAll(t.Context(), "rag/chat/executions:tracked-chat")
			if err != nil || len(active) != 0 {
				t.Fatalf("finished execution was retained: %v %v", active, err)
			}
		})
	}
}

func TestTaskRemovalExternalExecutorDoesNotRequirePythonChat(t *testing.T) {
	db := orm.MigrateTestDB(t, &orm.Conversation{}, &orm.ExternalChatRun{})
	store.Init(db.DB, nil, nil)
	previousTrash, previousRemoval := taskcenter.OnConversationTrashHook, taskcenter.OnRemovalCancelHook
	t.Cleanup(func() {
		store.Init(nil, nil, nil)
		taskcenter.OnConversationTrashHook, taskcenter.OnRemovalCancelHook = previousTrash, previousRemoval
	})
	now := time.Now().UTC()
	if err := db.Create(&orm.Conversation{ID: "external-removed", ChatExecutor: ChatExecutorCodex, BaseModel: orm.BaseModel{CreateUserID: "u1", DeletedAt: &now}}).Error; err != nil {
		t.Fatal(err)
	}
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls++; w.WriteHeader(http.StatusServiceUnavailable) }))
	defer server.Close()
	t.Setenv("LAZYMIND_CHAT_SERVICE_URL", server.URL)
	RegisterTaskCenterEnvCleanup()
	if err := taskcenter.OnRemovalCancelHook(t.Context(), "u1", "external-removed", now, nil); err != nil {
		t.Fatal(err)
	}
	if calls != 0 {
		t.Fatal("external executor cancellation unnecessarily depends on Python")
	}
}

func TestTaskRemovalUsesStoredExecutionAndPreservesNewerExecution(t *testing.T) {
	for _, status := range []int{http.StatusOK, http.StatusServiceUnavailable} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			db := newChatExecutionTestDB(t)
			shared := newRunDecisionTestStore(t)
			store.Init(db.DB, nil, shared)
			previousTrash, previousRemoval := taskcenter.OnConversationTrashHook, taskcenter.OnRemovalCancelHook
			t.Cleanup(func() {
				store.Init(nil, nil, nil)
				taskcenter.OnConversationTrashHook, taskcenter.OnRemovalCancelHook = previousTrash, previousRemoval
			})
			if err := db.Create(&orm.Conversation{ID: "removed", BaseModel: orm.BaseModel{CreateUserID: "owner"}}).Error; err != nil {
				t.Fatal(err)
			}
			for _, id := range []string{"removed_100", "removed_200"} {
				if err := taskcenter.RegisterChatExecution(t.Context(), db.DB, "owner", "removed", id); err != nil {
					t.Fatal(err)
				}
			}
			if err := db.Model(&orm.Conversation{}).Where("id=?", "removed").Update("deleted_at", time.Now().UTC()).Error; err != nil {
				t.Fatal(err)
			}
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				var payload map[string]string
				if err := json.NewDecoder(r.Body).Decode(&payload); err != nil || len(payload) != 1 || payload["task_id"] != "removed_100" {
					t.Errorf("wrong cancellation target: %v %v", payload, err)
				}
				w.WriteHeader(status)
				_, _ = w.Write([]byte(`{"ok":true}`))
			}))
			defer server.Close()
			t.Setenv("LAZYMIND_CHAT_SERVICE_URL", server.URL)
			RegisterTaskCenterEnvCleanup()
			err := taskcenter.OnRemovalCancelHook(t.Context(), "owner", "removed", time.UnixMilli(150), []string{"removed_100"})
			if calls != 1 || (err != nil) != (status != http.StatusOK) {
				t.Fatalf("calls=%d err=%v", calls, err)
			}
			active, err := shared.HGetAll(t.Context(), "rag/chat/executions:removed")
			want := 1
			if status != http.StatusOK {
				want = 2
			}
			if err != nil || len(active) != want || active["removed_200"] != "active" {
				t.Fatalf("new execution changed or retry lost its target: %v %v", active, err)
			}
		})
	}
}

func TestTaskCenterRemovalClearsSessionEnvAfterCommit(t *testing.T) {
	for _, tc := range []struct {
		name       string
		status     string
		userID     string
		failUpdate bool
		wantStatus int
	}{
		{"completed task", "succeeded", "u1", false, http.StatusOK},
		{"running task", "running", "u1", false, http.StatusOK},
		{"foreign task", "succeeded", "other-user", false, http.StatusNotFound},
		{"transaction failure", "succeeded", "u1", true, http.StatusInternalServerError},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db := recoveryTestDB(t)
			seedRecoveryConversation(t, db, "task-env-conversation", true)
			if err := db.Create(&orm.TaskCenterTask{
				ID: "task-env", UserID: "u1", ConversationID: "task-env-conversation",
				TaskType: "scheduled", Status: tc.status,
			}).Error; err != nil {
				t.Fatal(err)
			}
			previousTrash, previousCancel, previousRemoval := taskcenter.OnConversationTrashHook, taskcenter.OnCancelHook, taskcenter.OnRemovalCancelHook
			taskcenter.OnCancelHook = nil
			t.Cleanup(func() {
				taskcenter.OnConversationTrashHook, taskcenter.OnCancelHook = previousTrash, previousCancel
				taskcenter.OnRemovalCancelHook = previousRemoval
			})
			RegisterTaskCenterEnvCleanup()
			notify := taskcenter.OnConversationTrashHook
			calls := 0
			taskcenter.OnConversationTrashHook = func(id string) {
				calls++
				var conversation orm.Conversation
				if err := db.First(&conversation, "id = ?", id).Error; err != nil || conversation.DeletedAt == nil {
					t.Fatalf("cleanup ran before trash was committed: %v", err)
				}
				notify(id)
			}
			seen := make(chan []string, 1)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/api/chat/session-env:clear" {
					http.NotFound(w, r)
					return
				}
				var payload struct {
					IDs []string `json:"conversation_ids"`
				}
				if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
					http.Error(w, "invalid payload", http.StatusBadRequest)
					return
				}
				seen <- payload.IDs
				_, _ = w.Write([]byte(`{"ok":true}`))
			}))
			defer server.Close()
			t.Setenv("LAZYMIND_CHAT_SERVICE_URL", server.URL)
			if tc.failUpdate {
				if err := db.Callback().Update().Before("gorm:update").Register("fail_task_archive", func(tx *gorm.DB) {
					if tx.Statement.Table == "task_center_tasks" {
						tx.AddError(errors.New("synthetic archive failure"))
					}
				}); err != nil {
					t.Fatal(err)
				}
			}
			rec, req := recoveryRequest(http.MethodPost, "/task-center/tasks/task-env:remove", nil, nil)
			req.Header.Set("X-User-Id", tc.userID)
			taskcenter.RemoveTaskHandler(rec, req)
			if rec.Code != tc.wantStatus {
				t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
			}
			if tc.wantStatus != http.StatusOK {
				if calls != 0 {
					t.Fatal("failed removal cleared session credentials")
				}
				var conversation orm.Conversation
				if err := db.First(&conversation, "id = ?", "task-env-conversation").Error; err != nil || conversation.DeletedAt != nil {
					t.Fatalf("failed removal did not preserve conversation: %v", err)
				}
				return
			}
			if calls != 1 {
				t.Fatalf("cleanup calls=%d", calls)
			}
			select {
			case ids := <-seen:
				if len(ids) != 1 || ids[0] != "task-env-conversation" {
					t.Fatalf("unexpected cleanup scope: %v", ids)
				}
			case <-time.After(2 * time.Second):
				t.Fatal("missing task-center session env cleanup request")
			}
		})
	}
}
