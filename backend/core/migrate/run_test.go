package migrate

import (
	"database/sql"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	_ "github.com/glebarez/go-sqlite"
)

func TestRunUpAppliesMissingLowerVersionMigrationAfterManualHistoryBackfill(t *testing.T) {
	migrationsDir := filepath.Join(t.TempDir(), "migrations")
	if err := os.MkdirAll(migrationsDir, 0o755); err != nil {
		t.Fatalf("mkdir migrations dir: %v", err)
	}

	for _, name := range []string{
		"20260423120000_create_word.up.sql",
		"20260423120000_create_word.down.sql",
		"20260424130000_add_user_personalization_settings.up.sql",
		"20260424130000_add_user_personalization_settings.down.sql",
	} {
		copyMigrationFile(t, migrationsDir, name)
	}

	dbPath := filepath.Join(t.TempDir(), "acl.db")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open sqlite db: %v", err)
	}
	defer db.Close()

	if _, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS schema_migrations (version uint64, dirty bool);
		CREATE UNIQUE INDEX IF NOT EXISTS version_unique ON schema_migrations (version);
		CREATE TABLE IF NOT EXISTS schema_migration_history (
		  version bigint NOT NULL PRIMARY KEY,
		  name varchar(255) NOT NULL DEFAULT '',
		  applied_at timestamp NOT NULL DEFAULT CURRENT_TIMESTAMP
		);
		DELETE FROM schema_migrations;
		INSERT INTO schema_migrations (version, dirty) VALUES (20260424130000, 0);
		DELETE FROM schema_migration_history;
		INSERT INTO schema_migration_history (version, name) VALUES (20260424130000, 'add_user_personalization_settings');
	`); err != nil {
		t.Fatalf("seed migration state: %v", err)
	}

	t.Setenv("ACL_DB_DRIVER", "sqlite")
	t.Setenv("ACL_DB_DSN", dbPath)
	t.Setenv("MIGRATIONS_DIR", migrationsDir)

	if err := RunUp(); err != nil {
		t.Fatalf("RunUp: %v", err)
	}

	var exists int
	if err := db.QueryRow(`
		SELECT COUNT(1)
		FROM sqlite_master
		WHERE type = 'table' AND name = 'words'
	`).Scan(&exists); err != nil {
		t.Fatalf("query words table existence: %v", err)
	}
	if exists != 1 {
		t.Fatalf("expected words table to exist after compensating migration, got count=%d", exists)
	}

	var version uint64
	var dirty bool
	if err := db.QueryRow(`SELECT version, dirty FROM schema_migrations LIMIT 1`).Scan(&version, &dirty); err != nil {
		t.Fatalf("read schema_migrations: %v", err)
	}
	if version != 20260424130000 || dirty {
		t.Fatalf("expected schema_migrations to stay at highest applied version 20260424130000 clean, got version=%d dirty=%v", version, dirty)
	}

	var historyCount int
	if err := db.QueryRow(`SELECT COUNT(1) FROM schema_migration_history WHERE version = 20260423120000`).Scan(&historyCount); err != nil {
		t.Fatalf("query schema_migration_history for 20260423120000: %v", err)
	}
	if historyCount != 1 {
		t.Fatalf("expected schema_migration_history to record 20260423120000 after manual backfill path, got count=%d", historyCount)
	}
}

func TestRunnerAppliesLateMergedMigrationAfterHistoryBootstrap(t *testing.T) {
	migrationsDir := filepath.Join(t.TempDir(), "migrations")
	if err := os.MkdirAll(migrationsDir, 0o755); err != nil {
		t.Fatalf("mkdir migrations dir: %v", err)
	}

	writeMigrationPair(t, migrationsDir, "20260401010000_create_alpha", `
CREATE TABLE IF NOT EXISTS alpha (
  id integer PRIMARY KEY
);
`, `
DROP TABLE IF EXISTS alpha;
`)
	writeMigrationPair(t, migrationsDir, "20260403010000_create_gamma", `
