package migrate

import (
	"database/sql"
	"path/filepath"
	"strings"
	"testing"

	_ "github.com/glebarez/go-sqlite"
)

const (
	testPreviousVersion = uint64(20260701000000)
	testSourceVersionA  = uint64(20260702000000)
	testSourceVersionB  = uint64(20260703000000)
	testSquashVersion   = uint64(20260704000000)
)

func TestRunnerCanonicalizesSquashMigrationAndContinuesWithLaterMigration(t *testing.T) {
	dir := t.TempDir()
	writeMigrationPair(t, dir, "20260701000000_previous_release", `
CREATE TABLE previous_release (id integer PRIMARY KEY);
`, `
DROP TABLE previous_release;
`)
	writeSquashMigrationPair(t, dir, `
-- This would fail if the squash SQL were executed because both tables already exist.
CREATE TABLE source_alpha (id integer PRIMARY KEY);
CREATE TABLE source_beta (id integer PRIMARY KEY);
`, `
DROP TABLE source_beta;
DROP TABLE source_alpha;
`)
	writeMigrationPair(t, dir, "20260705000000_after_squash", `
CREATE TABLE after_squash (id integer PRIMARY KEY);
`, `
DROP TABLE after_squash;
`)

	dbPath := filepath.Join(t.TempDir(), "acl.db")
	db := openSquashTestDB(t, dbPath)
	defer db.Close()
	seedHistory(t, db, []historyRecord{
		{Version: testPreviousVersion, Name: "previous_release"},
		{Version: testSourceVersionA, Name: "source_alpha"},
		{Version: testSourceVersionB, Name: "source_beta"},
	})
	if _, err := db.Exec(`
CREATE TABLE previous_release (id integer PRIMARY KEY);
CREATE TABLE source_alpha (id integer PRIMARY KEY);
CREATE TABLE source_beta (id integer PRIMARY KEY);
`); err != nil {
		t.Fatalf("seed schema: %v", err)
	}

	runner := openSquashTestRunner(t, dbPath, dir)
	defer runner.Close()
	if err := runner.Up(0); err != nil {
		t.Fatalf("canonicalize squash migration: %v", err)
	}
	if err := runner.Up(0); err != nil {
		t.Fatalf("canonicalized squash migration should be idempotent: %v", err)
	}

	assertHistoryVersionCount(t, db, testPreviousVersion, 1)
	assertHistoryVersionCount(t, db, testSourceVersionA, 0)
	assertHistoryVersionCount(t, db, testSourceVersionB, 0)
	assertHistoryVersionCount(t, db, testSquashVersion, 1)
	assertHistoryVersionCount(t, db, 20260705000000, 1)
	assertTableExists(t, db, "after_squash", true)
	assertMigrationState(t, db, 20260705000000)
}

func TestRunnerExecutesSquashMigrationOnNewDatabase(t *testing.T) {
	dir := t.TempDir()
	writeSquashMigrationPair(t, dir, `
CREATE TABLE source_alpha (id integer PRIMARY KEY);
CREATE TABLE source_beta (id integer PRIMARY KEY);
`, `
DROP TABLE source_beta;
DROP TABLE source_alpha;
`)

	dbPath := filepath.Join(t.TempDir(), "acl.db")
	runner := openSquashTestRunner(t, dbPath, dir)
	defer runner.Close()
	if err := runner.Up(0); err != nil {
		t.Fatalf("execute squash migration: %v", err)
	}

	db := openSquashTestDB(t, dbPath)
	defer db.Close()
	assertTableExists(t, db, "source_alpha", true)
	assertTableExists(t, db, "source_beta", true)
	assertHistoryVersionCount(t, db, testSourceVersionA, 0)
	assertHistoryVersionCount(t, db, testSourceVersionB, 0)
	assertHistoryVersionCount(t, db, testSquashVersion, 1)
	assertMigrationState(t, db, testSquashVersion)
}

func TestRunnerRejectsPartiallyAppliedSquashMigration(t *testing.T) {
	dir := t.TempDir()
	writeSquashMigrationPair(t, dir, `
CREATE TABLE source_alpha (id integer PRIMARY KEY);
CREATE TABLE source_beta (id integer PRIMARY KEY);
`, `
DROP TABLE source_beta;
DROP TABLE source_alpha;
`)

	dbPath := filepath.Join(t.TempDir(), "acl.db")
	db := openSquashTestDB(t, dbPath)
	defer db.Close()
	seedHistory(t, db, []historyRecord{{Version: testSourceVersionA, Name: "source_alpha"}})
	if _, err := db.Exec(`CREATE TABLE source_alpha (id integer PRIMARY KEY)`); err != nil {
		t.Fatalf("seed source schema: %v", err)
	}

	runner := openSquashTestRunner(t, dbPath, dir)
	defer runner.Close()
	err := runner.Up(0)
	if err == nil || !strings.Contains(err.Error(), "partially applied") {
		t.Fatalf("expected partial squash error, got %v", err)
	}
	assertHistoryVersionCount(t, db, testSourceVersionA, 1)
	assertHistoryVersionCount(t, db, testSquashVersion, 0)
	assertMigrationState(t, db, testSourceVersionA)
}

