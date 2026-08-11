package search

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"gorm.io/gorm"

	sqliteDriver "gorm.io/driver/sqlite"
	skilltestutil "lazymind/core/skillv2/testutil"
)

// newSearchTestDB creates a SQLite DB with skill tables for search tests.
func newSearchTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqliteDriver.Open(filepath.Join(t.TempDir(), "search.db")), &gorm.Config{})
	if err != nil {
		t.Fatalf("connect sqlite: %v", err)
	}
	if err := db.AutoMigrate(
		&skillRow{},
		&skilltestutil.SkillBlobRow{},
		&skilltestutil.SkillRevisionRow{},
		&skilltestutil.SkillRevisionEntryRow{},
	); err != nil {
		t.Fatalf("auto migrate: %v", err)
	}
	return db
}

// testError is a simple error type for testing error-based functions.
type testError struct{ msg string }

func (e *testError) Error() string { return e.msg }

// TestIsMissingIndexTable detects missing skill_search_indexes table from error messages.
func TestIsMissingIndexTable(t *testing.T) {
	tests := []struct {
		errMsg string
		want   bool
	}{
		{"", false},
		{"skill_search_indexes: no such table", true},
		{"relation \"skill_search_indexes\" does not exist", true},
		{"ERROR: sqlstate 42p01: skill_search_indexes", true},
		{"unknown table", false},
	}
	for _, tt := range tests {
		t.Run(tt.errMsg, func(t *testing.T) {
			var err error
			if tt.errMsg != "" {
				err = &testError{msg: tt.errMsg}
			}
			if got := isMissingIndexTable(err); got != tt.want {
				t.Fatalf("isMissingIndexTable(%q) = %v, want %v", tt.errMsg, got, tt.want)
			}
		})
	}
}

// TestContainsHeadText_MissingSkill returns false with error.
func TestContainsHeadText_MissingSkill(t *testing.T) {
	db := newSearchTestDB(t)
	_, err := containsHeadText(context.Background(), db, "nonexistent", "test")
	if err == nil {
		t.Fatal("expected error for missing skill")
	}
}

// TestContainsHeadText_DeletedSkill returns false, nil error.
func TestContainsHeadText_DeletedSkill(t *testing.T) {
	db := newSearchTestDB(t)
	now := time.Now()
	deleted := now
	db.Create(&skillRow{
		ID: "s1", OwnerUserID: "u1", Category: "test", SkillName: "Test",
		DeletedAt: &deleted,
	})
	got, err := containsHeadText(context.Background(), db, "s1", "hello")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got {
		t.Fatal("expected false for deleted skill")
	}
}

// TestContainsHeadText_NoHeadRevision returns false.
func TestContainsHeadText_NoHeadRevision(t *testing.T) {
	db := newSearchTestDB(t)
	db.Create(&skillRow{
		ID: "s2", OwnerUserID: "u1", Category: "test", SkillName: "Test",
		HeadRevisionID: nil,
	})
	got, err := containsHeadText(context.Background(), db, "s2", "hello")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got {
		t.Fatal("expected false for nil head revision")
	}
}
