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
)

func TestChatCompletionDoesNotFinishLinkedWorkflowTask(t *testing.T) {
	for _, executor := range []string{"native", "external"} {
		t.Run(executor, func(t *testing.T) {
			_, db := newExternalChatTestApplication(t)
			now := time.Now().UTC()
			sessionID := "writer"
			tasks := []orm.TaskCenterTask{
				{ID: "linked", UserID: "user-1", ConversationID: "conversation-1", TaskType: "background_chat",
					WorkflowSessionID: &sessionID, Status: "running", CreatedAt: now, UpdatedAt: now},
				{ID: "plain", UserID: "user-1", ConversationID: "conversation-1", TaskType: "background_chat",
					Status: "running", CreatedAt: now, UpdatedAt: now},
			}
			if err := db.Create(&tasks).Error; err != nil {
				t.Fatal(err)
			}
			if executor == "external" {
				err := db.Transaction(func(tx *gorm.DB) error {
					_, err := finalizeExternalChatHistory(tx, &orm.ExternalChatRun{
						ID: "run", HistoryID: "history", ConversationID: "conversation-1", ActorUserID: "user-1",
						Status: "completed", Sequence: 1, HistoryExt: json.RawMessage(`{}`),
					}, now)
					return err
				})
				if err != nil {
					t.Fatal(err)
				}
			} else {
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					_, _ = fmt.Fprintln(w, `{"code":200,"msg":"success","data":{"text":"answer"}}`)
					_, _ = fmt.Fprintln(w, runFinishedFrame(t, "run"))
				}))
				defer server.Close()
				recorder := httptest.NewRecorder()
				streamSingleAnswer(context.Background(), context.Background(), recorder, recorder, db, nil,
					server.URL, map[string]any{"query": "question", "run_id": "run"}, "conversation-1", "question", "history",
					chatPersistTarget{HistoryID: "history", Seq: 1}, json.RawMessage(`{}`))
			}
			var linked, plain orm.TaskCenterTask
			if err := db.First(&linked, "id = ?", "linked").Error; err != nil {
				t.Fatal(err)
			}
			if err := db.First(&plain, "id = ?", "plain").Error; err != nil {
				t.Fatal(err)
			}
			if linked.Status != "running" || linked.FinishedAt != nil || plain.Status != "succeeded" || plain.FinishedAt == nil {
				t.Fatalf("chat terminal overwrote workflow lifecycle: linked=%#v plain=%#v", linked, plain)
			}
			var notices []orm.TaskNotification
			if err := db.Find(&notices).Error; err != nil {
				t.Fatal(err)
			}
			if len(notices) != 1 || notices[0].TaskID != plain.ID || notices[0].Channel != "desktop" || notices[0].Event != "succeeded" {
				t.Fatalf("plain background completion did not produce its desktop notice: %#v", notices)
			}
		})
	}
}
