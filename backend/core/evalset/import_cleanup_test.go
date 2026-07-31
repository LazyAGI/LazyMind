package evalset

import (
	"context"
	"testing"
	"time"

	"gorm.io/gorm"

	"lazymind/core/asyncjob"
	"lazymind/core/common/orm"
)

// newImportCleanupTestDB creates a SQLite test database with AsyncJob table.
func newImportCleanupTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	return orm.MigrateTestDB(t, &orm.AsyncJob{}).DB
}

// seedImportJob inserts a test async job with the given status and finished time.
func seedCleanupImportJob(t *testing.T, db *gorm.DB, id, status string, finishedAt time.Time) {
	t.Helper()
	now := time.Now().UTC()
	if err := db.Create(&orm.AsyncJob{
		ID:         id,
		JobType:    importJobType,
		Status:     status,
		CreatedAt:  now,
		UpdatedAt:  now,
		FinishedAt: &finishedAt,
	}).Error; err != nil {
		t.Fatalf("seed job %s: %v", id, err)
	}
}

// TestCleanupTerminalImportJobs deletes old terminal jobs past retention.
func TestCleanupTerminalImportJobs(t *testing.T) {
	db := newImportCleanupTestDB(t)
	ctx := context.Background()
	now := time.Now().UTC()

	// Old succeeded job (past 30-day default retention)
	seedCleanupImportJob(t, db, "job-old-done", string(asyncjob.StatusSucceeded), now.Add(-31*24*time.Hour))
	// Recent succeeded job (within retention)
	seedCleanupImportJob(t, db, "job-recent-done", string(asyncjob.StatusSucceeded), now.Add(-1*time.Hour))
	// Old failed job
	seedCleanupImportJob(t, db, "job-old-failed", string(asyncjob.StatusFailed), now.Add(-31*24*time.Hour))
	// Pending job (never cleaned up)
	seedCleanupImportJob(t, db, "job-pending", string(asyncjob.StatusPending), now.Add(-60*24*time.Hour))

	// Run cleanup with default retention
	err := CleanupTerminalImportJobs(ctx, db, now, defaultImportTaskRetention)
	if err != nil {
		t.Fatalf("cleanup: %v", err)
	}

	// Verify old terminal jobs are deleted
	var count int64
	db.Model(&orm.AsyncJob{}).Where("id = ?", "job-old-done").Count(&count)
	if count != 0 {
		t.Fatal("old done job should be deleted")
	}
	db.Model(&orm.AsyncJob{}).Where("id = ?", "job-old-failed").Count(&count)
	if count != 0 {
		t.Fatal("old failed job should be deleted")
	}

	// Verify recent job still exists
	db.Model(&orm.AsyncJob{}).Where("id = ?", "job-recent-done").Count(&count)
	if count != 1 {
		t.Fatal("recent done job should remain")
	}

	// Verify pending job still exists (not terminal)
	db.Model(&orm.AsyncJob{}).Where("id = ?", "job-pending").Count(&count)
	if count != 1 {
		t.Fatal("pending job should remain")
	}
}

// TestCleanupTerminalImportJobsZeroRetention uses default when retention <= 0.
func TestCleanupTerminalImportJobsZeroRetention(t *testing.T) {
	db := newImportCleanupTestDB(t)
	ctx := context.Background()
	now := time.Now().UTC()

	// Very old job
	seedCleanupImportJob(t, db, "job-zero-ret", string(asyncjob.StatusSucceeded), now.Add(-60*24*time.Hour))

	// Zero retention → uses defaultImportTaskRetention
	err := CleanupTerminalImportJobs(ctx, db, now, 0)
	if err != nil {
		t.Fatalf("cleanup with zero retention: %v", err)
	}

	var count int64
	db.Model(&orm.AsyncJob{}).Where("id = ?", "job-zero-ret").Count(&count)
	if count != 0 {
		t.Fatal("60-day old job should be deleted under 30-day default retention")
	}
}

// TestCleanupTerminalImportJobsNotImportType ignores non-import jobs.
func TestCleanupTerminalImportJobsNotImportType(t *testing.T) {
	db := newImportCleanupTestDB(t)
	ctx := context.Background()
	now := time.Now().UTC()

	// Non-import old job
	if err := db.Create(&orm.AsyncJob{
		ID:         "job-other-type",
		JobType:    "other_job",
		Status:     string(asyncjob.StatusSucceeded),
		CreatedAt:  now,
		UpdatedAt:  now,
		FinishedAt: &now,
	}).Error; err != nil {
		t.Fatalf("seed other job: %v", err)
	}

	err := CleanupTerminalImportJobs(ctx, db, now.Add(time.Hour), defaultImportTaskRetention)
	if err != nil {
		t.Fatalf("cleanup: %v", err)
	}

	// Non-import job should remain
	var count int64
	db.Model(&orm.AsyncJob{}).Where("id = ?", "job-other-type").Count(&count)
	if count != 1 {
		t.Fatal("non-import job should remain")
	}
}
