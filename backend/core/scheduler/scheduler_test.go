package scheduler

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"lazymind/core/common/orm"
)

func newTestSchedulerDB(t *testing.T) *orm.DB {
	t.Helper()
	db, err := orm.Connect(orm.DriverSQLite, filepath.Join(t.TempDir(), "sched.db"))
	if err != nil {
		t.Fatalf("connect db: %v", err)
	}
	if err := db.AutoMigrate(&orm.UserSchedule{}, &orm.TaskCenterTask{}); err != nil {
		t.Fatalf("auto migrate: %v", err)
	}
	return db
}

// ──────────────────────────────────────────────
// CreateSchedule + next_run_at calculation
// ──────────────────────────────────────────────

func TestCreateAndCancelSchedule(t *testing.T) {
	db := newTestSchedulerDB(t)
	ctx := context.Background()

	s := &orm.UserSchedule{
		UserID:         "user-1",
		CronExpr:       "0 9 * * 1",
		Timezone:       "Asia/Shanghai",
		PromptTemplate: "weekly report",
		Enabled:        true,
	}
	if err := CreateSchedule(ctx, db.DB, s); err != nil {
		t.Fatalf("CreateSchedule: %v", err)
	}
	if s.ID == "" {
		t.Fatal("expected non-empty ID")
	}
	if s.NextRunAt.IsZero() {
		t.Fatal("expected non-zero next_run_at")
	}

	// Cancel the schedule.
	if err := CancelSchedule(ctx, db.DB, "user-1", s.ID); err != nil {
		t.Fatalf("CancelSchedule: %v", err)
	}
	var got orm.UserSchedule
	if err := db.First(&got, "id = ?", s.ID).Error; err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if got.Enabled {
		t.Fatal("expected schedule to be disabled after cancel")
	}
}

// ──────────────────────────────────────────────
// Optimistic lock — only one attempt fires per tick
// ──────────────────────────────────────────────

func TestFireOne_OptimisticLock(t *testing.T) {
	db := newTestSchedulerDB(t)

	// Create a schedule whose next_run_at is in the past so it fires immediately.
	oldNext := time.Now().UTC().Add(-time.Minute)
	s := &orm.UserSchedule{
		ID:             "sched-lock-1",
		UserID:         "user-lock",
		CronExpr:       "* * * * *",
		Timezone:       "UTC",
		PromptTemplate: "lock test",
		Enabled:        true,
		NextRunAt:      oldNext,
		CreatedAt:      time.Now().UTC(),
	}
	if err := db.Create(s).Error; err != nil {
		t.Fatalf("seed: %v", err)
	}

	newNext := time.Now().UTC().Add(time.Minute)
	// First attempt: should succeed (WHERE next_run_at = oldNext matches).
	r1 := db.Model(&orm.UserSchedule{}).
		Where("id = ? AND next_run_at = ?", "sched-lock-1", oldNext).
		Updates(map[string]any{"last_run_at": time.Now().UTC(), "next_run_at": newNext})
	if r1.RowsAffected != 1 {
		t.Fatalf("first attempt: expected 1 row affected, got %d", r1.RowsAffected)
	}

	// Second attempt with same old next_run_at: should fail (row already updated).
	r2 := db.Model(&orm.UserSchedule{}).
		Where("id = ? AND next_run_at = ?", "sched-lock-1", oldNext).
		Updates(map[string]any{"last_run_at": time.Now().UTC(), "next_run_at": newNext})
	if r2.RowsAffected != 0 {
		t.Fatalf("second attempt should be skipped (optimistic lock), got %d rows affected", r2.RowsAffected)
	}
}
