package chat

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"gorm.io/gorm"

	"lazymind/core/common/orm"
	"lazymind/core/taskcenter"
)

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
			previousTrash, previousCancel := taskcenter.OnConversationTrashHook, taskcenter.OnCancelHook
			taskcenter.OnCancelHook = nil
			t.Cleanup(func() {
				taskcenter.OnConversationTrashHook, taskcenter.OnCancelHook = previousTrash, previousCancel
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
