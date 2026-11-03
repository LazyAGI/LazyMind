package tasktiming

import (
	"context"
	"reflect"
	"testing"
	"time"

	"lazymind/core/common/orm"
	"lazymind/core/workflow/displaystate"
)

func TestWorkflowFailureFreezesInternalWaitingAndRetryExcludesIdleTime(t *testing.T) {
	for _, stepStatus := range []string{"interrupted", "failed", "succeeded"} {
		t.Run(stepStatus, func(t *testing.T) {
			db := orm.MigrateTestDB(t, &orm.TaskExecutionTiming{}, &orm.TaskAttemptTiming{}, &orm.WorkflowSession{}, &orm.WorkflowSessionStep{})
			ctx := t.Context()
			start := time.Date(2026, 10, 26, 0, 0, 0, 0, time.UTC)
			at := func(seconds int) time.Time { return start.Add(time.Duration(seconds) * time.Second) }
			must := func(err error) {
				t.Helper()
				if err != nil {
					t.Fatal(err)
				}
			}
			session := orm.WorkflowSession{ID: "workflow", ConversationID: "conv", CreateUserID: "owner", Status: "active"}
			must(db.Create(&session).Error)
			must(Begin(ctx, db.DB, "first", "history", "conv", "owner", at(0)))
			must(BindWorkflow(ctx, db.DB, session, at(0)))
			must(RecordAttempt(ctx, db.DB, "first", "attempt", at(2), ptr(at(10))))
			must(db.Create(&orm.WorkflowSessionStep{ID: "attempt", SessionID: session.ID, StepID: "step", Status: stepStatus, Validity: "effective", Attempt: 1}).Error)
			must(db.Model(&session).Update("status", "waiting").Error)
			session.Status = "waiting"
			view, err := displaystate.Read(db.DB, session)
			must(err)
			if view.Status != "failed" {
				t.Fatalf("display: %+v", view)
			}
			must(SyncWorkflow(ctx, db.DB, session.ID, at(10)))
			must(SyncWorkflow(ctx, db.DB, session.ID, at(20)))
			run, err := Snapshot(ctx, db.DB, "owner", "conv", session.ID, at(25))
			must(err)
			if run.Timing.FinishedAt == nil || *run.Timing.ElapsedMS != 10000 || *run.Timing.CumulativeExecutionMS != 8000 {
				t.Fatalf("failure did not freeze clock: %+v", run.Timing)
			}
			must(BeginChat(ctx, db.DB, "retry", "retry-history", "conv", "owner", at(30)))
			must(db.Model(&session).Update("status", "active").Error)
			must(SyncWorkflow(ctx, db.DB, session.ID, at(30)))
			run, err = Snapshot(ctx, db.DB, "owner", "conv", session.ID, at(35))
			must(err)
			if run.Timing.FinishedAt != nil || *run.Timing.ElapsedMS != 15000 || *run.Timing.CumulativeExecutionMS != 8000 {
				t.Fatalf("retry lost prior time or counted idle wait: %+v", run.Timing)
			}
		})
	}
}

