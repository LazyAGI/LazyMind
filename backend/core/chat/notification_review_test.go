package chat

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"gorm.io/gorm"
	"lazymind/core/common/orm"
	"lazymind/core/scheduler"
	"lazymind/core/store"
	"lazymind/core/taskcenter"
	"lazymind/core/workflow"
)

func notificationReviewChat(t *testing.T, db *gorm.DB, calls *atomic.Int32) {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		_, _ = fmt.Fprintln(w, `{"code":200,"msg":"success","data":{"text":"最终结果：完成联调。"}}`)
		_, _ = fmt.Fprintln(w, runFinishedFrame(t, "review-run"))
	}))
	defer server.Close()
	recorder := httptest.NewRecorder()
	streamSingleAnswer(t.Context(), t.Context(), recorder, recorder, db, nil, server.URL,
		map[string]any{"query": "question", "run_id": "review-run"}, "review-conv", "question", "review-history",
		chatPersistTarget{HistoryID: "review-history", Seq: 1}, json.RawMessage(`{}`))
}

func notificationReviewRun(t *testing.T, db *gorm.DB, config *string, workflowID *string) {
	t.Helper()
	now := time.Now().UTC()
	scheduleID := "review-schedule"
	revision := int64(0)
	if config != nil {
		revision = 1
	}
	for _, row := range []any{
		&orm.Conversation{ID: "review-conv", BaseModel: orm.BaseModel{CreateUserID: "owner", CreatedAt: now, UpdatedAt: now}},
		&orm.UserSchedule{ID: scheduleID, UserID: "owner", Name: "Review", CronExpr: "0 9 * * *", Timezone: "UTC", PromptTemplate: "review"},
		&orm.TaskCenterTask{ID: "review-task", UserID: "owner", ConversationID: "review-conv", TaskType: "scheduled",
			ScheduleID: &scheduleID, Status: "running", NotificationConfig: config, NotificationRevision: revision,
			WorkflowSessionID: workflowID, CreatedAt: now, UpdatedAt: now},
	} {
		if err := db.Create(row).Error; err != nil {
			t.Fatal(err)
		}
	}
}

func TestNotificationReviewChatWriteFailureRecoversWithoutRerun(t *testing.T) {
	for _, failureEnabled := range []bool{false, true} {
		t.Run(fmt.Sprint(failureEnabled), func(t *testing.T) {
			db := orm.MigrateAllModelsForTest(t).DB
			config := fmt.Sprintf(`{"events":{"succeeded":{"enabled":true,"content":"summary"},"failed":{"enabled":%t,"content":"summary"}},"channels":{"desktop":{"enabled":true}}}`, failureEnabled)
			notificationReviewRun(t, db, &config, nil)
			// Database-native failure leaves PostgreSQL's transaction aborted too.
			trigger := `CREATE TRIGGER reject_review_notice BEFORE INSERT ON task_notifications WHEN NEW.event = 'succeeded' BEGIN SELECT RAISE(ABORT, 'synthetic notification storage failure'); END`
			drop := `DROP TRIGGER reject_review_notice`
			if db.Dialector.Name() == "postgres" {
				if err := db.Exec(`CREATE FUNCTION reject_review_notice() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.event = 'succeeded' THEN RAISE EXCEPTION 'synthetic notification storage failure'; END IF; RETURN NEW; END $$`).Error; err != nil {
					t.Fatal(err)
				}
				trigger = `CREATE TRIGGER reject_review_notice BEFORE INSERT ON task_notifications FOR EACH ROW EXECUTE FUNCTION reject_review_notice()`
				drop = `DROP TRIGGER reject_review_notice ON task_notifications`
			}
			if err := db.Exec(trigger).Error; err != nil {
				t.Fatal(err)
			}
			var calls atomic.Int32
			notificationReviewChat(t, db, &calls)
			var run orm.TaskCenterTask
			if err := db.First(&run, "id = ?", "review-task").Error; err != nil {
				t.Fatal(err)
			}
			if run.Status != "running" || run.FinishedAt != nil {
				t.Fatalf("notification outage corrupted task: %#v", run)
			}
			var history orm.ChatHistory
			if err := db.First(&history, "id = ?", "review-history").Error; err != nil {
				t.Fatal(err)
			}
			if history.RunStatus != "completed" || history.Result != "最终结果：完成联调。" {
				t.Fatalf("lost authoritative history: %#v", history)
			}
			for _, model := range []any{&orm.TaskRunOutput{}, &orm.TaskNotification{}} {
				var count int64
				if err := db.Model(model).Where("task_id = ?", run.ID).Count(&count).Error; err != nil {
					t.Fatal(err)
				}
				if count != 0 {
					t.Fatalf("partial finalization committed: %T count=%d", model, count)
				}
			}
			if err := taskcenter.FinalizeScheduledConversation(t.Context(), db, run.ConversationID); err == nil {
				t.Fatal("persistent notification failure was swallowed")
			}
			if _, err := taskcenter.ReconcileScheduledNotifications(t.Context(), db, "", time.Now().UTC()); err == nil {
				t.Fatal("recovery hid notification failure")
			}
			if err := db.Exec(drop).Error; err != nil {
				t.Fatal(err)
			}
			for i := 0; i < 2; i++ {
				if _, err := taskcenter.ReconcileScheduledNotifications(t.Context(), db, "", time.Now().UTC()); err != nil {
					t.Fatal(err)
				}
				if err := taskcenter.FinalizeScheduledConversation(t.Context(), db, run.ConversationID); err != nil {
					t.Fatal(err)
				}
			}
			if err := db.First(&run, "id = ?", run.ID).Error; err != nil {
				t.Fatal(err)
			}
			if run.Status != "succeeded" || calls.Load() != 1 {
				t.Fatalf("recovery reran execution or failed: status=%s calls=%d", run.Status, calls.Load())
			}
			for _, model := range []any{&orm.TaskRunOutput{}, &orm.TaskNotification{}} {
				var count int64
				if err := db.Model(model).Where("task_id = ?", run.ID).Count(&count).Error; err != nil {
					t.Fatal(err)
				}
				if count != 1 {
					t.Fatalf("not exactly one %T: %d", model, count)
				}
			}
			notificationReviewConsumeDependency(t, db)
		})
	}
}

