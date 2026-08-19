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

// newRepoTestDB creates a SQLite test database.
func newRepoTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	return orm.OpenTestDB(t).DB
}

// TestIsUniqueConflict detects duplicate/unique constraint errors.
func TestIsUniqueConflict(t *testing.T) {
	// nil
	if isUniqueConflict(nil) {
		t.Fatal("nil should not be unique conflict")
	}
	// "duplicate"
	if !isUniqueConflict(errors.New("duplicate key error")) {
		t.Fatal("duplicate should be detected")
	}
	// "unique constraint"
	if !isUniqueConflict(errors.New("UNIQUE constraint failed")) {
		t.Fatal("unique constraint should be detected")
	}
	// "unique violation"
	if !isUniqueConflict(errors.New("unique violation")) {
		t.Fatal("unique violation should be detected")
	}
	// generic error
	if isUniqueConflict(errors.New("generic error")) {
		t.Fatal("generic error should not be unique conflict")
	}
}

// TestWithUpdateLockSQLite returns the DB without locking clause for SQLite.
func TestWithUpdateLockSQLite(t *testing.T) {
	db := newRepoTestDB(t)
	result := withUpdateLock(db)
	if result == nil {
		t.Fatal("should return non-nil db for SQLite")
	}
}

// TestWithClaimLockSQLite returns the DB without locking clause for SQLite.
func TestWithClaimLockSQLite(t *testing.T) {
	db := newRepoTestDB(t)
	result := withClaimLock(db)
	if result == nil {
		t.Fatal("should return non-nil db for SQLite")
	}
}

// seedStaleJob inserts a terminal async job row that still occupies the unique
// (job_type, idempotency_key) index.
func seedStaleJob(t *testing.T, db *gorm.DB, jobType, idempotencyKey, status string) *orm.AsyncJob {
	t.Helper()
	now := time.Now().UTC()
	row := &orm.AsyncJob{
		ID:               "job_stale_" + status,
		JobType:          jobType,
		Status:           status,
		IdempotencyKey:   idempotencyKey,
		PayloadJSON:      json.RawMessage(`{"old":true}`),
		ErrorCode:        "old_error",
		ErrorMessage:     "old error message",
		ErrorDetailsJSON: json.RawMessage(`{"old":true}`),
		ResultJSON:       json.RawMessage(`{"old":true}`),
		ProgressCurrent:  7,
		ProgressTotal:    9,
		AttemptCount:     2,
		MaxAttempts:      2,
		NextRunAt:        now,
		LockedBy:         "old-worker",
		LockUntil:        &now,
		StartedAt:        &now,
		FinishedAt:       &now,
		HeartbeatAt:      &now,
		CreateUserID:     "old-user",
		CreateUserName:   "Old User",
		CreatedAt:        now.Add(-time.Hour),
		UpdatedAt:        now,
	}
	if err := db.Create(row).Error; err != nil {
		t.Fatalf("seed stale job: %v", err)
	}
	return row
}

func TestEnqueueReusesFailedJob(t *testing.T) {
	db := orm.MigrateTestDB(t, &orm.AsyncJob{}).DB
	seed := seedStaleJob(t, db, "test.retry", "key-1", string(StatusFailed))

	job, err := Enqueue(context.Background(), db, EnqueueRequest{
		JobType:        "test.retry",
		IdempotencyKey: "key-1",
		ResourceType:   "resource",
		ResourceID:     "r-new",
		Payload:        map[string]string{"hello": "world"},
		MaxAttempts:    3,
		CreateUserID:   "u-new",
		CreateUserName: "User New",
	})
	if err != nil {
		t.Fatalf("enqueue after failed job: %v", err)
	}
	if job.ID != seed.ID {
		t.Fatalf("expected same job id %s, got %s", seed.ID, job.ID)
	}
	if job.Status != string(StatusPending) {
		t.Fatalf("status = %q, want pending", job.Status)
	}
	if job.AttemptCount != 0 || job.MaxAttempts != 3 {
		t.Fatalf("attempts = %d, max = %d, want 0/3", job.AttemptCount, job.MaxAttempts)
	}
	if job.ErrorCode != "" || job.ErrorMessage != "" || job.ErrorDetailsJSON != nil || job.ResultJSON != nil {
		t.Fatalf("terminal fields not cleared: %+v", job)
	}
	if job.StartedAt != nil || job.FinishedAt != nil || job.HeartbeatAt != nil || job.LockUntil != nil {
		t.Fatalf("execution fields not cleared: %+v", job)
	}
	if job.ProgressCurrent != 0 || job.ProgressTotal != 0 {
		t.Fatalf("progress not reset: %d/%d", job.ProgressCurrent, job.ProgressTotal)
	}
	if job.ResourceID != "r-new" || job.CreateUserID != "u-new" || job.LockedBy != "" {
		t.Fatalf("resource/user fields not refreshed: %+v", job)
	}
	if !job.CreatedAt.Equal(seed.CreatedAt.Truncate(time.Microsecond)) {
		t.Fatalf("created_at changed: %v -> %v", seed.CreatedAt.Truncate(time.Microsecond), job.CreatedAt)
	}
	var payload map[string]string
	if err := json.Unmarshal(job.PayloadJSON, &payload); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	if payload["hello"] != "world" {
		t.Fatalf("unexpected payload: %#v", payload)
	}
	var count int64
	if err := db.Model(&orm.AsyncJob{}).
		Where("job_type = ? AND idempotency_key = ?", "test.retry", "key-1").
		Count(&count).Error; err != nil {
		t.Fatalf("count jobs: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected 1 row for the key, got %d", count)
	}
}

