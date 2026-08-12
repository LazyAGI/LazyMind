package scheduler

import (
	"context"
	"testing"
	"time"

	"lazymind/core/common/orm"
)

func newTestSchedulerDB(t *testing.T) *orm.DB {
	t.Helper()
	return orm.MigrateTestDB(t, &orm.UserSchedule{}, &orm.TaskCenterTask{}, &orm.UserUIPreferences{})
}

func TestFireSchedulesSkipsPausedTaskCenterAndMovesNextRunForward(t *testing.T) {
	db := newTestSchedulerDB(t)
	now := time.Now().UTC()
	schedule := orm.UserSchedule{
		ID: "sched-paused", UserID: "user-1", CronExpr: "*/10 * * * *", Timezone: "UTC", PromptTemplate: "daily",
		Enabled: true, NextRunAt: now.Add(-time.Hour), CreatedAt: now.Add(-time.Hour),
	}
	if err := db.Create(&schedule).Error; err != nil {
		t.Fatalf("seed schedule: %v", err)
	}
	if err := db.Model(&orm.UserUIPreferences{}).Create(map[string]any{
		"user_id": "user-1", "task_center_enabled": false, "skills_enabled": true, "mcp_enabled": true,
		"created_at": now, "updated_at": now,
	}).Error; err != nil {
		t.Fatalf("seed controls: %v", err)
	}

	fireSchedules(context.Background(), db.DB, "")

	var saved orm.UserSchedule
	if err := db.Where("id = ?", schedule.ID).Take(&saved).Error; err != nil {
		t.Fatalf("load schedule: %v", err)
	}
	if !saved.NextRunAt.After(now) {
		t.Fatalf("paused schedule must move forward, got %s", saved.NextRunAt)
	}
	if saved.RunCount != 0 || saved.LastRunAt != nil {
		t.Fatalf("paused schedule must not produce a new run: %#v", saved)
	}
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

func TestNextCronTimeAfterUsesScheduleTimezone(t *testing.T) {
	now := time.Date(2026, time.July, 30, 5, 30, 0, 0, time.UTC)

	next, err := nextCronTimeAfter("0 14 * * *", "Asia/Shanghai", now)
	if err != nil {
		t.Fatalf("nextCronTimeAfter: %v", err)
	}
	want := time.Date(2026, time.July, 30, 6, 0, 0, 0, time.UTC)
	if !next.Equal(want) {
		t.Fatalf("next run = %s, want %s", next, want)
	}
}

func TestNextCronTimeRejectsUnknownTimezone(t *testing.T) {
	if _, err := nextCronTimeAfter("0 14 * * *", "Invalid/Timezone", time.Now()); err == nil {
		t.Fatal("expected an invalid timezone error")
	}
	if _, err := previousCronTime("0 14 * * *", "Invalid/Timezone", time.Now()); err == nil {
		t.Fatal("expected an invalid timezone error for previous cron time")
	}
}

func TestRepairFutureScheduleNextRunsCorrectsTimezoneFallback(t *testing.T) {
	db := newTestSchedulerDB(t)
	ctx := context.Background()
	now := time.Date(2026, time.July, 30, 5, 30, 0, 0, time.UTC)
	wrongFuture := time.Date(2026, time.July, 30, 14, 0, 0, 0, time.UTC)
	overdue := time.Date(2026, time.July, 29, 6, 0, 0, 0, time.UTC)

	for _, schedule := range []*orm.UserSchedule{
		{
			ID:             "sched-wrong-timezone",
			UserID:         "user-1",
			CronExpr:       "0 14 * * *",
			Timezone:       "Asia/Shanghai",
			PromptTemplate: "future",
			Enabled:        true,
			NextRunAt:      wrongFuture,
			CreatedAt:      now.Add(-time.Hour),
		},
		{
			ID:             "sched-overdue",
			UserID:         "user-1",
			CronExpr:       "0 14 * * *",
			Timezone:       "Asia/Shanghai",
			PromptTemplate: "overdue",
			Enabled:        true,
			NextRunAt:      overdue,
			CreatedAt:      now.Add(-48 * time.Hour),
		},
	} {
		if err := db.Create(schedule).Error; err != nil {
			t.Fatalf("seed schedule %s: %v", schedule.ID, err)
		}
	}

	repairFutureScheduleNextRunsAt(ctx, db.DB, now)

	var repaired orm.UserSchedule
	if err := db.First(&repaired, "id = ?", "sched-wrong-timezone").Error; err != nil {
		t.Fatalf("fetch repaired schedule: %v", err)
	}
	want := time.Date(2026, time.July, 30, 6, 0, 0, 0, time.UTC)
	if !repaired.NextRunAt.Equal(want) {
		t.Fatalf("repaired next run = %s, want %s", repaired.NextRunAt, want)
	}

	var preserved orm.UserSchedule
	if err := db.First(&preserved, "id = ?", "sched-overdue").Error; err != nil {
		t.Fatalf("fetch overdue schedule: %v", err)
	}
	if !preserved.NextRunAt.Equal(overdue) {
		t.Fatalf("overdue next run changed to %s, want %s", preserved.NextRunAt, overdue)
	}
}

func TestDeleteScheduleRemovesRuleAndDependencyEdgesKeepsHistory(t *testing.T) {
	db := newTestSchedulerDB(t)
	if err := db.AutoMigrate(&orm.ScheduleDependency{}); err != nil {
		t.Fatalf("auto migrate dependencies: %v", err)
	}
	now := time.Now().UTC()
	schedules := []orm.UserSchedule{
		{ID: "source", UserID: "user-1", CronExpr: "0 9 * * 1", Timezone: "UTC", PromptTemplate: "source", Enabled: true, NextRunAt: now, CreatedAt: now},
		{ID: "target", UserID: "user-1", CronExpr: "0 10 * * 1", Timezone: "UTC", PromptTemplate: "target", Enabled: true, NextRunAt: now, CreatedAt: now},
		{ID: "other", UserID: "user-1", CronExpr: "0 11 * * 1", Timezone: "UTC", PromptTemplate: "other", Enabled: true, NextRunAt: now, CreatedAt: now},
	}
	if err := db.Create(&schedules).Error; err != nil {
		t.Fatalf("seed schedules: %v", err)
	}
	deps := []orm.ScheduleDependency{
		{ID: "dep-in", UserID: "user-1", SourceScheduleID: "source", TargetScheduleID: "target", Enabled: true, CreatedAt: now, UpdatedAt: now},
		{ID: "dep-out", UserID: "user-1", SourceScheduleID: "target", TargetScheduleID: "other", Enabled: true, CreatedAt: now, UpdatedAt: now},
		{ID: "dep-keep", UserID: "user-1", SourceScheduleID: "source", TargetScheduleID: "other", Enabled: true, CreatedAt: now, UpdatedAt: now},
	}
	if err := db.Create(&deps).Error; err != nil {
		t.Fatalf("seed dependencies: %v", err)
	}
	scheduleID := "target"
	history := orm.TaskCenterTask{ID: "target-history", UserID: "user-1", TaskType: "scheduled", Status: "succeeded", ScheduleID: &scheduleID, CreatedAt: now, UpdatedAt: now}
	if err := db.Create(&history).Error; err != nil {
		t.Fatalf("seed history: %v", err)
	}

	if err := DeleteSchedule(context.Background(), db.DB, "user-1", "target"); err != nil {
		t.Fatalf("delete schedule: %v", err)
	}
	var deleted orm.UserSchedule
	if err := db.First(&deleted, "id = ?", "target").Error; err == nil {
		t.Fatal("expected deleted schedule to be absent")
	}
	var dependencyCount int64
	if err := db.Model(&orm.ScheduleDependency{}).
		Where("source_schedule_id = ? OR target_schedule_id = ?", "target", "target").Count(&dependencyCount).Error; err != nil {
		t.Fatal(err)
	}
	if dependencyCount != 0 {
		t.Fatalf("expected deleted schedule dependencies to be removed, got %d", dependencyCount)
	}
	var keptDependency orm.ScheduleDependency
	if err := db.First(&keptDependency, "id = ?", "dep-keep").Error; err != nil {
		t.Fatalf("unrelated dependency was removed: %v", err)
	}
	var keptHistory orm.TaskCenterTask
	if err := db.First(&keptHistory, "id = ?", history.ID).Error; err != nil {
		t.Fatalf("task history was removed with schedule: %v", err)
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