func TestWorkflowPauseAndApprovalIntervalsExcludeWaitAndPreserveCumulativeTime(t *testing.T) {
	db := orm.MigrateTestDB(t, &orm.TaskExecutionTiming{}, &orm.TaskAttemptTiming{},
		&orm.WorkflowSession{}, &orm.WorkflowSessionStep{}, &orm.WorkflowReviewCheckpoint{})
	ctx := context.Background()
	start := time.Date(2026, 10, 25, 0, 0, 0, 0, time.UTC)
	at := func(seconds int) time.Time { return start.Add(time.Duration(seconds) * time.Second) }
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	session := orm.WorkflowSession{ID: "workflow", ConversationID: "conv", CreateUserID: "owner",
		TriggerHistoryID: "history", Status: "active", ControllerHost: "external-agent", ControlProtocol: "workflow.control.v1"}
	must(db.Create(&session).Error)
	must(Begin(ctx, db.DB, "first", "history", "conv", "owner", at(0)))
	must(BindWorkflow(ctx, db.DB, session, at(1)))
	step := orm.WorkflowSessionStep{ID: "a", SessionID: session.ID, StepID: "review", Status: "running", Validity: "effective", Attempt: 1, CreatedAt: at(5)}
	must(db.Create(&step).Error)
	must(RecordAttempt(ctx, db.DB, "first", "a", at(5), nil))
	setState := func(status string, seconds int) {
		t.Helper()
		must(db.Model(&session).Update("status", status).Error)
		must(SyncWorkflow(ctx, db.DB, session.ID, at(seconds)))
	}
	check := func(seconds int, elapsed, cumulative int64, running int, frozen bool) {
		t.Helper()
		run, err := Snapshot(ctx, db.DB, "owner", "conv", session.ID, at(seconds))
		must(err)
		if run == nil || *run.Timing.ElapsedMS != elapsed*1000 || *run.Timing.CumulativeExecutionMS != cumulative*1000 ||
			run.Timing.RunningAttemptCount != running || (run.Timing.FinishedAt != nil) != frozen {
			t.Fatalf("at %d: %+v", seconds, run)
		}
		listed, err := List(ctx, db.DB, "owner", "conv", at(seconds))
		must(err)
		for _, entry := range listed {
			if entry.RunID == session.ID {
				if !reflect.DeepEqual(entry, *run) {
					t.Fatalf("list and detail disagree: %+v / %+v", entry, run)
				}
				return
			}
		}
		t.Fatal("workflow absent from list")
	}
	// Requesting pause does not freeze execution before the worker has exited.
	setState("pausing", 10)
	check(15, 15, 10, 1, false)
	must(RecordAttempt(ctx, db.DB, "first", "a", at(5), ptr(at(20))))
	must(db.Model(&step).Updates(map[string]any{"status": "interrupted", "terminal_code": "WORKFLOW_STOPPED", "updated_at": at(20)}).Error)
	must(db.Model(&session).Update("last_stopped_at", at(10)).Error)
	setState("waiting", 20)
	check(100, 20, 15, 0, true)
	setState("waiting", 110) // A duplicate event cannot move the cutoff.
	check(120, 20, 15, 0, true)

	must(BeginChat(ctx, db.DB, "resumed", "h2", "conv", "owner", at(120)))
	setState("active", 121)
	must(RecordAttempt(ctx, db.DB, "resumed", "b", at(125), ptr(at(130))))
	second := orm.WorkflowSessionStep{ID: "b", SessionID: session.ID, StepID: "review", Status: "succeeded", Validity: "effective", Attempt: 2, CreatedAt: at(125)}
	must(db.Create(&second).Error)
	must(db.Create(&orm.WorkflowReviewCheckpoint{ID: "review", SessionID: session.ID, AttemptID: "b", StepID: "review", Status: "pending"}).Error)
	setState("waiting", 130)
	check(180, 30, 20, 0, true)
	// A normal Chat turn during approval neither resumes nor replaces the clock.
	must(BeginChat(ctx, db.DB, "chat", "h3", "conv", "owner", at(180)))
	must(FinishChat(ctx, db.DB, "chat", "completed", at(185)))
	check(200, 30, 20, 0, true)

	must(BeginChat(ctx, db.DB, "approved", "h4", "conv", "owner", at(220)))
	must(db.Model(&orm.WorkflowReviewCheckpoint{}).Where("id = ?", "review").Update("status", "accepted").Error)
	setState("active", 220)
	must(RecordAttempt(ctx, db.DB, "approved", "c", at(225), nil))
	check(230, 40, 25, 1, false)
	must(RecordAttempt(ctx, db.DB, "approved", "c", at(225), ptr(at(240))))
	setState("completed", 240)
	check(300, 50, 35, 0, true)
	must(RecordAttempt(ctx, db.DB, "first", "a", at(5), ptr(at(350))))
	check(400, 50, 35, 0, true)
	// A genuinely new execution after completion must not inherit old duration.
	setState("active", 500)
	check(505, 5, 0, 0, false)
}