func TestEnqueueReusesCanceledJob(t *testing.T) {
	db := orm.MigrateTestDB(t, &orm.AsyncJob{}).DB
	seed := seedStaleJob(t, db, "test.retry", "key-1", string(StatusCanceled))

	job, err := Enqueue(context.Background(), db, EnqueueRequest{
		JobType:        "test.retry",
		IdempotencyKey: "key-1",
		Payload:        map[string]string{"hello": "world"},
		CreateUserID:   "u-new",
	})
	if err != nil {
		t.Fatalf("enqueue after canceled job: %v", err)
	}
	if job.ID != seed.ID || job.Status != string(StatusPending) || job.AttemptCount != 0 {
		t.Fatalf("canceled job not reset: %+v", job)
	}
	if job.ErrorMessage != "" || job.FinishedAt != nil {
		t.Fatalf("terminal fields not cleared: %+v", job)
	}
}

func TestEnqueueCreatesNewJobWhenKeyFree(t *testing.T) {
	db := orm.MigrateTestDB(t, &orm.AsyncJob{}).DB
	seed := seedStaleJob(t, db, "test.retry", "key-other", string(StatusFailed))

	job, err := Enqueue(context.Background(), db, EnqueueRequest{
		JobType:        "test.retry",
		IdempotencyKey: "key-new",
		Payload:        map[string]string{"hello": "world"},
		CreateUserID:   "u-new",
	})
	if err != nil {
		t.Fatalf("enqueue with fresh key: %v", err)
	}
	if job.ID == seed.ID {
		t.Fatalf("expected a new job id, reused %s", seed.ID)
	}
	if job.Status != string(StatusPending) {
		t.Fatalf("status = %q, want pending", job.Status)
	}
}

// TestEnqueueReusesSucceededJobByDefault verifies the default behavior keeps
// reusing a previously succeeded job for the same idempotency key.
func TestEnqueueReusesSucceededJobByDefault(t *testing.T) {
	db := orm.MigrateTestDB(t, &orm.AsyncJob{}).DB
	seed := seedStaleJob(t, db, "test.batch", "key-1", string(StatusSucceeded))

	job, err := Enqueue(context.Background(), db, EnqueueRequest{
		JobType:        "test.batch",
		IdempotencyKey: "key-1",
		Payload:        map[string]string{"hello": "world"},
		CreateUserID:   "u-new",
	})
	if err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	if job.ID != seed.ID {
		t.Fatalf("expected reused job %s, got %s", seed.ID, job.ID)
	}
	var count int64
	if err := db.Model(&orm.AsyncJob{}).
		Where("job_type = ? AND idempotency_key = ?", "test.batch", "key-1").
		Count(&count).Error; err != nil {
		t.Fatalf("count jobs: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected 1 row for the key, got %d", count)
	}
}

// TestEnqueueSkipSucceededRetiresAndCreatesFreshJob verifies that
// SkipSucceeded retires the succeeded history row (its idempotency key is
// released so the row is kept) and creates a fresh pending job.
func TestEnqueueSkipSucceededRetiresAndCreatesFreshJob(t *testing.T) {
	db := orm.MigrateTestDB(t, &orm.AsyncJob{}).DB
	seed := seedStaleJob(t, db, "test.batch", "key-1", string(StatusSucceeded))

	job, err := Enqueue(context.Background(), db, EnqueueRequest{
		JobType:        "test.batch",
		IdempotencyKey: "key-1",
		Payload:        map[string]string{"hello": "world"},
		CreateUserID:   "u-new",
		SkipSucceeded:  true,
	})
	if err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	if job.ID == seed.ID {
		t.Fatalf("expected a fresh job, reused succeeded %s", seed.ID)
	}
	if job.Status != string(StatusPending) {
		t.Fatalf("status = %q, want pending", job.Status)
	}

	var retired orm.AsyncJob
	if err := db.First(&retired, "id = ?", seed.ID).Error; err != nil {
		t.Fatalf("load retired job: %v", err)
	}
	wantKey := "key-1:done:" + seed.ID
	if retired.IdempotencyKey != wantKey {
		t.Fatalf("retired idempotency_key = %q, want %q", retired.IdempotencyKey, wantKey)
	}
	if retired.Status != string(StatusSucceeded) {
		t.Fatalf("retired job status changed to %q", retired.Status)
	}

	var count int64
	if err := db.Model(&orm.AsyncJob{}).
		Where("job_type = ? AND idempotency_key = ?", "test.batch", "key-1").
		Count(&count).Error; err != nil {
		t.Fatalf("count jobs: %v", err)
	}
	if count != 1 {
		t.Fatalf("expected the fresh job to hold the key, got %d rows", count)
	}
}

// TestEnqueueSkipSucceededStillReusesActiveJob verifies that SkipSucceeded
// keeps deduplicating pending/running jobs so a concurrent duplicate request
// cannot start two batches.
func TestEnqueueSkipSucceededStillReusesActiveJob(t *testing.T) {
	db := orm.MigrateTestDB(t, &orm.AsyncJob{}).DB
	seed := seedStaleJob(t, db, "test.batch", "key-1", string(StatusPending))

	job, err := Enqueue(context.Background(), db, EnqueueRequest{
		JobType:        "test.batch",
		IdempotencyKey: "key-1",
		Payload:        map[string]string{"hello": "world"},
		CreateUserID:   "u-new",
		SkipSucceeded:  true,
	})
	if err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	if job.ID != seed.ID {
		t.Fatalf("expected the active job to be reused, got %s", job.ID)
	}
}