CREATE TABLE IF NOT EXISTS gamma (
  id integer PRIMARY KEY
);
`, `
DROP TABLE IF EXISTS gamma;
`)

	dbPath := filepath.Join(t.TempDir(), "acl.db")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open sqlite db: %v", err)
	}
	defer db.Close()

	if _, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS alpha (id integer PRIMARY KEY);
		CREATE TABLE IF NOT EXISTS gamma (id integer PRIMARY KEY);
		CREATE TABLE IF NOT EXISTS schema_migrations (version uint64, dirty bool);
		CREATE UNIQUE INDEX IF NOT EXISTS version_unique ON schema_migrations (version);
		DELETE FROM schema_migrations;
		INSERT INTO schema_migrations (version, dirty) VALUES (20260403010000, 0);
	`); err != nil {
		t.Fatalf("seed existing schema: %v", err)
	}

	runner, err := NewRunner("sqlite", dbPath, migrationsDir)
	if err != nil {
		t.Fatalf("new runner: %v", err)
	}
	defer runner.Close()

	if err := runner.Up(0); err != nil {
		t.Fatalf("initial Up bootstrap: %v", err)
	}

	writeMigrationPair(t, migrationsDir, "20260402010000_create_beta", `
CREATE TABLE IF NOT EXISTS beta (
  id integer PRIMARY KEY
);
`, `
DROP TABLE IF EXISTS beta;
`)

	if err := runner.Up(0); err != nil {
		t.Fatalf("late-merge Up: %v", err)
	}

	var betaExists int
	if err := db.QueryRow(`
		SELECT COUNT(1)
		FROM sqlite_master
		WHERE type = 'table' AND name = 'beta'
	`).Scan(&betaExists); err != nil {
		t.Fatalf("query beta table existence: %v", err)
	}
	if betaExists != 1 {
		t.Fatalf("expected beta table to exist after late-merge migration, got count=%d", betaExists)
	}

	var historyCount int
	if err := db.QueryRow(`SELECT COUNT(1) FROM schema_migration_history`).Scan(&historyCount); err != nil {
		t.Fatalf("count history rows: %v", err)
	}
	if historyCount != 3 {
		t.Fatalf("expected 3 history rows after late merge, got %d", historyCount)
	}

	var version uint64
	var dirty bool
	if err := db.QueryRow(`SELECT version, dirty FROM schema_migrations LIMIT 1`).Scan(&version, &dirty); err != nil {
		t.Fatalf("read schema_migrations: %v", err)
	}
	if version != 20260403010000 || dirty {
		t.Fatalf("expected current version to stay at highest applied version 20260403010000 clean, got version=%d dirty=%v", version, dirty)
	}
}

func TestRunnerDownAllowsInitialMigrationToDropHistoryTable(t *testing.T) {
	migrationsDir := filepath.Join(t.TempDir(), "migrations")
	if err := os.MkdirAll(migrationsDir, 0o755); err != nil {
		t.Fatalf("mkdir migrations dir: %v", err)
	}

	writeMigrationPair(t, migrationsDir, "20260401010000_init_schema", `
CREATE TABLE IF NOT EXISTS schema_migration_history (
  version bigint NOT NULL PRIMARY KEY,
  name varchar(255) NOT NULL DEFAULT '',
  applied_at timestamp NOT NULL DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS sample_items (
  id integer PRIMARY KEY
);
`, `
DROP TABLE IF EXISTS sample_items;
DROP TABLE IF EXISTS schema_migration_history;
`)

	dbPath := filepath.Join(t.TempDir(), "acl.db")
	runner, err := NewRunner("sqlite", dbPath, migrationsDir)
	if err != nil {
		t.Fatalf("new runner: %v", err)
	}
	defer runner.Close()

	if err := runner.Up(0); err != nil {
		t.Fatalf("Up: %v", err)
	}
	if err := runner.Down(1); err != nil {
		t.Fatalf("Down: %v", err)
	}

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open sqlite db: %v", err)
	}
	defer db.Close()

	var stateCount int
	if err := db.QueryRow(`SELECT COUNT(1) FROM schema_migrations`).Scan(&stateCount); err != nil {
		t.Fatalf("count schema_migrations rows: %v", err)
	}
	if stateCount != 0 {
		t.Fatalf("expected schema_migrations to be empty after down to zero, got %d rows", stateCount)
	}
}

func copyMigrationFile(t *testing.T, dstDir, name string) {
	t.Helper()

	srcPath := filepath.Join(repoMigrationsDir(t), name)
	body, err := os.ReadFile(srcPath)
	if err != nil {
		t.Fatalf("read migration %s: %v", srcPath, err)
	}

	dstPath := filepath.Join(dstDir, name)
	if err := os.WriteFile(dstPath, body, 0o644); err != nil {
		t.Fatalf("write migration %s: %v", dstPath, err)
	}
}

func repoMigrationsDir(t *testing.T) string {
	t.Helper()

	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve current file path")
	}
	return filepath.Join(filepath.Dir(thisFile), "..", "migrations")
}

func writeMigrationPair(t *testing.T, dir, base, upSQL, downSQL string) {
	t.Helper()

	upPath := filepath.Join(dir, base+".up.sql")
	if err := os.WriteFile(upPath, []byte("-- +migrate Up\n\n"+strings.TrimSpace(upSQL)+"\n"), 0o644); err != nil {
		t.Fatalf("write up migration %s: %v", upPath, err)
	}

	downPath := filepath.Join(dir, base+".down.sql")
	if err := os.WriteFile(downPath, []byte("-- +migrate Down\n\n"+strings.TrimSpace(downSQL)+"\n"), 0o644); err != nil {
		t.Fatalf("write down migration %s: %v", downPath, err)
	}
}
