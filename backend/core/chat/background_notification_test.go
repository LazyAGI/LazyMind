package chat

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"gorm.io/gorm"
	"lazymind/core/common/orm"
	"lazymind/core/taskcenter"
)

func TestBackgroundCompletionNotificationKeepsLifecycleBoundaries(t *testing.T) {
	for _, executor := range []string{"native", "external"} {
		for _, event := range []string{"succeeded", "failed", "canceled"} {
			t.Run(executor+"/"+event, func(t *testing.T) {
				_, db := newExternalChatTestApplication(t)
				now := time.Now().UTC()
				sessionID := "workflow"
				tasks := []orm.TaskCenterTask{
					{ID: "plain", UserID: "user-1", ConversationID: "conversation-1", TaskType: "background_chat", Status: "running", CreatedAt: now, UpdatedAt: now},
					{ID: "linked", UserID: "user-1", ConversationID: "conversation-1", TaskType: "background_chat", Status: "running", WorkflowSessionID: &sessionID},
					{ID: "terminal", UserID: "user-1", ConversationID: "conversation-1", TaskType: "background_chat", Status: "skipped"},
					{ID: "archived", UserID: "user-1", ConversationID: "conversation-1", TaskType: "background_chat", Status: "running", ArchivedAt: &now},
				}
				tasks[0].ProgressJSON, _ = json.Marshal(map[string]any{"_background_execution": map[string]any{"version": 1, "parts": []any{map[string]any{"history_id": "history", "run_id": "run"}}}})
				if err := db.Create(&tasks).Error; err != nil {
					t.Fatal(err)
				}
				finishBackgroundNotificationTest(t, db, executor, event, now)
				var notices []orm.TaskNotification
				if err := db.Find(&notices).Error; err != nil {
					t.Fatal(err)
				}
				wantCount := 1
				if event == "canceled" {
					wantCount = 0
				}
				if len(notices) != wantCount {
					t.Fatalf("notices=%#v, want %d", notices, wantCount)
				}
				if wantCount > 0 && (notices[0].TaskID != "plain" || notices[0].Channel != "desktop" || notices[0].Event != event) {
					t.Fatalf("wrong completion notification: %#v", notices)
				}
				var saved []orm.TaskCenterTask
				if err := db.Order("id").Find(&saved).Error; err != nil {
					t.Fatal(err)
				}
				for _, task := range saved {
					want := map[string]string{"plain": event, "linked": "running", "terminal": "skipped", "archived": "running"}[task.ID]
					if task.Status != want {
						t.Fatalf("%s status=%s, want %s", task.ID, task.Status, want)
					}
				}
			})
		}
	}
}

func finishBackgroundNotificationTest(t *testing.T, db *gorm.DB, executor, event string, now time.Time) {
	t.Helper()
	status := map[string]string{"succeeded": "completed", "failed": "failed", "canceled": "cancelled"}[event]
	if executor == "external" {
		if event == "canceled" {
			status = "stopped"
		}
		if err := db.Transaction(func(tx *gorm.DB) error {
			_, err := finalizeExternalChatHistory(tx, &orm.ExternalChatRun{
				ID: "run", HistoryID: "history", ConversationID: "conversation-1", ActorUserID: "user-1",
				Status: status, Sequence: 1, HistoryExt: json.RawMessage(`{}`),
			}, now)
			return err
		}); err != nil {
			t.Fatal(err)
		}
		return
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprintln(w, `{"code":200,"msg":"success","data":{"text":"answer"}}`)
		terminal := map[string]any{"status": status, "reason": "normal", "partial_output": true}
		if event == "failed" {
			terminal["reason"], terminal["code"] = "runtime_failure", "synthetic_failure"
		}
		if event == "canceled" {
			terminal["reason"] = "user_cancelled"
		}
		_, _ = fmt.Fprintln(w, algorithmFrame(t, map[string]any{"runtime_event": map[string]any{
			"schema_version": 1, "event_id": "evt_test", "run_id": "run", "type": RuntimeEventRunFinished,
			"data": terminal,
		}}))
	}))
	defer server.Close()
	recorder := httptest.NewRecorder()
	streamSingleAnswer(t.Context(), t.Context(), recorder, recorder, db, nil,
		server.URL, map[string]any{"query": "question", "run_id": "run"}, "conversation-1", "question", "history",
		chatPersistTarget{HistoryID: "history", Seq: 1}, json.RawMessage(`{}`))
}

