package common_test

import (
	"context"
	"database/sql"
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"

	"lazymind/core/common"
)

type proxyMarkedDialector struct {
	gorm.Dialector
}

func (proxyMarkedDialector) IsSQLiteProxy() bool { return true }

func TestImmediateTransactionUsesDriverProtocolForSQLiteProxy(t *testing.T) {
	db, err := gorm.Open(proxyMarkedDialector{Dialector: sqlite.Open(":memory:")}, &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	// NewDB deliberately discards statement-scoped settings; proxy detection is
	// tied to the dialector so it remains correct for derived GORM sessions.
	db = db.Session(&gorm.Session{NewDB: true})

	err = common.ImmediateTransactionWithSQLiteBusyRetry(context.Background(), db, func(tx *gorm.DB) error {
		if _, ok := tx.Statement.ConnPool.(*sql.Tx); !ok {
			t.Fatalf("proxy-marked database did not use database/sql transaction protocol: %T", tx.Statement.ConnPool)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("transaction failed: %v", err)
	}
}