func TestNotificationReviewLegacyAsyncWorkflowCompletesWithoutNotifications(t *testing.T) {
	db := orm.MigrateAllModelsForTest(t).DB
	sessionID := "review-workflow"
	session := orm.WorkflowSession{ID: sessionID, ConversationID: "review-conv", WorkflowID: "writer", Status: "active", CreateUserID: "owner"}
	if err := db.Create(&session).Error; err != nil {
		t.Fatal(err)
	}
	notificationReviewRun(t, db, nil, &sessionID)
	var calls atomic.Int32
	notificationReviewChat(t, db, &calls)
	var run orm.TaskCenterTask
	if err := db.First(&run, "id = ?", "review-task").Error; err != nil {
		t.Fatal(err)
	}
	if run.Status != "running" {
		t.Fatalf("workflow prematurely completed: %s", run.Status)
	}
	// Use the production asynchronous workflow completion entry after the chat
	// stream is gone; no second chat callback or task-detail read occurs.
	_, _, completed, err := workflow.HandleWorkflowStepCreated(t.Context(), db, nil, "review-conv", "review-history", "owner",
		"", "", "", workflow.WorkflowStepParams{SessionID: sessionID, StepID: "__end__"}, nil, nil, nil, nil)
	if err != nil || !completed {
		t.Fatalf("workflow completion: completed=%v err=%v", completed, err)
	}
	if _, err := taskcenter.ReconcileScheduledNotifications(t.Context(), db, "", time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if err := db.First(&run, "id = ?", run.ID).Error; err != nil {
		t.Fatal(err)
	}
	var output orm.TaskRunOutput
	if err := db.First(&output, "task_id = ?", run.ID).Error; err != nil {
		t.Fatal(err)
	}
	if run.Status != "succeeded" || run.NotificationConfig != nil || run.NotificationRevision != 0 || output.FinalAnswerText != "最终结果：完成联调。" || output.OutputStatus != "ready" {
		t.Fatalf("legacy run not durably finalized: run=%#v output=%#v", run, output)
	}
	var count int64
	if err := db.Model(&orm.TaskNotification{}).Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 0 || calls.Load() != 1 {
		t.Fatalf("backfilled notifications or reran model: notices=%d calls=%d", count, calls.Load())
	}
	notificationReviewConsumeDependency(t, db)
}

// Exercise production dependency selection, durable input snapshots and launch
// through RunNow. Only the downstream chat transport is replaced; returning a
// controlled 503 lets the launched goroutine finish before database cleanup.
func notificationReviewConsumeDependency(t *testing.T, db *gorm.DB) {
	t.Helper()
	store.Init(db, nil, nil)
	t.Cleanup(func() { store.Init(nil, nil, nil) })
	now := time.Now().UTC()
	target := orm.UserSchedule{ID: "review-dependent", UserID: "owner", Name: "汇总", CronExpr: "0 0 * * *",
		Timezone: "UTC", PromptTemplate: "汇总已有结果", Enabled: true, CreatedAt: now.Add(-48 * time.Hour), NextRunAt: now}
	if err := db.Create(&target).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&orm.ScheduleDependency{ID: "review-dependency", UserID: "owner", SourceScheduleID: "review-schedule", TargetScheduleID: target.ID, Enabled: true}).Error; err != nil {
		t.Fatal(err)
	}
	received := make(chan string, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload struct {
			Query string `json:"query"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Error(err)
		}
		received <- payload.Query
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer server.Close()
	t.Setenv("LAZYMIND_CORE_SELF_URL", server.URL)
	req := httptest.NewRequest("POST", "/schedules/"+target.ID+":run-now", nil)
	req.Header.Set("X-User-Id", "owner")
	rec := httptest.NewRecorder()
	scheduler.RunNowHandler(rec, req)
	if rec.Code != 200 {
		t.Fatalf("dependency launch: %d %s", rec.Code, rec.Body.String())
	}
	var response struct {
		TaskID string `json:"task_id"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	select {
	case query := <-received:
		if !strings.Contains(query, "最终结果：完成联调。") || !strings.Contains(query, `task_id="review-task"`) {
			t.Fatalf("dependency lost authoritative output: %q", query)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("dependency did not consume recovered output")
	}
	deadline := time.NewTimer(10 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(5 * time.Millisecond)
	defer ticker.Stop()
	for {
		var run orm.TaskCenterTask
		if err := db.First(&run, "id = ?", response.TaskID).Error; err != nil {
			t.Fatal(err)
		}
		if run.Status == "failed" {
			break
		}
		select {
		case <-ticker.C:
		case <-deadline.C:
			t.Fatal("downstream transport did not finish")
		}
	}
	var inputs []orm.TaskRunInput
	if err := db.Where("downstream_task_id = ?", response.TaskID).Find(&inputs).Error; err != nil {
		t.Fatal(err)
	}
	if len(inputs) != 1 || inputs[0].UpstreamTaskID != "review-task" {
		t.Fatalf("invalid durable dependency inputs: %#v", inputs)
	}
}
