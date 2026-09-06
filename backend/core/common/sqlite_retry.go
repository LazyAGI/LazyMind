package common

import (
	"context"
	"math/rand/v2"
	"strings"
	"time"

	"gorm.io/gorm"
)

const (
	sqliteTransactionAttempts = 10
	sqliteRetryInitialDelay   = 10 * time.Millisecond
	sqliteRetryMaxDelay       = 750 * time.Millisecond
)

// sqliteWriterGate keeps Core's known multi-statement SQLite write paths from
// racing each other. It is intentionally process-wide: Desktop stores are
// local and these transactions are short, while PostgreSQL never uses it.
var sqliteWriterGate = make(chan struct{}, 1)

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
	return transactionWithSQLiteBusyRetry(ctx, db, fn, false)
}

// ImmediateTransactionWithSQLiteBusyRetry is intended for short SQLite
// read-then-write callbacks that never open nested transactions. It reserves
// the writer before the first read, avoiding SQLITE_BUSY_SNAPSHOT entirely for
// in-process contenders. Other database engines use a normal GORM transaction.
func ImmediateTransactionWithSQLiteBusyRetry(
	ctx context.Context,
	db *gorm.DB,
	fn func(tx *gorm.DB) error,
) error {
	return transactionWithSQLiteBusyRetry(ctx, db, fn, true)
}

func transactionWithSQLiteBusyRetry(
	ctx context.Context,
	db *gorm.DB,
	fn func(tx *gorm.DB) error,
	immediate bool,
) error {
	if db == nil {
		return gorm.ErrInvalidDB
	}
	if db.Dialector.Name() != "sqlite" {
		return db.WithContext(ctx).Transaction(fn)
	}
	select {
	case sqliteWriterGate <- struct{}{}:
		defer func() { <-sqliteWriterGate }()
	case <-ctx.Done():
		return ctx.Err()
	}

	for attempt := 0; attempt < sqliteTransactionAttempts; attempt++ {
		var err error
		if immediate {
			err = sqliteImmediateTransaction(ctx, db, fn)
		} else {
			err = db.WithContext(ctx).Transaction(fn)
		}
		if err == nil || !IsSQLiteBusy(err) || attempt == sqliteTransactionAttempts-1 {
			return err
		}

		delay := sqliteRetryDelay(attempt)
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

// sqliteImmediateTransaction reserves SQLite's only writer before the callback
// reads any rows. This prevents a deferred transaction from reading a snapshot
// that another writer invalidates before the callback's first write (error 517).
func sqliteImmediateTransaction(ctx context.Context, db *gorm.DB, fn func(tx *gorm.DB) error) error {
	return db.WithContext(ctx).Connection(func(conn *gorm.DB) error {
		if err := conn.Exec("BEGIN IMMEDIATE").Error; err != nil {
			return err
		}

		committed := false
		defer func() {
			if !committed {
				conn.WithContext(context.WithoutCancel(ctx)).Exec("ROLLBACK")
			}
		}()

		// BEGIN IMMEDIATE already owns the transaction. Disable GORM's default
		// per-write transaction so Create/Update do not try to nest another BEGIN.
		tx := conn.WithContext(ctx).Session(&gorm.Session{SkipDefaultTransaction: true})
		if err := fn(tx); err != nil {
			return err
		}
		if err := tx.Exec("COMMIT").Error; err != nil {
			return err
		}
		committed = true
		return nil
	})
}

func sqliteRetryDelay(attempt int) time.Duration {
	delay := sqliteRetryInitialDelay << attempt
	if delay > sqliteRetryMaxDelay {
		delay = sqliteRetryMaxDelay
	}
	// Spread simultaneous writers over a 75%-125% window so retries do not
	// repeatedly collide with each other.
	quarter := delay / 4
	if quarter == 0 {
		return delay
	}
	return delay - quarter + time.Duration(rand.Int64N(int64(quarter*2)+1))
}
