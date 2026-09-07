package asyncjob

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"gorm.io/gorm"

	"lazymind/core/common/orm"
)

func TestEnqueueCreatesPendingJob(t *testing.T) {
	db := newTestDB(t)

	job, err := Enqueue(context.Background(), db, EnqueueRequest{
		JobType:        "test.create",
		ResourceType:   "resource",
		ResourceID:     "r1",
		Payload:        map[string]string{"hello": "world"},
		CreateUserID:   "u1",
		CreateUserName: "User One",
	})
	if err != nil {
		t.Fatalf("enqueue: %v", err)
	}

	if job.ID == "" || job.Status != string(StatusPending) || job.JobType != "test.create" {
		t.Fatalf("unexpected job: %+v", job)
	}
	if job.MaxAttempts != 1 || job.NextRunAt.IsZero() {
		t.Fatalf("expected default attempts and next_run_at, got attempts=%d next=%v", job.MaxAttempts, job.NextRunAt)
	}

	var payload map[string]string
	if err := json.Unmarshal(job.PayloadJSON, &payload); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	if payload["hello"] != "world" {
		t.Fatalf("unexpected payload: %#v", payload)
	}
}

func TestRunnerExecutesRegisteredHandler(t *testing.T) {
	db := newTestDB(t)
	resetRegistryForTest()
	defer resetRegistryForTest()

	Register("test.success", func(ctx context.Context, job Job, reporter Reporter) (Result, error) {
		if job.AttemptCount != 1 {
			t.Fatalf("expected attempt 1, got %d", job.AttemptCount)
		}
		if err := reporter.SetProgress(ctx, 2, 4); err != nil {
			t.Fatalf("set progress: %v", err)
		}
		if err := reporter.Heartbeat(ctx); err != nil {
			t.Fatalf("heartbeat: %v", err)
		}
		return Result{ResultJSON: json.RawMessage(`{"ok":true}`)}, nil
	})

	job := enqueueTestJob(t, db, "test.success", 1)
	runner := newTestRunner(db)
	ran, err := runner.runOnce(context.Background())
	if err != nil {
		t.Fatalf("run once: %v", err)
	}
	if !ran {
		t.Fatalf("expected runner to process one job")
	}

	got := getTestJob(t, db, job.ID)
	if got.Status != string(StatusSucceeded) {
		t.Fatalf("expected succeeded, got %+v", got)
	}
	if string(got.ResultJSON) != `{"ok":true}` {
		t.Fatalf("unexpected result json: %s", string(got.ResultJSON))
	}
	if got.ProgressCurrent != got.ProgressTotal || got.ProgressTotal != 4 {
		t.Fatalf("expected completed progress to equal total 4, got current=%d total=%d", got.ProgressCurrent, got.ProgressTotal)
	}
	if got.LockedBy != "" || got.LockUntil != nil || got.FinishedAt == nil {
		t.Fatalf("expected lock cleared and finished_at set, got %+v", got)
	}
}

func TestRunnerMarksFailedWhenHandlerErrorsWithoutRetry(t *testing.T) {
	db := newTestDB(t)
	resetRegistryForTest()
	defer resetRegistryForTest()

	Register("test.fail", func(ctx context.Context, job Job, reporter Reporter) (Result, error) {
		return Result{ErrorCode: "boom"}, errors.New("handler exploded")
	})

	job := enqueueTestJob(t, db, "test.fail", 1)
	runner := newTestRunner(db)
	if _, err := runner.runOnce(context.Background()); err != nil {
		t.Fatalf("run once: %v", err)
	}

	got := getTestJob(t, db, job.ID)
	if got.Status != string(StatusFailed) || got.ErrorCode != "boom" || got.ErrorMessage != "handler exploded" {
		t.Fatalf("expected failed job with handler error, got %+v", got)
	}
	if got.AttemptCount != 1 || got.FinishedAt == nil {
		t.Fatalf("expected one finished attempt, got attempts=%d finished=%v", got.AttemptCount, got.FinishedAt)
	}
}