func TestBackgroundCompletionNotificationWriteFailureRollsBackTaskStatus(t *testing.T) {
	for _, executor := range []string{"native", "external"} {
		t.Run(executor, func(t *testing.T) {
			_, db := newExternalChatTestApplication(t)
			now := time.Now().UTC()
			task := orm.TaskCenterTask{ID: "plain", UserID: "user-1", ConversationID: "conversation-1", TaskType: "background_chat", Status: "running", CreatedAt: now, UpdatedAt: now}
			if err := db.Create(&task).Error; err != nil {
				t.Fatal(err)
			}
			trigger := `CREATE TRIGGER reject_background_notice BEFORE INSERT ON task_notifications BEGIN SELECT RAISE(ABORT, 'synthetic notification storage failure'); END`
			if db.Dialector.Name() == "postgres" {
				if err := db.Exec(`CREATE FUNCTION reject_background_notice() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'synthetic notification storage failure'; END $$`).Error; err != nil {
					t.Fatal(err)
				}
				trigger = `CREATE TRIGGER reject_background_notice BEFORE INSERT ON task_notifications FOR EACH ROW EXECUTE FUNCTION reject_background_notice()`
			}
			if err := db.Exec(trigger).Error; err != nil {
				t.Fatal(err)
			}
			if executor == "external" {
				err := db.Transaction(func(tx *gorm.DB) error {
					_, err := finalizeExternalChatHistory(tx, &orm.ExternalChatRun{ID: "run", HistoryID: "history", ConversationID: "conversation-1", ActorUserID: "user-1", Status: "completed", Sequence: 1, HistoryExt: json.RawMessage(`{}`)}, now)
					return err
				})
				if err != nil {
					t.Fatalf("notification storage failure aborted authoritative external result: %v", err)
				}
			} else {
				finishBackgroundNotificationTest(t, db, executor, "succeeded", now)
				var history orm.ChatHistory
				if err := db.First(&history, "id = ?", "history").Error; err != nil {
					t.Fatal(err)
				}
				if history.RunStatus != "completed" {
					t.Fatalf("authoritative history lost its completion: %#v", history)
				}
			}
			if err := db.First(&task, "id = ?", task.ID).Error; err != nil {
				t.Fatal(err)
			}
			if task.Status != "running" || task.FinishedAt != nil {
				t.Fatalf("notification failure committed task completion: %#v", task)
			}
			{
				drop := "DROP TRIGGER reject_background_notice"
				if db.Dialector.Name() == "postgres" {
					drop += " ON task_notifications"
				}
				if err := db.Exec(drop).Error; err != nil {
					t.Fatal(err)
				}
				ctx, cancel := context.WithCancel(t.Context())
				done := taskcenter.RunNotificationDelivery(ctx, db)
				defer func() { cancel(); <-done }()
				deadline := time.Now().Add(3 * time.Second)
				for time.Now().Before(deadline) {
					if err := db.First(&task, "id = ?", task.ID).Error; err != nil {
						t.Fatal(err)
					}
					if task.Status == "succeeded" {
						break
					}
					time.Sleep(10 * time.Millisecond)
				}
				if task.Status != "succeeded" {
					t.Fatalf("committed history was not recovered after notification storage returned: %#v", task)
				}
				var count int64
				if err := db.Model(&orm.TaskNotification{}).Where("task_id = ? AND channel = 'desktop'", task.ID).Count(&count).Error; err != nil {
					t.Fatal(err)
				}
				if count != 1 {
					t.Fatalf("recovery desktop notices=%d, want 1", count)
				}
			}
		})
	}
}

