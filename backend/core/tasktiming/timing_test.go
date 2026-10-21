package tasktiming

import (
	"context"
	"testing"
	"time"

	"lazymind/core/common/orm"
)

func TestRunIncludesInitializationWaitRetriesAndFinalCutoff(t *testing.T) {
	db := orm.MigrateTestDB(t, &orm.TaskExecutionTiming{}, &orm.TaskAttemptTiming{})
	ctx := context.Background()
	start := time.Date(2026, 10, 16, 0, 0, 0, 0, time.UTC)
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(Begin(ctx, db.DB, "execution", "history", "conv", "owner", start))
	run, err := Snapshot(ctx, db.DB, "owner", "conv", "history", start.Add(10*time.Second))
	must(err)
	if run == nil || *run.Timing.ElapsedMS != 10000 || *run.Timing.CumulativeExecutionMS != 0 {
		t.Fatalf("initialization: %+v", run)
	}
	must(RecordAttempt(ctx, db.DB, "execution", "A", start.Add(10*time.Second), ptr(start.Add(30*time.Second))))
	// Waiting has no running child; total continues while cumulative stays fixed.
	must(SetStatus(ctx, db.DB, "execution", "waiting", start.Add(30*time.Second)))
	run, err = Snapshot(ctx, db.DB, "owner", "conv", "history", start.Add(60*time.Second))
	must(err)
	if *run.Timing.ElapsedMS != 60000 || *run.Timing.CumulativeExecutionMS != 20000 || run.Timing.RunningAttemptCount != 0 {
		t.Fatalf("wait: %+v", run.Timing)
	}
	must(RecordAttempt(ctx, db.DB, "execution", "B-failed", start.Add(60*time.Second), ptr(start.Add(70*time.Second))))
	must(RecordAttempt(ctx, db.DB, "execution", "B-retry", start.Add(70*time.Second), nil))
	must(SetStatus(ctx, db.DB, "execution", "running", start.Add(70*time.Second)))
	must(SetStatus(ctx, db.DB, "execution", "completed", start.Add(90*time.Second)))
	// Duplicate/late completion must not extend either final value.
	must(SetStatus(ctx, db.DB, "execution", "completed", start.Add(100*time.Second)))
	must(RecordAttempt(ctx, db.DB, "execution", "B-retry", start.Add(70*time.Second), ptr(start.Add(110*time.Second))))
	run, err = Snapshot(ctx, db.DB, "owner", "conv", "history", start.Add(120*time.Second))
	must(err)
	if *run.Timing.ElapsedMS != 90000 || *run.Timing.CumulativeExecutionMS != 50000 || run.Timing.RunningAttemptCount != 0 {
		t.Fatalf("final: %+v", run.Timing)
	}
	foreign, err := Snapshot(ctx, db.DB, "other", "conv", "history", start)
	must(err)
	if foreign != nil {
		t.Fatal("foreign owner saw timing")
	}
	unknown, err := Snapshot(ctx, db.DB, "owner", "conv", "legacy", start)
	must(err)
	if unknown != nil {
		t.Fatal("fabricated legacy timing")
	}
}

func TestParallelAttemptsAndNewRoundRemainIndependent(t *testing.T) {
	db := orm.MigrateTestDB(t, &orm.TaskExecutionTiming{}, &orm.TaskAttemptTiming{})
	ctx := context.Background()
	start := time.Date(2026, 10, 16, 0, 0, 0, 123456000, time.UTC)
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(Begin(ctx, db.DB, "first", "run", "conv", "owner", start))
	must(RecordAttempt(ctx, db.DB, "first", "A", start, nil))
	must(RecordAttempt(ctx, db.DB, "first", "A", start, nil))
	must(RecordAttempt(ctx, db.DB, "first", "B", start, nil))
	run, err := Snapshot(ctx, db.DB, "owner", "conv", "run", start.Add(10*time.Second))
	must(err)
	if *run.Timing.CumulativeExecutionMS != 20000 || run.Timing.RunningAttemptCount != 2 {
		t.Fatalf("parallel/dedup: %+v", run.Timing)
	}
	must(SetStatus(ctx, db.DB, "first", "canceled", start.Add(10*time.Second)))
	must(Begin(ctx, db.DB, "second", "run", "conv", "owner", start.Add(20*time.Second)))
	run, err = Snapshot(ctx, db.DB, "owner", "conv", "run", start.Add(25*time.Second))
	must(err)
	if run.ExecutionID != "second" || *run.Timing.ElapsedMS != 5000 || *run.Timing.CumulativeExecutionMS != 0 {
		t.Fatalf("new round: %+v", run)
	}
	var old orm.TaskExecutionTiming
	if err := db.First(&old, "id = ?", "first").Error; err != nil || old.FinishedAt == nil || !old.FinishedAt.Equal(start.Add(10*time.Second)) {
		t.Fatalf("old round lost: %+v %v", old, err)
	}
}
func ptr(t time.Time) *time.Time { return &t }