func TestRunnerRetriesFailedHandlerWhenAttemptsRemain(t *testing.T) {
	db := newTestDB(t)
	resetRegistryForTest()
	defer resetRegistryForTest()

	Register("test.retry", func(ctx context.Context, job Job, reporter Reporter) (Result, error) {
		return Result{}, errors.New("try again")
	})

	job := enqueueTestJob(t, db, "test.retry", 2)
	runner := newTestRunner(db)
	before := time.Now().UTC()
	if _, err := runner.runOnce(context.Background()); err != nil {
		t.Fatalf("run once: %v", err)
	}

	got := getTestJob(t, db, job.ID)
	if got.Status != string(StatusPending) {
		t.Fatalf("expected pending retry, got %+v", got)
	}
	if got.AttemptCount != 1 || got.FinishedAt != nil || got.LockedBy != "" || got.LockUntil != nil {
		t.Fatalf("expected unlocked pending first attempt, got %+v", got)
	}
	if got.NextRunAt.Before(before.Add(9 * time.Second)) {
		t.Fatalf("expected retry backoff near 10s, got next_run_at=%v before=%v", got.NextRunAt, before)
	}
}

func TestRecoverStaleJobsRestoresPendingAndFailsExhausted(t *testing.T) {
	db := newTestDB(t)
	now := time.Now().UTC()
	expired := now.Add(-time.Minute)

	retryJob := orm.AsyncJob{
		ID:           "job_retry",
		JobType:      "test",
		Status:       string(StatusRunning),
		AttemptCount: 1,
		MaxAttempts:  2,
		NextRunAt:    now.Add(-time.Hour),
		LockedBy:     "old-worker",
		LockUntil:    &expired,
		CreatedAt:    now.Add(-time.Hour),
		UpdatedAt:    now.Add(-time.Hour),
	}
	exhaustedJob := orm.AsyncJob{
		ID:           "job_exhausted",
		JobType:      "test",
		Status:       string(StatusRunning),
		AttemptCount: 1,
		MaxAttempts:  1,
		NextRunAt:    now.Add(-time.Hour),
		LockedBy:     "old-worker",
		LockUntil:    &expired,
		CreatedAt:    now.Add(-time.Hour),
		UpdatedAt:    now.Add(-time.Hour),
	}
	if err := db.Create(&retryJob).Error; err != nil {
		t.Fatalf("create retry job: %v", err)
	}
	if err := db.Create(&exhaustedJob).Error; err != nil {
		t.Fatalf("create exhausted job: %v", err)
	}

	if err := RecoverStaleJobs(context.Background(), db, now); err != nil {
		t.Fatalf("recover stale jobs: %v", err)
	}

	retry := getTestJob(t, db, retryJob.ID)
	if retry.Status != string(StatusPending) || retry.LockedBy != "" || retry.LockUntil != nil {
		t.Fatalf("expected stale retry job to become pending and unlocked, got %+v", retry)
	}
	want := now.Truncate(time.Microsecond)
	if !retry.NextRunAt.Truncate(time.Microsecond).Equal(want) {
		t.Fatalf("expected retry next_run_at to be now, got %v want %v", retry.NextRunAt, want)
	}

	exhausted := getTestJob(t, db, exhaustedJob.ID)
	if exhausted.Status != string(StatusFailed) || exhausted.ErrorCode != ErrorCodeLockExpired || exhausted.FinishedAt == nil {
		t.Fatalf("expected exhausted stale job to fail, got %+v", exhausted)
	}
}

func TestRunnerMarksUnregisteredHandlerFailed(t *testing.T) {
	db := newTestDB(t)
	resetRegistryForTest()
	defer resetRegistryForTest()

	job := enqueueTestJob(t, db, "test.missing", 1)
	runner := newTestRunner(db)
	if _, err := runner.runOnce(context.Background()); err != nil {
		t.Fatalf("run once: %v", err)
	}

	got := getTestJob(t, db, job.ID)
	if got.Status != string(StatusFailed) || got.ErrorCode != ErrorCodeHandlerNotFound {
		t.Fatalf("expected missing handler failure, got %+v", got)
	}
}

func TestEnqueueIdempotencyKeyReturnsExistingJob(t *testing.T) {
	db := newTestDB(t)

	first, err := Enqueue(context.Background(), db, EnqueueRequest{
		JobType:        "test.idempotent",
		IdempotencyKey: "same",
		Payload:        map[string]string{"value": "first"},
	})
	if err != nil {
		t.Fatalf("first enqueue: %v", err)
	}
	second, err := Enqueue(context.Background(), db, EnqueueRequest{
		JobType:        "test.idempotent",
		IdempotencyKey: "same",
		Payload:        map[string]string{"value": "second"},
	})
	if err != nil {
		t.Fatalf("second enqueue: %v", err)
	}

	if first.ID != second.ID {
		t.Fatalf("expected same job for duplicate idempotency key, got %s and %s", first.ID, second.ID)
	}

	var count int64
	if err := db.Model(&orm.AsyncJob{}).Where("job_type = ?", "test.idempotent").Count(&count).Error; err != nil {
		t.Fatalf("count jobs: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected one job, got %d", count)
	}
}

