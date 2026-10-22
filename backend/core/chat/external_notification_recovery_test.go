package chat

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/mux"
	"lazymind/core/common/orm"
	"lazymind/core/store"
	"lazymind/core/taskcenter"
)

func TestExternalTerminalSurvivesNotificationStorageFailure(t *testing.T) {
	for _, outcome := range []string{"completed", "failed"} {
		t.Run(outcome, func(t *testing.T) {
			app, db := newExternalChatTestApplication(t)
			store.Init(db, nil, nil)
			t.Cleanup(func() { store.Init(nil, nil, nil) })
			createExternalChatTestRun(t, app, "run")
			job, err := app.claim(t.Context(), "user-1", ChatExecutorCodex, "host")
			if err != nil || job == nil {
				t.Fatalf("claim: %#v %v", job, err)
			}
			task := orm.TaskCenterTask{ID: "task", UserID: "user-1", ConversationID: "conversation-1", TaskType: "background_chat", Status: "running", CreatedAt: time.Now().UTC()}
			if err := db.Create(&task).Error; err != nil {
				t.Fatal(err)
			}
			if _, err := app.appendEvent(t.Context(), "user-1", "run", "host", job.LeaseToken, externalChatEvent{EventID: "message", Type: "message", Text: "durable result"}); err != nil {
				t.Fatal(err)
			}
			trigger := `CREATE TRIGGER reject_external_notice BEFORE INSERT ON task_notifications BEGIN SELECT RAISE(ABORT, 'synthetic notification storage failure'); END`
			drop := "DROP TRIGGER reject_external_notice"
			if db.Dialector.Name() == "postgres" {
				if err := db.Exec(`CREATE FUNCTION reject_external_notice() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'synthetic notification storage failure'; END $$`).Error; err != nil {
					t.Fatal(err)
				}
				trigger = `CREATE TRIGGER reject_external_notice BEFORE INSERT ON task_notifications FOR EACH ROW EXECUTE FUNCTION reject_external_notice()`
				drop += " ON task_notifications"
			}
			if err := db.Exec(trigger).Error; err != nil {
				t.Fatal(err)
			}
			payload, _ := json.Marshal(map[string]any{"host_id": "host", "lease_token": job.LeaseToken, "event_id": "terminal", "type": outcome})
			for range 2 {
				req := mux.SetURLVars(httptest.NewRequest("POST", "/external-chat/runs/run:event", strings.NewReader(string(payload))), map[string]string{"run_id": "run"})
				req.Header.Set("X-User-Id", "user-1")
				response := httptest.NewRecorder()
				PublishExternalChatEvent(response, req)
				if response.Code != http.StatusOK {
					t.Fatalf("optional notice failure rejected terminal: %d %s", response.Code, response.Body.String())
				}
			}
			var history orm.ChatHistory
			if err := db.First(&history, "id = ?", "history-run").Error; err != nil {
				t.Fatal(err)
			}
			if history.RunStatus != outcome || history.Result != "durable result" {
				t.Fatalf("lost authoritative result: %#v", history)
			}
			if err := db.Exec(drop).Error; err != nil {
				t.Fatal(err)
			}
			for range 2 {
				if _, err := taskcenter.ReconcileOrdinaryNotifications(t.Context(), db, ""); err != nil {
					t.Fatal(err)
				}
			}
			if err := db.First(&task, "id = ?", task.ID).Error; err != nil {
				t.Fatal(err)
			}
			want := "succeeded"
			if outcome == "failed" {
				want = "failed"
			}
			var notices []orm.TaskNotification
			if err := db.Where("task_id = ?", task.ID).Find(&notices).Error; err != nil {
				t.Fatal(err)
			}
			if task.Status != want || len(notices) != 1 || notices[0].Event != want {
				t.Fatalf("recovery: task=%s notices=%#v", task.Status, notices)
			}
			if reclaimed, err := app.claim(t.Context(), "user-1", ChatExecutorCodex, "host"); err != nil || reclaimed != nil {
				t.Fatalf("terminal work became runnable again: %#v %v", reclaimed, err)
			}
		})
	}
}
