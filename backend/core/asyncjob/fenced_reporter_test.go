package asyncjob

import (
	"context"
	"errors"
	"gorm.io/gorm"
	"lazymind/core/common/orm"
	"testing"
	"time"
)

func TestFencedReporterRejectsStaleAttemptExpiryAndCancel(t *testing.T) {
	for _, change := range []string{"attempt", "worker", "expired", "canceled"} {
		t.Run(change, func(t *testing.T) {
			db := newTestDB(t)
			job := enqueueTestJob(t, db, "fence", 3)
			runner := newTestRunner(db)
			row, err := runner.claimOne(context.Background(), time.Now())
			if err != nil || row == nil {
				t.Fatalf("claim: %v", err)
			}
			reporter := &jobReporter{db: db, jobID: job.ID, workerID: row.LockedBy, attemptCount: row.AttemptCount, requireLiveLease: true}
			called := false
			if err := reporter.WithLease(context.Background(), func(tx *gorm.DB) error { called = true; return nil }); err != nil || !called {
				t.Fatalf("live lease: %v", err)
			}
			values := map[string]any{}
			switch change {
			case "attempt":
				values["attempt_count"] = row.AttemptCount + 1
			case "worker":
				values["locked_by"] = "new"
			case "expired":
				values["lock_until"] = time.Now().Add(-time.Minute)
			case "canceled":
				values["status"] = StatusCanceled
			}
			if err := db.Model(&orm.AsyncJob{}).Where("id = ?", job.ID).Updates(values).Error; err != nil {
				t.Fatal(err)
			}
			if change == "expired" {
				if err := reporter.Heartbeat(context.Background()); !errors.Is(err, ErrLeaseLost) {
					t.Fatalf("expired lease resurrected: %v", err)
				}
			}
			called = false
			err = reporter.WithLease(context.Background(), func(tx *gorm.DB) error { called = true; return nil })
			if !errors.Is(err, ErrLeaseLost) || called {
				t.Fatalf("stale writer admitted: called=%v err=%v", called, err)
			}
		})
	}
}

func TestDefaultReporterRetainsExpiredLeaseHeartbeat(t *testing.T) {
	db := newTestDB(t)
	job := enqueueTestJob(t, db, "default-heartbeat", 3)
	runner := newTestRunner(db)
	row, err := runner.claimOne(context.Background(), time.Now())
	if err != nil || row == nil {
		t.Fatalf("claim: %v", err)
	}
	if err := db.Model(&orm.AsyncJob{}).Where("id = ?", job.ID).Update("lock_until", time.Now().Add(-time.Minute)).Error; err != nil {
		t.Fatal(err)
	}
	reporter := &jobReporter{db: db, jobID: job.ID, workerID: row.LockedBy, attemptCount: row.AttemptCount, lockTTL: time.Minute}
	if err := reporter.Heartbeat(context.Background()); err != nil {
		t.Fatalf("default heartbeat changed: %v", err)
	}
}
