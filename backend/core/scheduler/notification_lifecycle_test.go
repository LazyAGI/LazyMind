package scheduler

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"lazymind/core/common/orm"
	"lazymind/core/taskcenter"
)

func notificationLifecycleRun(t *testing.T) (*orm.DB, orm.TaskCenterTask) {
	t.Helper()
	db := orm.MigrateAllModelsForTest(t)
	schedule := orm.UserSchedule{ID: "notice-schedule", UserID: "owner", Name: "每日简报",
		CronExpr: "0 9 * * *", Timezone: "Asia/Shanghai", PromptTemplate: "整理简报", Enabled: true}
	if err := CreateSchedule(t.Context(), db.DB, &schedule); err != nil {
		t.Fatal(err)
	}
	run := orm.TaskCenterTask{ID: "notice-run", UserID: "owner", ConversationID: "notice-conversation",
		TaskType: "scheduled", Status: "running", ScheduleID: &schedule.ID, Title: &schedule.Name}
	if err := taskcenter.CreateTask(t.Context(), db.DB, &run); err != nil {
		t.Fatal(err)
	}
	return db, run
}

func TestNotificationRealSchedulerFinalizationPersistsOneResultEvent(t *testing.T) {
	db, run := notificationLifecycleRun(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/conversations:chat" || r.Header.Get("X-User-Id") != "owner" {
			t.Errorf("unexpected scheduler chat request: %s", r.URL.Path)
		}
		// Replace only the chat service boundary; scheduler, output and event storage are real.
		if err := db.Create(&orm.ChatHistory{ID: "notice-history", Seq: 1, ConversationID: run.ConversationID,
			Result: "<think>internal planning</think>今日完成接口设计，明日进行联调。"}).Error; err != nil {
			t.Error(err)
			w.WriteHeader(500)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
	}))
	defer server.Close()
	t.Setenv("LAZYMIND_CORE_SELF_URL", server.URL)
	sendScheduledChatRequest(run.UserID, run.ConversationID, run.ID, db.DB, map[string]any{"message": "简报"})
	// Duplicate finalization models callback replay after restart.
	finalizeTaskOutput(t.Context(), db.DB, run.ID, run.ConversationID)
	var output orm.TaskRunOutput
	if err := db.Where("task_id = ?", run.ID).First(&output).Error; err != nil || output.OutputStatus != "ready" {
		t.Fatalf("final output not queryable: status=%s err=%v", output.OutputStatus, err)
	}
	var notices []struct{ TaskID, Event, Channel, Title, Body string }
	if err := db.Table("task_notifications").Where("task_id = ?", run.ID).Find(&notices).Error; err != nil {
		t.Fatalf("durable notification missing from real scheduler path: %v", err)
	}
	if len(notices) != 1 || notices[0].Event != "succeeded" || notices[0].Channel != "desktop" {
		t.Fatalf("expected one completion event: %#v", notices)
	}
	if notices[0].Title != "每日简报" || notices[0].Body != output.FinalAnswerText || strings.Contains(notices[0].Body, "internal") {
		t.Fatalf("notification does not contain the safe actual result: %#v", notices[0])
	}
}

func TestNotificationOutputWriteFailureCannotProduceSuccess(t *testing.T) {
	db, run := notificationLifecycleRun(t)
	if err := db.Create(&orm.ChatHistory{ID: "notice-history", Seq: 1,
		ConversationID: run.ConversationID, Result: "尚未成功持久化的最终结果"}).Error; err != nil {
		t.Fatal(err)
	}
	// Fault injection is confined to this test's disposable database.
	if err := db.Migrator().DropTable(&orm.TaskRunOutput{}); err != nil {
		t.Fatal(err)
	}
	finalizeTaskOutput(t.Context(), db.DB, run.ID, run.ConversationID)
	var persisted orm.TaskCenterTask
	if err := db.First(&persisted, "id = ?", run.ID).Error; err != nil {
		t.Fatal(err)
	}
	if persisted.Status == "succeeded" {
		t.Fatal("task was marked successful although final output persistence failed")
	}
	var count int64
	if err := db.Table("task_notifications").Where("task_id = ? AND event = ?", run.ID, "succeeded").Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatal("sent a success notification for an unavailable result")
	}
}

func TestNotificationRealSchedulerFailureUsesSafeReason(t *testing.T) {
	db, run := notificationLifecycleRun(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(503)
		_, _ = w.Write([]byte("private upstream diagnostic"))
	}))
	defer server.Close()
	t.Setenv("LAZYMIND_CORE_SELF_URL", server.URL)
	sendScheduledChatRequest(run.UserID, run.ConversationID, run.ID, db.DB, map[string]any{})
	var notices []struct{ Event, Body string }
	if err := db.Table("task_notifications").Where("task_id = ?", run.ID).Find(&notices).Error; err != nil {
		t.Fatal(err)
	}
	if len(notices) != 1 || notices[0].Event != "failed" || notices[0].Body == "" || strings.Contains(notices[0].Body, "private") {
		t.Fatalf("expected safe failure notification: %#v", notices)
	}
}

func TestNotificationDependencyWaitingIsNotActionableWaiting(t *testing.T) {
	db, run := notificationLifecycleRun(t)
	if err := taskcenter.UpdateTaskStatus(t.Context(), db.DB, run.ID, "waiting_inputs"); err != nil {
		t.Fatal(err)
	}
	var count int64
	if err := db.Table("task_notifications").Where("task_id = ?", run.ID).Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatal("ordinary dependency waiting must not notify the user")
	}
}
