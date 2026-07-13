package service

import (
	"context"
	"testing"

	"gorm.io/gorm"
)

func TestHasRunningSkillReviewTask(t *testing.T) {
	db := newSkillV2TestDB(t)
	createSkillReviewStatsTable(t, db)
	svc := NewSkillService(SkillServiceDeps{DB: db})

	insertSkillReviewStats(t, db, map[string]any{
		"id":          "other-running",
		"requestid":   "req-other",
		"userid":      "user-2",
		"status":      "running",
		"started_at":  "2026-07-11T10:00:00Z",
		"duration_ms": 0,
		"summary":     "{}",
	})
	insertSkillReviewStats(t, db, map[string]any{
		"id":          "own-completed",
		"requestid":   "req-done",
		"userid":      "user-1",
		"status":      "completed",
		"started_at":  "2026-07-11T10:01:00Z",
		"duration_ms": 1,
		"summary":     "{}",
	})

	hasRunning, err := svc.HasRunningSkillReviewTask(context.Background(), " user-1 ")
	if err != nil {
		t.Fatalf("HasRunningSkillReviewTask returned error: %v", err)
	}
	if hasRunning {
		t.Fatal("HasRunningSkillReviewTask reported running for another user's running row or completed row")
	}

	insertSkillReviewStats(t, db, map[string]any{
		"id":          "own-running",
		"requestid":   "req-running",
		"userid":      "user-1",
		"status":      "running",
		"started_at":  "2026-07-11T10:02:00Z",
		"duration_ms": 0,
		"summary":     "{}",
	})

	hasRunning, err = svc.HasRunningSkillReviewTask(context.Background(), "user-1")
	if err != nil {
		t.Fatalf("HasRunningSkillReviewTask returned error: %v", err)
	}
	if !hasRunning {
		t.Fatal("HasRunningSkillReviewTask did not report the user's running row")
	}
}

func TestHasRunningSkillReviewTaskRequiresUserID(t *testing.T) {
	db := newSkillV2TestDB(t)
	createSkillReviewStatsTable(t, db)
	svc := NewSkillService(SkillServiceDeps{DB: db})

	if _, err := svc.HasRunningSkillReviewTask(context.Background(), " "); err == nil {
		t.Fatal("HasRunningSkillReviewTask accepted an empty user_id")
	}
}

func createSkillReviewStatsTable(t *testing.T, db *gorm.DB) {
	t.Helper()
	if err := db.Exec(`
CREATE TABLE skill_review_stats (
	id TEXT NOT NULL PRIMARY KEY,
	requestid TEXT NOT NULL,
	userid TEXT NOT NULL,
	status TEXT NOT NULL,
	started_at TEXT NOT NULL,
	duration_ms INTEGER NOT NULL DEFAULT 0,
	summary TEXT NOT NULL DEFAULT '{}'
)`).Error; err != nil {
		t.Fatalf("create skill_review_stats: %v", err)
	}
}

func insertSkillReviewStats(t *testing.T, db *gorm.DB, row map[string]any) {
	t.Helper()
	if err := db.Table("skill_review_stats").Create(row).Error; err != nil {
		t.Fatalf("insert skill_review_stats row: %v", err)
	}
}