func TestWorkflowTimingKeepsRunningWhileAFailedSiblingHasLiveWork(t *testing.T) {
	db := orm.MigrateTestDB(t, &orm.TaskExecutionTiming{}, &orm.TaskAttemptTiming{}, &orm.WorkflowSession{}, &orm.WorkflowSessionStep{})
	start := time.Now().UTC()
	session := orm.WorkflowSession{ID: "parallel", ConversationID: "conv", CreateUserID: "owner", Status: "active"}
	for _, err := range []error{db.Create(&session).Error,
		BindWorkflow(t.Context(), db.DB, session, start),
		db.Create(&orm.WorkflowSessionStep{ID: "running", SessionID: session.ID, StepID: "running", Status: "running", Validity: "effective"}).Error,
		db.Create(&orm.WorkflowSessionStep{ID: "failed", SessionID: session.ID, StepID: "failed", Status: "failed", Validity: "effective"}).Error,
		db.Model(&session).Update("status", "failed").Error,
		SyncWorkflow(t.Context(), db.DB, session.ID, start.Add(time.Second)),
	} {
		if err != nil {
			t.Fatal(err)
		}
	}
	run, err := Snapshot(t.Context(), db.DB, "owner", "conv", session.ID, start.Add(10*time.Second))
	if err != nil || run.Timing.FinishedAt != nil || *run.Timing.ElapsedMS != 10000 {
		t.Fatalf("live sibling clock stopped: %+v %v", run, err)
	}
}

