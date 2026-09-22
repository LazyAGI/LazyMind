package common_test

import (
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"lazymind/core/common"
)

type nestedTransactionRow struct {
	ID string `gorm:"primaryKey"`
}

func nestedTransactionDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "nested.db")), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	sqlDB.SetMaxOpenConns(4)
	t.Cleanup(func() { _ = sqlDB.Close() })
	if err := db.AutoMigrate(&nestedTransactionRow{}); err != nil {
		t.Fatal(err)
	}
	return db
}
func requireNestedRows(t *testing.T, db *gorm.DB, want ...string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	var got []string
	if err := db.WithContext(ctx).Model(&nestedTransactionRow{}).Order("id").Pluck("id", &got).Error; err != nil {
		t.Fatal(err)
	}
	if len(got) == 0 && len(want) == 0 {
		return
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("committed rows=%v want=%v", got, want)
	}
}

func TestSQLiteNestedTransactionCommitAndRollback(t *testing.T) {
	for _, kind := range []string{"commit", "inner rollback", "outer rollback"} {
		t.Run(kind, func(t *testing.T) {
			db := nestedTransactionDB(t)
			ctx, cancel := context.WithTimeout(t.Context(), time.Second)
			defer cancel()
			sentinel := errors.New("expected rollback")
			innerCalls := 0
			err := common.TransactionWithSQLiteBusyRetry(ctx, db, func(tx *gorm.DB) error {
				if err := tx.Create(&nestedTransactionRow{ID: "outer-before"}).Error; err != nil {
					return err
				}
				// The production caller passes its original request context, not a new
				// context returned by the helper. Derived GORM sessions must stay nested.
				innerErr := common.TransactionWithSQLiteBusyRetry(ctx, tx.Session(&gorm.Session{NewDB: true}).WithContext(ctx), func(inner *gorm.DB) error {
					innerCalls++
					if err := inner.Create(&nestedTransactionRow{ID: "inner"}).Error; err != nil {
						return err
					}
					if kind == "inner rollback" {
						return sentinel
					}
					return nil
				})
				if kind == "inner rollback" {
					if !errors.Is(innerErr, sentinel) {
						return innerErr
					}
				} else if innerErr != nil {
					return innerErr
				}
				if err := tx.Create(&nestedTransactionRow{ID: "outer-after"}).Error; err != nil {
					return err
				}
				if kind == "outer rollback" {
					return sentinel
				}
				return nil
			})
			if innerCalls != 1 {
				t.Errorf("inner calls=%d, nested transaction did not execute", innerCalls)
			}
			if kind == "outer rollback" {
				if !errors.Is(err, sentinel) {
					t.Errorf("outer rollback error=%v", err)
				}
			} else if err != nil {
				t.Errorf("nested transaction failed: %v", err)
			}
			switch kind {
			case "commit":
				requireNestedRows(t, db, "inner", "outer-after", "outer-before")
			case "inner rollback":
				requireNestedRows(t, db, "outer-after", "outer-before")
			case "outer rollback":
				requireNestedRows(t, db)
			}
		})
	}
}

func TestSQLiteNestedBusyRetriesWholeOuterTransaction(t *testing.T) {
	db := nestedTransactionDB(t)
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	outerCalls, innerCalls := 0, 0
	err := common.TransactionWithSQLiteBusyRetry(ctx, db, func(tx *gorm.DB) error {
		outerCalls++
		if err := tx.Create(&nestedTransactionRow{ID: "outer"}).Error; err != nil {
			return err
		}
		return common.TransactionWithSQLiteBusyRetry(ctx, tx, func(inner *gorm.DB) error {
			innerCalls++
			if err := inner.Create(&nestedTransactionRow{ID: "inner"}).Error; err != nil {
				return err
			}
			if innerCalls == 1 {
				return errors.New("database is locked (517)")
			}
			return nil
		})
	})
	if err != nil || outerCalls != 2 || innerCalls != 2 {
		t.Errorf("whole transaction retry: outer=%d inner=%d err=%v", outerCalls, innerCalls, err)
	}
	requireNestedRows(t, db, "inner", "outer")
}