func TestRunnerRejectsUnexpectedAppliedMigrationWithoutFile(t *testing.T) {
	dir := t.TempDir()
	writeSquashMigrationPair(t, dir, `
CREATE TABLE source_alpha (id integer PRIMARY KEY);
CREATE TABLE source_beta (id integer PRIMARY KEY);
`, `
DROP TABLE source_beta;
DROP TABLE source_alpha;
`)

	dbPath := filepath.Join(t.TempDir(), "acl.db")
	db := openSquashTestDB(t, dbPath)
	defer db.Close()
	seedHistory(t, db, []historyRecord{
		{Version: testSourceVersionA, Name: "source_alpha"},
		{Version: testSourceVersionB, Name: "source_beta"},
		{Version: 20260703500000, Name: "unexpected_orphan"},
	})

	runner := openSquashTestRunner(t, dbPath, dir)
	defer runner.Close()
	err := runner.Up(0)
	if err == nil || !strings.Contains(err.Error(), "has no migration file or release directory") {
		t.Fatalf("expected unexpected orphan error, got %v", err)
	}
	assertHistoryVersionCount(t, db, testSourceVersionA, 1)
	assertHistoryVersionCount(t, db, testSourceVersionB, 1)
	assertHistoryVersionCount(t, db, testSquashVersion, 0)
}

func TestRunnerRejectsSquashWhileSupersededFilesRemain(t *testing.T) {
	dir := t.TempDir()
	writeMigrationPair(t, dir, "20260702000000_source_alpha", `
CREATE TABLE source_alpha (id integer PRIMARY KEY);
`, `
DROP TABLE source_alpha;
`)
	writeMigrationPair(t, dir, "20260703000000_source_beta", `
CREATE TABLE source_beta (id integer PRIMARY KEY);
`, `
DROP TABLE source_beta;
`)
	writeSquashMigrationPair(t, dir, `
CREATE TABLE source_alpha (id integer PRIMARY KEY);
CREATE TABLE source_beta (id integer PRIMARY KEY);
`, `
DROP TABLE source_beta;
DROP TABLE source_alpha;
`)

	dbPath := filepath.Join(t.TempDir(), "acl.db")
	runner := openSquashTestRunner(t, dbPath, dir)
	defer runner.Close()
	err := runner.Up(0)
	if err == nil || !strings.Contains(err.Error(), "still has superseded migration file") {
		t.Fatalf("expected superseded file error, got %v", err)
	}

	db := openSquashTestDB(t, dbPath)
	defer db.Close()
	assertTableExists(t, db, "source_alpha", false)
}

func TestRunnerDownAfterCanonicalizedSquashUsesAggregateDownMigration(t *testing.T) {
	dir := t.TempDir()
	writeMigrationPair(t, dir, "20260701000000_previous_release", `
CREATE TABLE previous_release (id integer PRIMARY KEY);
`, `
DROP TABLE previous_release;
`)
	writeSquashMigrationPair(t, dir, `
CREATE TABLE source_alpha (id integer PRIMARY KEY);
CREATE TABLE source_beta (id integer PRIMARY KEY);
`, `
DROP TABLE source_beta;
DROP TABLE source_alpha;
`)

	dbPath := filepath.Join(t.TempDir(), "acl.db")
	db := openSquashTestDB(t, dbPath)
	defer db.Close()
	seedHistory(t, db, []historyRecord{
		{Version: testPreviousVersion, Name: "previous_release"},
		{Version: testSourceVersionA, Name: "source_alpha"},
		{Version: testSourceVersionB, Name: "source_beta"},
	})
	if _, err := db.Exec(`
CREATE TABLE previous_release (id integer PRIMARY KEY);
CREATE TABLE source_alpha (id integer PRIMARY KEY);
CREATE TABLE source_beta (id integer PRIMARY KEY);
`); err != nil {
		t.Fatalf("seed schema: %v", err)
	}

	runner := openSquashTestRunner(t, dbPath, dir)
	defer runner.Close()
	if err := runner.Up(0); err != nil {
		t.Fatalf("canonicalize squash migration: %v", err)
	}
	if err := runner.Down(1); err != nil {
		t.Fatalf("down canonicalized squash migration: %v", err)
	}

	assertTableExists(t, db, "source_alpha", false)
	assertTableExists(t, db, "source_beta", false)
	assertTableExists(t, db, "previous_release", true)
	assertHistoryVersionCount(t, db, testSquashVersion, 0)
	assertMigrationState(t, db, testPreviousVersion)
}

