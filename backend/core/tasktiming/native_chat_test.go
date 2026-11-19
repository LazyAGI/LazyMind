package tasktiming

import (
	"testing"
	"time"

	"lazymind/core/common/orm"
)

func TestNativeChatCompletionFreezesWorkflowAndResumeExcludesWait(t *testing.T) {
	db := orm.MigrateTestDB(t, &orm.TaskExecutionTiming{}, &orm.TaskAttemptTiming{}, &orm.ChatHistory{}, &orm.WorkflowSession{}, &orm.WorkflowSessionStep{})
	ctx := t.Context()
	start := time.Date(2026, 11, 18, 0, 0, 0, 0, time.UTC)
	at := func(s int) time.Time { return start.Add(time.Duration(s) * time.Second) }
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	session := orm.WorkflowSession{ID: "workflow", ConversationID: "conv", CreateUserID: "owner", TriggerHistoryID: "h1", ControllerHost: "lazymind", Status: "active"}
	must(db.Create(&session).Error)
	must(db.Create(&orm.ChatHistory{ID: "h1", ConversationID: "conv", RunID: "run1", RunStatus: "generating"}).Error)
	must(BeginChat(ctx, db.DB, "run1", "h1", "conv", "owner", at(0)))
	must(BindWorkflow(ctx, db.DB, session, at(1)))
	check := func(now int, elapsed, cumulative int64, frozen bool) {
		t.Helper()
		run, err := Snapshot(ctx, db.DB, "owner", "conv", session.ID, at(now))
		must(err)
		if run == nil || run.Timing == nil {
			t.Fatalf("at %d: missing workflow timing", now)
		}
		if *run.Timing.ElapsedMS != elapsed*1000 || *run.Timing.CumulativeExecutionMS != cumulative*1000 || (run.Timing.FinishedAt != nil) != frozen {
			t.Fatalf("at %d: run=%+v timing=%+v", now, run, run.Timing)
		}
	}
	check(5, 5, 0, false)
	must(db.Model(&orm.ChatHistory{}).Where("id=?", "h1").Update("run_status", "completed").Error)
	must(FinishChat(ctx, db.DB, "run1", "completed", at(10)))
	check(70, 10, 0, true)
	must(FinishChat(ctx, db.DB, "run1", "completed", at(80)))
	check(90, 10, 0, true)
	// A later Chat resumes this Workflow; its accepted request starts the next interval.
	must(db.Create(&orm.ChatHistory{ID: "h2", ConversationID: "conv", RunID: "run2", RunStatus: "generating"}).Error)
	must(BeginChat(ctx, db.DB, "run2", "h2", "conv", "owner", at(100)))
	must(db.Model(&session).Update("control_binding_json", `{"chat_history_id":"h2"}`).Error)
	must(SyncWorkflow(ctx, db.DB, session.ID, at(101)))
	must(RecordAttempt(ctx, db.DB, "run2", "child", at(102), nil))
	child := orm.WorkflowSessionStep{ID: "child", SessionID: session.ID, StepID: "first", Status: "running", Validity: "effective"}
	must(db.Create(&child).Error)
	check(105, 15, 3, false)
	// A delayed terminal notification from the previous owner cannot stop the new interval.
	must(FinishChat(ctx, db.DB, "run1", "completed", at(106)))
	check(107, 17, 5, false)
	// Finishing Chat while a child is live must keep counting.
	must(db.Model(&orm.ChatHistory{}).Where("id=?", "h2").Update("run_status", "completed").Error)
	must(FinishChat(ctx, db.DB, "run2", "completed", at(108)))
	check(109, 19, 7, false)
	must(RecordAttempt(ctx, db.DB, "run2", "child", at(102), ptr(at(110))))
	must(db.Model(&child).Update("status", "succeeded").Error)
	must(SyncWorkflow(ctx, db.DB, session.ID, at(110)))
	check(200, 20, 8, true)
	// A continuation admitted while a Workflow was active has no separate timing row.
	must(db.Model(&orm.ChatHistory{}).Where("id=?", "h2").Updates(map[string]any{"run_id": "run3", "run_status": "generating"}).Error)
	must(SyncWorkflow(ctx, db.DB, session.ID, at(210)))
	must(db.Model(&orm.ChatHistory{}).Where("id=?", "h2").Update("run_status", "completed").Error)
	must(FinishChat(ctx, db.DB, "run3", "completed", at(215)))
	check(300, 25, 8, true)
	// A stale run ID on a reused history is ignored.
	must(db.Model(&orm.ChatHistory{}).Where("id=?", "h2").Updates(map[string]any{"run_id": "run4", "run_status": "generating"}).Error)
	must(SyncWorkflow(ctx, db.DB, session.ID, at(310)))
	must(FinishChat(ctx, db.DB, "run3", "completed", at(312)))
	check(315, 30, 8, false)
}
