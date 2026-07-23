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

func TestMatchDayOfMonthSupportsMonthEndOffsets(t *testing.T) {
	loc := time.UTC
	if !matchDayOfMonth("-1", time.Date(2026, time.February, 28, 9, 0, 0, 0, loc)) {
		t.Fatal("expected -1 to match the last day of February")
	}
	if !matchDayOfMonth("-2", time.Date(2026, time.April, 29, 9, 0, 0, 0, loc)) {
		t.Fatal("expected -2 to match the second-to-last day of April")
	}
	if matchDayOfMonth("-1", time.Date(2026, time.April, 29, 9, 0, 0, 0, loc)) {
		t.Fatal("expected -1 not to match before the last day")
	}
}

func TestCadenceExpression(t *testing.T) {
	interval, unit, cronExpr, err := parseCadenceExpr("@every:2:week;0 9 * * 1")
	if err != nil || interval != 2 || unit != "week" || cronExpr != "0 9 * * 1" {
		t.Fatalf("unexpected cadence parse: %d %q %q %v", interval, unit, cronExpr, err)
	}
	matching := time.Date(2026, time.January, 5, 9, 0, 0, 0, time.UTC)
	if matchCadence(matching, 2, "week") == matchCadence(matching.AddDate(0, 0, 7), 2, "week") {
		t.Fatal("adjacent ISO weeks must not both match a two-week cadence")
	}
}

func TestPreviousCronTimeUsesPriorScheduledCycle(t *testing.T) {
	next := time.Date(2026, time.July, 30, 12, 0, 0, 0, time.UTC)
	previous, err := previousCronTime("0 12 * * 4", "UTC", next)
	if err != nil {
		t.Fatal(err)
	}
	want := time.Date(2026, time.July, 23, 12, 0, 0, 0, time.UTC)
	if !previous.Equal(want) {
		t.Fatalf("previous cycle = %s, want %s", previous, want)
	}
}
