package orm

import (
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
)

func TestSkillReviewStatsActiveScope(t *testing.T) {
	db, err := Connect(DriverSQLite, filepath.Join(t.TempDir(), "skill-review-stats.db"))
	if err != nil {
		t.Fatalf("connect sqlite: %v", err)
	}
	if err := db.Exec(`CREATE TABLE skill_review_stats (id TEXT PRIMARY KEY, requestid TEXT NOT NULL, userid TEXT NOT NULL, status TEXT NOT NULL)`).Error; err != nil {
		t.Fatalf("create skill_review_stats: %v", err)
	}
	for id, status := range map[string]string{
		"review-draft":   SkillReviewStatsStatusReviewDraft,
		"organize-draft": SkillReviewStatsStatusOrganizeDraft,
		"completed":      "completed",
		"skipped":        "skipped",
		"failed":         "failed",
	} {
		if err := db.Table("skill_review_stats").Create(map[string]any{"id": id, "requestid": id, "userid": "user-1", "status": status}).Error; err != nil {
			t.Fatalf("insert status %q: %v", status, err)
		}
	}
	if err := db.Table("skill_review_stats").Create(map[string]any{
		"id": "duplicate-completed", "requestid": "review-draft", "userid": "user-1", "status": SkillReviewStatsStatusCompleted,
	}).Error; err != nil {
		t.Fatalf("insert completed duplicate: %v", err)
	}

	var ids []string
	if err := db.Table("skill_review_stats").Scopes(SkillReviewStatsActiveScope).Order("id").Pluck("id", &ids).Error; err != nil {
		t.Fatalf("query active statuses: %v", err)
	}
	want := []string{"organize-draft"}
	if !reflect.DeepEqual(ids, want) {
		t.Fatalf("active ids = %#v, want %#v", ids, want)
	}
}

func TestDetailedSkillReviewStatsStatusMigrationHasNoEnumConstraint(t *testing.T) {
	_, file, _, _ := runtime.Caller(0)
	migrationPath := filepath.Join(filepath.Dir(file), "..", "..", "migrations", "20260714190000_allow_detailed_skill_review_stats_status.up.sql")
	body, err := os.ReadFile(migrationPath)
	if err != nil {
		t.Fatal(err)
	}
	content := string(body)
	if !strings.Contains(content, "DROP CONSTRAINT IF EXISTS chk_skill_review_stats_status") {
		t.Fatal("migration must remove the legacy status enum constraint")
	}
	if strings.Contains(content, "ADD CONSTRAINT") || strings.Contains(content, "CHECK (") {
		t.Fatal("migration must not add a status value constraint")
	}
}