func newTestDB(t *testing.T) *gorm.DB {
	t.Helper()

	return orm.MigrateTestDB(t, &orm.AsyncJob{}).DB
}

func newTestRunner(db *gorm.DB) *Runner {
	return newRunner(db, Options{
		WorkerID:     "test-worker",
		Concurrency:  1,
		PollInterval: time.Hour,
		LockTTL:      time.Minute,
	})
}

func enqueueTestJob(t *testing.T, db *gorm.DB, jobType string, maxAttempts int) *orm.AsyncJob {
	t.Helper()

	job, err := Enqueue(context.Background(), db, EnqueueRequest{
		JobType:     jobType,
		Payload:     map[string]string{"job": jobType},
		MaxAttempts: maxAttempts,
	})
	if err != nil {
		t.Fatalf("enqueue test job: %v", err)
	}
	return job
}

func getTestJob(t *testing.T, db *gorm.DB, id string) *orm.AsyncJob {
	t.Helper()

	job, err := Get(context.Background(), db, id)
	if err != nil {
		t.Fatalf("get job %s: %v", id, err)
	}
	return job
}

func TestRunnerTypePriorityAndResourceSerialization(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	history := enqueueTestJob(t, db, "opening.backfill", 3)
	live := enqueueTestJob(t, db, "opening.live", 3)
	background := newRunner(db, Options{JobTypes: []string{"opening.backfill"}, YieldToJobTypes: []string{"opening.live"}, SerializeResources: true})
	if row, err := background.claimOne(ctx, time.Now()); err != nil || row != nil {
		t.Fatal("backfill did not yield", err)
	}
	foreground := newRunner(db, Options{JobTypes: []string{"opening.live"}, SerializeResources: true})
	row, err := foreground.claimOne(ctx, time.Now())
	if err != nil || row == nil || row.ID != live.ID {
		t.Fatal("live claim", err)
	}
	// Both jobs use the same resource in enqueueTestJob.
	if row, err := background.claimOne(ctx, time.Now()); err != nil || row != nil {
		t.Fatal("same resource ran concurrently", err)
	}
	if err := foreground.markSucceeded(ctx, *row, Result{}); err != nil {
		t.Fatal(err)
	}
	if row, err := background.claimOne(ctx, time.Now()); err != nil || row == nil || row.ID != history.ID {
		t.Fatal("backfill did not resume", err)
	}
}
func TestRunnerPermanentFailureAndStaleOwner(t *testing.T) {
	db := newTestDB(t)
	ctx := context.Background()
	runner := newTestRunner(db)
	job := enqueueTestJob(t, db, "opening.test", 3)
	row, err := runner.claimOne(ctx, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if err := runner.markFailedAttempt(ctx, *row, Result{Permanent: true}, errors.New("invalid config")); err != nil {
		t.Fatal(err)
	}
	if got := getTestJob(t, db, job.ID); got.Status != "failed" || got.AttemptCount != 1 {
		t.Fatal("deterministic failure retried")
	}
	db.Model(&orm.AsyncJob{}).Where("id = ?", job.ID).Updates(map[string]any{"status": "running", "attempt_count": 2, "locked_by": "new-owner"})
	if err := runner.markSucceeded(ctx, *row, Result{}); err != nil {
		t.Fatal(err)
	}
	if got := getTestJob(t, db, job.ID); got.Status != "running" || got.LockedBy != "new-owner" {
		t.Fatal("stale owner finalized newer attempt")
	}
}

func TestRunnerRecoversLeaseThatExpiresAfterStartup(t *testing.T) {
	db := newTestDB(t)
	resetRegistryForTest()
	defer resetRegistryForTest()
	done := make(chan struct{}, 1)
	Register("test.recover-later", func(context.Context, Job, Reporter) (Result, error) {
		done <- struct{}{}
		return Result{}, nil
	})
	job := enqueueTestJob(t, db, "test.recover-later", 3)
	until := time.Now().Add(200 * time.Millisecond)
	if err := db.Model(&orm.AsyncJob{}).Where("id = ?", job.ID).Updates(map[string]any{"status": StatusRunning, "lock_until": until, "locked_by": "previous-process", "attempt_count": 1}).Error; err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	runner := Start(ctx, db, Options{Concurrency: 1, PollInterval: 20 * time.Millisecond, LockTTL: 100 * time.Millisecond})
	defer func() { cancel(); <-runner.Done() }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("unexpired startup lease was never recovered")
	}
}