func TestParseSupersedesDirective(t *testing.T) {
	versions, err := parseSupersedesDirective(`
-- +migrate Up
-- +migrate Supersedes: 20260701000000, 20260702000000
SELECT 1;
`)
	if err != nil {
		t.Fatalf("parse Supersedes: %v", err)
	}
	if len(versions) != 2 || versions[0] != 20260701000000 || versions[1] != 20260702000000 {
		t.Fatalf("unexpected Supersedes versions: %v", versions)
	}
}

func writeSquashMigrationPair(t *testing.T, dir, upSQL, downSQL string) {
	t.Helper()
	directive := "-- +migrate Supersedes 20260702000000,20260703000000\n"
	writeMigrationPair(
		t,
		dir,
		"20260704000000_release_v1_2_0",
		directive+strings.TrimSpace(upSQL),
		directive+strings.TrimSpace(downSQL),
	)
}

func openSquashTestRunner(t *testing.T, dbPath, dir string) *Runner {
	t.Helper()
	runner, err := NewRunner("sqlite", dbPath, dir)
	if err != nil {
		t.Fatalf("new runner: %v", err)
	}
	return runner
}

func openSquashTestDB(t *testing.T, dbPath string) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open sqlite db: %v", err)
	}
	return db
}

func seedHistory(t *testing.T, db *sql.DB, records []historyRecord) {
	t.Helper()
	if _, err := db.Exec(`
CREATE TABLE IF NOT EXISTS schema_migrations (version uint64, dirty bool);
CREATE UNIQUE INDEX IF NOT EXISTS version_unique ON schema_migrations (version);
CREATE TABLE IF NOT EXISTS schema_migration_history (
  version bigint NOT NULL PRIMARY KEY,
  name varchar(255) NOT NULL DEFAULT '',
  applied_at timestamp NOT NULL DEFAULT CURRENT_TIMESTAMP
);
DELETE FROM schema_migrations;
DELETE FROM schema_migration_history;
`); err != nil {
		t.Fatalf("prepare migration history: %v", err)
	}
	var highest uint64
	for _, record := range records {
		if _, err := db.Exec(
			`INSERT INTO schema_migration_history (version, name) VALUES (?, ?)`,
			record.Version,
			record.Name,
		); err != nil {
			t.Fatalf("insert migration history %d: %v", record.Version, err)
		}
		if record.Version > highest {
			highest = record.Version
		}
	}
	if highest > 0 {
		if _, err := db.Exec(`INSERT INTO schema_migrations (version, dirty) VALUES (?, 0)`, highest); err != nil {
			t.Fatalf("insert migration state: %v", err)
		}
	}
}

func assertHistoryVersionCount(t *testing.T, db *sql.DB, version uint64, want int) {
	t.Helper()
	var got int
	if err := db.QueryRow(
		`SELECT COUNT(1) FROM schema_migration_history WHERE version = ?`,
		version,
	).Scan(&got); err != nil {
		t.Fatalf("count history version %d: %v", version, err)
	}
	if got != want {
		t.Fatalf("history version %d count=%d, want %d", version, got, want)
	}
}

func assertMigrationState(t *testing.T, db *sql.DB, want uint64) {
	t.Helper()
	var version uint64
	var dirty bool
	if err := db.QueryRow(`SELECT version, dirty FROM schema_migrations LIMIT 1`).Scan(&version, &dirty); err != nil {
		t.Fatalf("read migration state: %v", err)
	}
	if version != want || dirty {
		t.Fatalf("migration state version=%d dirty=%v, want version=%d dirty=false", version, dirty, want)
	}
}

func assertTableExists(t *testing.T, db *sql.DB, table string, want bool) {
	t.Helper()
	var count int
	if err := db.QueryRow(
		`SELECT COUNT(1) FROM sqlite_master WHERE type = 'table' AND name = ?`,
		table,
	).Scan(&count); err != nil {
		t.Fatalf("query table %s: %v", table, err)
	}
	if got := count == 1; got != want {
		t.Fatalf("table %s exists=%v, want %v", table, got, want)
	}
}