func TestFinalApprovalCompletesFrozenTimingWithoutCountingReviewWait(t *testing.T) {
	db := orm.MigrateTestDB(t, &orm.TaskExecutionTiming{}, &orm.TaskAttemptTiming{})
	start := time.Date(2026, 10, 25, 0, 0, 0, 0, time.UTC)
	session := orm.WorkflowSession{ID: "final-review", ConversationID: "conv", CreateUserID: "owner", Status: "active"}
	if err := BindWorkflow(t.Context(), db.DB, session, start); err != nil {
		t.Fatal(err)
	}
	session.Status = "waiting_approval"
	if err := BindWorkflow(t.Context(), db.DB, session, start.Add(10*time.Second)); err != nil {
		t.Fatal(err)
	}
	session.Status = "completed"
	if err := BindWorkflow(t.Context(), db.DB, session, start.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	run, err := Snapshot(t.Context(), db.DB, "owner", "conv", session.ID, start.Add(2*time.Hour))
	if err != nil || run.Status != "completed" || *run.Timing.ElapsedMS != 10000 {
		t.Fatalf("final review changed execution time: %+v %v", run, err)
	}
	session.Status = "active"
	if err := BindWorkflow(t.Context(), db.DB, session, start.Add(2*time.Hour)); err != nil {
		t.Fatal(err)
	}
	run, err = Snapshot(t.Context(), db.DB, "owner", "conv", session.ID, start.Add(2*time.Hour+5*time.Second))
	if err != nil || *run.Timing.ElapsedMS != 5000 {
		t.Fatalf("rerun inherited review clock: %+v %v", run, err)
	}
}

func TestWorkflowRetryPreservesIntervalsBeforePauseAndFailure(t *testing.T) {
	db := orm.MigrateTestDB(t, &orm.TaskExecutionTiming{}, &orm.TaskAttemptTiming{},
		&orm.WorkflowSession{}, &orm.WorkflowSessionStep{})
	ctx := t.Context()
	start := time.Date(2026, 10, 25, 0, 0, 0, 0, time.UTC)
	at := func(seconds int) time.Time { return start.Add(time.Duration(seconds) * time.Second) }
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	session := orm.WorkflowSession{ID: "retry-workflow", ConversationID: "conv", CreateUserID: "owner",
		TriggerHistoryID: "initial-history", Status: "active"}
	must(db.Create(&session).Error)
	setState := func(status string, seconds int) {
		t.Helper()
		must(db.Model(&session).Update("status", status).Error)
		must(SyncWorkflow(ctx, db.DB, session.ID, at(seconds)))
	}
	check := func(seconds int, elapsed, cumulative int64, running int, frozen bool) string {
		t.Helper()
		run, err := Snapshot(ctx, db.DB, "owner", "conv", session.ID, at(seconds))
		must(err)
		if run == nil || run.Timing == nil {
			t.Fatal("missing workflow timing")
		}
		if *run.Timing.ElapsedMS != elapsed*1000 || *run.Timing.CumulativeExecutionMS != cumulative*1000 ||
			run.Timing.RunningAttemptCount != running || (run.Timing.FinishedAt != nil) != frozen {
			t.Fatalf("at %d: elapsed=%d cumulative=%d running=%d finished=%v; want %d/%d/%d/%v",
				seconds, *run.Timing.ElapsedMS, *run.Timing.CumulativeExecutionMS,
				run.Timing.RunningAttemptCount, run.Timing.FinishedAt, elapsed*1000, cumulative*1000, running, frozen)
		}
		listed, err := List(ctx, db.DB, "owner", "conv", at(seconds))
		must(err)
		for _, entry := range listed {
			if entry.RunID == session.ID && reflect.DeepEqual(entry, *run) {
				return run.ExecutionID
			}
		}
		t.Fatal("list and detail disagree")
		return ""
	}
	// Match the real run: pause, resume into a new Chat history, fail a step,
	// then automatically retry without a new Chat request.
	must(BeginChat(ctx, db.DB, "initial", session.TriggerHistoryID, "conv", "owner", at(0)))
	must(BindWorkflow(ctx, db.DB, session, at(1)))
	must(RecordAttempt(ctx, db.DB, "initial", "prepare", at(5), ptr(at(30))))
	setState("stopped", 33)
	check(100, 33, 25, 0, true)
	must(BeginChat(ctx, db.DB, "resumed", "resume-history", "conv", "owner", at(100)))
	setState("active", 100)
	must(RecordAttempt(ctx, db.DB, "resumed", "draft-failed", at(103), ptr(at(136))))
	setState("failed", 136)
	check(139, 69, 58, 0, true)
	setState("active", 139)
	retryID := check(139, 69, 58, 0, false)
	must(RecordAttempt(ctx, db.DB, retryID, "draft-retry", at(140), nil))
	check(150, 80, 68, 1, false)
	must(RecordAttempt(ctx, db.DB, retryID, "draft-retry", at(140), ptr(at(155))))
	setState("failed", 155)
	check(200, 85, 73, 0, true)
	// Multiple retries retain every failed attempt, not just the latest one.
	setState("active", 200)
	lastID := check(200, 85, 73, 0, false)
	must(RecordAttempt(ctx, db.DB, lastID, "draft-success", at(201), ptr(at(209))))
	setState("completed", 210)
	check(300, 95, 81, 0, true)
	// Reloads and duplicate events do not move the cutoff or double-count facts.
	setState("completed", 320)
	must(RecordAttempt(ctx, db.DB, lastID, "draft-success", at(201), ptr(at(209))))
	check(400, 95, 81, 0, true)
	// Completion, unlike a failed step, ends this execution's accumulation.
	setState("active", 500)
	check(505, 5, 0, 0, false)
}

func TestOrdinaryChatFailureDoesNotAccumulateIntoRetry(t *testing.T) {
	db := orm.MigrateTestDB(t, &orm.TaskExecutionTiming{}, &orm.TaskAttemptTiming{})
	start := time.Date(2026, 10, 25, 0, 0, 0, 0, time.UTC)
	ctx := t.Context()
	for _, err := range []error{
		BeginChat(ctx, db.DB, "failed", "history", "conv", "owner", start),
		RecordAttempt(ctx, db.DB, "failed", "old-attempt", start, ptr(start.Add(10*time.Second))),
		FinishChat(ctx, db.DB, "failed", "failed", start.Add(10*time.Second)),
		BeginChat(ctx, db.DB, "retry", "history", "conv", "owner", start.Add(20*time.Second)),
	} {
		if err != nil {
			t.Fatal(err)
		}
	}
	run, err := Snapshot(ctx, db.DB, "owner", "conv", "history", start.Add(25*time.Second))
	if err != nil || run == nil || run.ExecutionID != "retry" || *run.Timing.ElapsedMS != 5000 || *run.Timing.CumulativeExecutionMS != 0 {
		t.Fatalf("ordinary Chat inherited failed round: %+v %v", run, err)
	}
}
