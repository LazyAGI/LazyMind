package orm

import (
	"fmt"
	"io"
	stdlog "log"
	"os"
	"strings"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/driver/mysql"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"

	"lazymind/core/log"
)

// DB text *gorm.DB，text ACL text。text PostgreSQL、SQLite、MySQL。
type DB struct {
	*gorm.DB
}

// Connect text
const (
	DriverPostgres = "postgres"
	DriverSQLite   = "sqlite"
	DriverMySQL    = "mysql"
)

// Connect text。driver: postgres / sqlite / mysql，dsn text。
func Connect(driver, dsn string) (*DB, error) {
	var dialector gorm.Dialector
	switch driver {
	case DriverPostgres:
		dialector = postgres.Open(dsn)
	case DriverSQLite:
		dialector = sqlite.Open(dsn)
	case DriverMySQL:
		dialector = mysql.Open(dsn)
	default:
		return nil, fmt.Errorf("unsupported driver: %s (use postgres, sqlite, mysql)", driver)
	}
	db, err := gorm.Open(dialector, &gorm.Config{Logger: newGORMLogger(os.Stdout)})
	if err != nil {
		return nil, err
	}
	if driver == DriverSQLite {
		sqlDB, err := db.DB()
		if err != nil {
			return nil, err
		}
		// Keep enough connections for legacy transaction callbacks that perform a
		// nested read through the root DB. Contended write paths are serialized by
		// TransactionWithSQLiteBusyRetry instead of constraining the whole pool.
		sqlDB.SetMaxOpenConns(4)
		for _, stmt := range []string{
			"PRAGMA busy_timeout=30000",
			"PRAGMA journal_mode=WAL",
			"PRAGMA synchronous=NORMAL",
			"PRAGMA foreign_keys=ON",
		} {
			if _, err := sqlDB.Exec(stmt); err != nil && !strings.Contains(strings.ToLower(err.Error()), "database is locked") {
				return nil, err
			}
		}
	}
	return &DB{DB: db}, nil
}

func newGORMLogger(out io.Writer) gormlogger.Interface {
	return gormlogger.New(stdlog.New(out, "\r\n", stdlog.LstdFlags), gormlogger.Config{
		SlowThreshold:             200 * time.Millisecond,
		LogLevel:                  gormlogger.Warn,
		IgnoreRecordNotFoundError: true,
		ParameterizedQueries:      true,
		Colorful:                  true,
	})
}

// MustConnect text，Failedtext Fatal Logtext，text main text。
func MustConnect(driver, dsn string) *DB {
	db, err := Connect(driver, dsn)
	if err != nil {
		log.Logger.Fatal().Err(err).Str("driver", driver).Msg("orm: connect failed")
	}
	return db
}