func TestBackgroundUpstreamSetupFailureCompletesTaskAndDesktopNotice(t *testing.T) {
	_, db := newExternalChatTestApplication(t)
	task := orm.TaskCenterTask{ID: "plain", UserID: "user-1", ConversationID: "conversation-1", TaskType: "background_chat", Status: "running"}
	if err := db.Create(&task).Error; err != nil {
		t.Fatal(err)
	}
	recorder := httptest.NewRecorder()
	streamSingleAnswer(t.Context(), t.Context(), recorder, recorder, db, nil, ":invalid-upstream-url",
		map[string]any{"query": "question", "run_id": "run"}, "conversation-1", "question", "history",
		chatPersistTarget{HistoryID: "history", Seq: 1}, json.RawMessage(`{}`))
	if err := db.First(&task, "id = ?", task.ID).Error; err != nil {
		t.Fatal(err)
	}
	if task.Status != "failed" || task.FinishedAt == nil {
		t.Fatalf("setup failure left background task active: %#v", task)
	}
	var notice orm.TaskNotification
	if err := db.First(&notice, "task_id = ?", task.ID).Error; err != nil {
		t.Fatal(err)
	}
	if notice.Event != "failed" || notice.Channel != "desktop" {
		t.Fatalf("wrong setup failure notice: %#v", notice)
	}
}

func TestBackgroundDualAnswerCompletionProducesOneDesktopNotice(t *testing.T) {
	for _, secondaryStatus := range []string{"completed", "failed", "cancelled"} {
		t.Run(secondaryStatus, func(t *testing.T) {
			_, db := newExternalChatTestApplication(t)
			task := orm.TaskCenterTask{ID: "plain", UserID: "user-1", ConversationID: "conversation-1", TaskType: "background_chat", Status: "running"}
			if err := db.Create(&task).Error; err != nil {
				t.Fatal(err)
			}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var request LazyChatRequest
				if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
					t.Error(err)
					return
				}
				terminal := map[string]any{"status": "completed", "reason": "normal", "partial_output": true}
				if request.Conversation.RunID != "primary" {
					terminal["status"] = secondaryStatus
					if secondaryStatus == "failed" {
						terminal["reason"], terminal["code"] = "runtime_failure", "synthetic_failure"
					}
					if secondaryStatus == "cancelled" {
						terminal["reason"] = "user_cancelled"
					}
				}
				_, _ = fmt.Fprintln(w, `{"code":200,"msg":"success","data":{"text":"answer"}}`)
				_, _ = fmt.Fprintln(w, algorithmFrame(t, map[string]any{"runtime_event": map[string]any{
					"schema_version": 1, "event_id": "evt_test", "run_id": request.Conversation.RunID, "type": RuntimeEventRunFinished, "data": terminal,
				}}))
			}))
			defer server.Close()
			recorder := httptest.NewRecorder()
			streamDualAnswer(t.Context(), t.Context(), recorder, recorder, db, nil, server.URL,
				map[string]any{"query": "question", "run_id": "primary", "secondary_run_id": "secondary"}, "conversation-1", "question", "history", "secondary-history",
				chatPersistTarget{HistoryID: "history", Seq: 1}, json.RawMessage(`{}`))
			if err := db.First(&task, "id = ?", task.ID).Error; err != nil {
				t.Fatal(err)
			}
			wantStatus := map[string]string{"completed": "succeeded", "failed": "failed", "cancelled": "canceled"}[secondaryStatus]
			if task.Status != wantStatus {
				t.Fatalf("dual completion task status=%s, want %s", task.Status, wantStatus)
			}
			var notices []orm.TaskNotification
			if err := db.Find(&notices).Error; err != nil {
				t.Fatal(err)
			}
			wantCount := 1
			if secondaryStatus == "cancelled" {
				wantCount = 0
			}
			if len(notices) != wantCount || wantCount > 0 && (notices[0].TaskID != task.ID || notices[0].Channel != "desktop" || notices[0].Event != wantStatus) {
				t.Fatalf("dual completion produced wrong notices: %#v", notices)
			}
		})
	}
}