func TestWorkflowBindingPreservesAcceptedTimeAndStartsNewRound(t *testing.T) {
	db := orm.MigrateTestDB(t, &orm.TaskExecutionTiming{}, &orm.TaskAttemptTiming{}, &orm.WorkflowSession{})
	ctx := context.Background()
	start := time.Now().UTC().Add(-time.Minute)
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	must(Begin(ctx, db.DB, "accepted", "history", "conv", "owner", start))
	session := orm.WorkflowSession{ID: "workflow", ConversationID: "conv", TriggerHistoryID: "history", CreateUserID: "owner", Status: "active"}
	must(db.Create(&session).Error)
	must(BindWorkflow(ctx, db.DB, session, start.Add(10*time.Second)))
	run, err := Snapshot(ctx, db.DB, "owner", "conv", "workflow", start.Add(20*time.Second))
	must(err)
	if run.ExecutionID != "accepted" || *run.Timing.ElapsedMS != 20000 {
		t.Fatalf("lost initialization: %+v", run)
	}
	session.Status = "waiting"
	must(db.Model(&session).Update("status", "waiting").Error)
	must(SyncWorkflow(ctx, db.DB, session.ID, start.Add(20*time.Second)))
	must(FinishChat(ctx, db.DB, "accepted", "completed", start.Add(20*time.Second)))
	run, err = Snapshot(ctx, db.DB, "owner", "conv", "workflow", start.Add(30*time.Second))
	must(err)
	if run.Timing.FinishedAt != nil || *run.Timing.ElapsedMS != 30000 {
		t.Fatal("chat stream completion ended waiting workflow")
	}
	session.Status = "completed"
	must(db.Model(&session).Update("status", "completed").Error)
	must(SyncWorkflow(ctx, db.DB, session.ID, start.Add(40*time.Second)))
	session.Status = "active"
	must(db.Model(&session).Update("status", "active").Error)
	must(SyncWorkflow(ctx, db.DB, session.ID, start.Add(50*time.Second)))
	run, err = Snapshot(ctx, db.DB, "owner", "conv", "workflow", start.Add(55*time.Second))
	must(err)
	if run.ExecutionID == "accepted" || *run.Timing.ElapsedMS != 5000 {
		t.Fatal("rerun reused old timing")
	}
}

func TestPermanentDeletionPurgesOnlyOwnedTiming(t *testing.T) {
	db := orm.MigrateTestDB(t, &orm.TaskExecutionTiming{}, &orm.TaskAttemptTiming{})
	ctx := context.Background()
	now := time.Now().UTC()
	for _, owner := range []string{"owner", "other"} {
		if err := Begin(ctx, db.DB, owner, "run", "conv", owner, now); err != nil {
			t.Fatal(err)
		}
		if err := RecordAttempt(ctx, db.DB, owner, owner+"-attempt", now, nil); err != nil {
			t.Fatal(err)
		}
	}
	if err := PurgeConversations(ctx, db.DB, "owner", []string{"conv"}); err != nil {
		t.Fatal(err)
	}
	var roots, attempts int64
	db.Model(&orm.TaskExecutionTiming{}).Count(&roots)
	db.Model(&orm.TaskAttemptTiming{}).Count(&attempts)
	if roots != 1 || attempts != 1 {
		t.Fatalf("wrong purge scope: %d roots %d attempts", roots, attempts)
	}
	run, err := Snapshot(ctx, db.DB, "other", "conv", "run", now)
	if err != nil || run == nil {
		t.Fatal("foreign run removed")
	}
}