func TestSQLiteNestedCancellationReleasesWriter(t *testing.T) {
	db := nestedTransactionDB(t)
	deadline, stop := context.WithTimeout(t.Context(), time.Second)
	defer stop()
	ctx, cancel := context.WithCancel(deadline)
	defer cancel()
	entered := false
	err := common.TransactionWithSQLiteBusyRetry(ctx, db, func(tx *gorm.DB) error {
		if err := tx.Create(&nestedTransactionRow{ID: "outer"}).Error; err != nil {
			return err
		}
		return common.TransactionWithSQLiteBusyRetry(ctx, tx, func(inner *gorm.DB) error {
			entered = true
			if err := inner.Create(&nestedTransactionRow{ID: "inner"}).Error; err != nil {
				return err
			}
			cancel()
			return ctx.Err()
		})
	})
	if !entered || !errors.Is(err, context.Canceled) {
		t.Errorf("nested cancellation: entered=%v err=%v", entered, err)
	}
	requireNestedRows(t, db)
	fresh, release := context.WithTimeout(t.Context(), time.Second)
	defer release()
	if err := common.TransactionWithSQLiteBusyRetry(fresh, db, func(tx *gorm.DB) error { return tx.Create(&nestedTransactionRow{ID: "next-writer"}).Error }); err != nil {
		t.Fatalf("writer gate leaked: %v", err)
	}
	requireNestedRows(t, db, "next-writer")
}

func TestSQLiteNestedCompletionKeepsOuterWriterGate(t *testing.T) {
	db := nestedTransactionDB(t)
	// A different DB avoids SQLite's own file lock masking loss of Core's
	// process-wide gate. Context reuse must not make an independent tx nested.
	other := nestedTransactionDB(t)
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	nested := false
	otherEntered := false
	err := common.TransactionWithSQLiteBusyRetry(ctx, db, func(tx *gorm.DB) error {
		if err := common.TransactionWithSQLiteBusyRetry(ctx, tx, func(inner *gorm.DB) error {
			nested = true
			return inner.Create(&nestedTransactionRow{ID: "inner"}).Error
		}); err != nil {
			return err
		}
		waiting, stop := context.WithTimeout(tx.Statement.Context, 100*time.Millisecond)
		defer stop()
		err := common.TransactionWithSQLiteBusyRetry(waiting, other.WithContext(tx.Statement.Context), func(writer *gorm.DB) error {
			otherEntered = true
			return writer.Create(&nestedTransactionRow{ID: "too-early"}).Error
		})
		if !errors.Is(err, context.DeadlineExceeded) {
			return errors.New("independent writer bypassed outer gate")
		}
		return tx.Create(&nestedTransactionRow{ID: "outer"}).Error
	})
	if err != nil || !nested || otherEntered {
		t.Errorf("outer gate: nested=%v other=%v err=%v", nested, otherEntered, err)
	}
	requireNestedRows(t, db, "inner", "outer")
	requireNestedRows(t, other)
	fresh, stop := context.WithTimeout(t.Context(), time.Second)
	defer stop()
	if err := common.TransactionWithSQLiteBusyRetry(fresh, other, func(tx *gorm.DB) error { return tx.Create(&nestedTransactionRow{ID: "after-commit"}).Error }); err != nil {
		t.Fatal(err)
	}
	requireNestedRows(t, other, "after-commit")
}

func TestSQLiteIndependentWriterCancellationControl(t *testing.T) {
	owner, waiter := nestedTransactionDB(t), nestedTransactionDB(t)
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	entered := false
	err := common.TransactionWithSQLiteBusyRetry(ctx, owner, func(tx *gorm.DB) error {
		waiting, stop := context.WithTimeout(ctx, 100*time.Millisecond)
		defer stop()
		blocked := common.TransactionWithSQLiteBusyRetry(waiting, waiter, func(other *gorm.DB) error {
			entered = true
			return other.Create(&nestedTransactionRow{ID: "unexpected"}).Error
		})
		if !errors.Is(blocked, context.DeadlineExceeded) {
			return errors.New("independent writer was not gated")
		}
		return tx.Create(&nestedTransactionRow{ID: "owner"}).Error
	})
	if err != nil || entered {
		t.Fatalf("independent writer gate: entered=%v err=%v", entered, err)
	}
	requireNestedRows(t, owner, "owner")
	requireNestedRows(t, waiter)
	if err := common.TransactionWithSQLiteBusyRetry(ctx, waiter, func(tx *gorm.DB) error { return tx.Create(&nestedTransactionRow{ID: "waiter"}).Error }); err != nil {
		t.Fatal(err)
	}
	requireNestedRows(t, waiter, "waiter")
}
