package resourceupdate

import (
	"path/filepath"
	"testing"

	"gorm.io/gorm"

	"lazymind/core/common/orm"
)

// newDBTestDB creates a SQLite DB for testing db.go helpers.
func newDBTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := orm.Connect(orm.DriverSQLite, filepath.Join(t.TempDir(), "dbtest.db"))
	if err != nil {
		t.Fatalf("connect db: %v", err)
	}
	return db.DB
}

// TestWithUpdateLock returns db as-is for SQLite (no locking clause needed).
func TestWithUpdateLock(t *testing.T) {
	db := newDBTestDB(t)
	result := withUpdateLock(db)
	if result == nil {
		t.Fatal("should return non-nil db for SQLite")
	}
}

// TestWithUpdateLockNilDB returns nil for nil input.
func TestWithUpdateLockNilDB(t *testing.T) {
	if got := withUpdateLock(nil); got != nil {
		t.Fatal("nil db should return nil")
	}
}

// TestWithUpdateSkipLocked returns db as-is for SQLite.
func TestWithUpdateSkipLocked(t *testing.T) {
	db := newDBTestDB(t)
	result := withUpdateSkipLocked(db)
	if result == nil {
		t.Fatal("should return non-nil db for SQLite")
	}
}

// TestWithUpdateSkipLockedNilDB returns nil for nil input.
func TestWithUpdateSkipLockedNilDB(t *testing.T) {
	if got := withUpdateSkipLocked(nil); got != nil {
		t.Fatal("nil db should return nil")
	}
}

// TestClauseOnConflictDoNothing returns DoNothing clause.
func TestClauseOnConflictDoNothing(t *testing.T) {
	clause := clauseOnConflictDoNothing()
	if !clause.DoNothing {
		t.Fatal("DoNothing should be true")
	}
}
