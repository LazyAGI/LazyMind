package orm

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// OpenTestDB opens a test database.  The driver is selected by TEST_DB_DRIVER.
// Defaults to SQLite when the variable is unset or set to "sqlite".
//
// PostgreSQL mode (TEST_DB_DRIVER=postgres, requires TEST_DB_DSN) creates an
// isolated schema per test and cleans it up via t.Cleanup.
func OpenTestDB(t testing.TB) *DB {
	t.Helper()

	driver := strings.ToLower(strings.TrimSpace(os.Getenv("TEST_DB_DRIVER")))
	switch {
	case driver == DriverPostgres:
		return openTestPostgres(t)
	default:
		return openTestSQLite(t)
	}
}

// MigrateTestDB opens a test database and runs AutoMigrate for the given models.
func MigrateTestDB(t testing.TB, models ...any) *DB {
	t.Helper()
	db := OpenTestDB(t)
	if err := db.AutoMigrate(models...); err != nil {
		t.Fatalf("auto migrate: %v", err)
	}
	return db
}

// MigrateAllModelsForTest opens a test database and runs AutoMigrate for
// AllModelsForDDL() — every model known to the application.
func MigrateAllModelsForTest(t testing.TB) *DB {
	t.Helper()
	return MigrateTestDB(t, AllModelsForDDL()...)
}

// ---------------------------------------------------------------------------
// internal helpers
// ---------------------------------------------------------------------------

func openTestSQLite(t testing.TB) *DB {
	dsn := filepath.Join(t.TempDir(), "test.db")
	db, err := Connect(DriverSQLite, dsn)
	if err != nil {
		t.Fatalf("connect sqlite: %v", err)
	}
	sqlDB, err := db.DB.DB()
	if err != nil {
		t.Fatalf("get sql db: %v", err)
	}
	// Close the pool before t.TempDir cleanup runs, so no sqlite WAL/shm
	// activity can race with RemoveAll (flaky "directory not empty" on CI).
	t.Cleanup(func() { _ = sqlDB.Close() })
	return db
}

func openTestPostgres(t testing.TB) *DB {
	baseDSN := strings.TrimSpace(os.Getenv("TEST_DB_DSN"))
	if baseDSN == "" {
		t.Fatal("TEST_DB_DRIVER=postgres requires TEST_DB_DSN")
	}

	schema := randomTestSchema(t)
	dsn := appendSearchPath(baseDSN, schema)

	db, err := Connect(DriverPostgres, dsn)
	if err != nil {
		t.Fatalf("connect postgres: %v", err)
	}

	if err := db.Exec("CREATE SCHEMA IF NOT EXISTS " + quoteIdent(schema)).Error; err != nil {
		t.Fatalf("create schema %q: %v", schema, err)
	}

	sqlDB, err := db.DB.DB()
	if err != nil {
		t.Fatalf("get sql db: %v", err)
	}
	sqlDB.SetMaxOpenConns(5)
	sqlDB.SetMaxIdleConns(2)
	sqlDB.SetConnMaxLifetime(30 * time.Minute)

	t.Cleanup(func() {
		if err := db.Exec("DROP SCHEMA IF EXISTS " + quoteIdent(schema) + " CASCADE").Error; err != nil {
			t.Logf("cleanup: drop schema %q: %v", schema, err)
		}
		_ = sqlDB.Close()
	})

	return db
}

func randomTestSchema(t testing.TB) string {
	b := make([]byte, 4)
	if _, err := rand.Read(b); err != nil {
		t.Fatalf("rand: %v", err)
	}

	name := strings.Map(func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '_' {
			return r
		}
		return '_'
	}, strings.ToLower(t.Name()))

	if len(name) > 40 {
		name = name[:40]
	}

	return fmt.Sprintf("test_%s_%s", name, hex.EncodeToString(b))
}

func appendSearchPath(dsn, schema string) string {
	if strings.Contains(dsn, "?") {
		return dsn + "&search_path=" + schema
	}
	return dsn + "?search_path=" + schema
}

func quoteIdent(s string) string {
	return `"` + strings.ReplaceAll(s, `"`, `""`) + `"`
}
