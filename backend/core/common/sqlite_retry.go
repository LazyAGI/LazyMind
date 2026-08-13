package common

import (
	"context"
	"strings"
	"time"

	"gorm.io/gorm"
)

const sqliteTransactionAttempts = 6

// IsSQLiteBusy reports SQLite's retryable writer-contention errors. Code 517
// (SQLITE_BUSY_SNAPSHOT) is commonly rendered as "database is locked (517)".
func IsSQLiteBusy(err error) bool {
	if err == nil {
		return false
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "database is locked") ||
		strings.Contains(message, "sqlite_busy")
}

// TransactionWithSQLiteBusyRetry retries the whole transaction after SQLite
// writer contention. Retrying only the failed statement is unsafe for
// SQLITE_BUSY_SNAPSHOT because the transaction must acquire a fresh snapshot.
func TransactionWithSQLiteBusyRetry(
	ctx context.Context,
	db *gorm.DB,
	fn func(tx *gorm.DB) error,
) error {
	if db == nil || db.Dialector.Name() != "sqlite" {
		return db.WithContext(ctx).Transaction(fn)
	}

	for attempt := 0; attempt < sqliteTransactionAttempts; attempt++ {
		err := db.WithContext(ctx).Transaction(fn)
		if err == nil || !IsSQLiteBusy(err) || attempt == sqliteTransactionAttempts-1 {
			return err
		}

		delay := time.Duration(10*(1<<attempt)) * time.Millisecond
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				<-timer.C
			}
			return ctx.Err()
		case <-timer.C:
		}
	}
	return nil
}
