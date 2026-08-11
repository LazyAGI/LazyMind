package asyncjob

import (
	"errors"
	"testing"

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
