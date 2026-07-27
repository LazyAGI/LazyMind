package migrate

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const (
	testAggregateV1 = uint64(20260802120000)
	testDevV1A      = uint64(20260725093000)
	testDevV1B      = uint64(20260801110000)
	testDevV2A      = uint64(20260915100000)
)

func TestRepositoryStructuredMigrationCatalogLoads(t *testing.T) {
	runner := &Runner{dir: filepath.Join("..", "migrations")}
	catalog, err := runner.loadCatalog()
	if err != nil {
		t.Fatalf("load repository migration catalog: %v", err)
	}
	if len(catalog.VersionMigrations) != 2 {
		t.Fatalf("version migration count=%d, want 2", len(catalog.VersionMigrations))
	}
	if len(catalog.Modes) != 1 {
		t.Fatalf("mode count=%d, want 1", len(catalog.Modes))
	}
	mode := catalog.Modes[0]
	if mode.Name != "v1" || mode.Aggregate == nil || mode.Aggregate.Version != 20260723183515 {
		t.Fatalf("unexpected v1 aggregate: %#v", mode.Aggregate)
	}
	if len(mode.Dev) != 88 {
		t.Fatalf("v1 dev migration count=%d, want 88", len(mode.Dev))
	}
	if !containsMigrationFileVersion(mode.Dev, 20260703130000) {
		t.Fatal("v1 dev migrations are missing create_plugin_step_intents")
	}
	if !containsVersion(mode.Aggregate.Supersedes, 20260703130000) {
		t.Fatal("v1 aggregate Supersedes is missing create_plugin_step_intents")
	}
	if len(mode.Aggregate.Supersedes) != len(mode.Dev) {
		t.Fatalf(
			"v1 aggregate Supersedes count=%d, dev migration count=%d",
			len(mode.Aggregate.Supersedes),
			len(mode.Dev),
		)
	}
	for _, migration := range mode.Dev {
		if !containsVersion(mode.Aggregate.Supersedes, migration.FileVersion) {
			t.Fatalf("v1 aggregate Supersedes is missing dev migration %d", migration.FileVersion)
		}
	}
	up, err := os.ReadFile(mode.Aggregate.UpPath)
	if err != nil {
		t.Fatalf("read v1 aggregate up: %v", err)
	}
	down, err := os.ReadFile(mode.Aggregate.DownPath)
	if err != nil {
		t.Fatalf("read v1 aggregate down: %v", err)
	}
	if !strings.Contains(string(up), "CREATE TABLE public.plugin_step_intents") ||
		!strings.Contains(string(up), "CREATE UNIQUE INDEX uk_plugin_step_intent") {
		t.Fatal("v1 aggregate up is missing plugin_step_intents schema")
	}
	if !strings.Contains(string(down), "DROP TABLE IF EXISTS public.plugin_step_intents CASCADE") {
		t.Fatal("v1 aggregate down is missing plugin_step_intents rollback")
	}
}

func TestVersionMappingRejectsDuplicatedDevMigrationIDs(t *testing.T) {
	dir := newStructuredMigrationDir(t)
	body := `{
  "schema_version": 1,
  "versions": {
    "v1": {
      "dev_migration_ids": [20260725093000]
    }
  }
}`
	if err := os.WriteFile(filepath.Join(dir, versionMappingFileName), []byte(body), 0o644); err != nil {
		t.Fatalf("write version mapping: %v", err)
	}

	runner := &Runner{dir: dir}
	_, err := runner.loadCatalog()
	if err == nil || !strings.Contains(err.Error(), `unknown field "dev_migration_ids"`) {
		t.Fatalf("expected unknown dev_migration_ids error, got %v", err)
	}
}

func TestRunnerUsesAggregateForUntouchedMode(t *testing.T) {
	dir := newStructuredMigrationDir(t)
	writeMigrationPair(t, filepath.Join(dir, versionModeDirName), "20260802120000_release", `
CREATE TABLE users (id integer PRIMARY KEY, source text NOT NULL);
INSERT INTO users (id, source) VALUES (1, 'aggregate');
`, `
DROP TABLE users;
`)
	writeMigrationPair(t, devModeDir(t, dir, "v1"), "20260725093000_create_users", `
CREATE TABLE users (id integer PRIMARY KEY, source text NOT NULL);
`, `
DROP TABLE users;
`)
	writeVersionMapping(t, dir, `"v1": {"version_migration_id": 20260802120000}`)

	dbPath := filepath.Join(t.TempDir(), "acl.db")
	runner := openSquashTestRunner(t, dbPath, dir)
	defer runner.Close()
	if err := runner.Up(0); err != nil {
		t.Fatalf("up: %v", err)
	}

	db := openSquashTestDB(t, dbPath)
	defer db.Close()
	devVersion, err := combineDevVersion(1, testDevV1A)
	if err != nil {
		t.Fatal(err)
	}
	assertHistoryVersionCount(t, db, testAggregateV1, 1)
	assertHistoryVersionCount(t, db, devVersion, 0)
	var source string
	if err := db.QueryRow(`SELECT source FROM users WHERE id = 1`).Scan(&source); err != nil {
		t.Fatalf("read users: %v", err)
	}
	if source != "aggregate" {
		t.Fatalf("source=%q, want aggregate", source)
	}
}

func TestRunnerInterleavesUnmappedVersionMigrationsWithMappedModes(t *testing.T) {
	dir := newStructuredMigrationDir(t)
	versionDir := filepath.Join(dir, versionModeDirName)
	writeMigrationPair(t, versionDir, "20260101000000_release_one", `
CREATE TABLE migration_order (sequence integer PRIMARY KEY);
INSERT INTO migration_order (sequence) VALUES (1);
`, `
DROP TABLE migration_order;
`)
	writeMigrationPair(t, versionDir, "20260201000000_legacy_bridge", `
INSERT INTO migration_order (sequence) VALUES (2);
`, `
DELETE FROM migration_order WHERE sequence = 2;
`)
	writeMigrationPair(t, versionDir, "20260301000000_release_two", `
INSERT INTO migration_order (sequence) VALUES (3);
`, `
DELETE FROM migration_order WHERE sequence = 3;
`)
	writeVersionMapping(t, dir, strings.Join([]string{
		`"v1": {"version_migration_id": 20260101000000}`,
		`"v2": {"version_migration_id": 20260301000000}`,
	}, ","))

	dbPath := filepath.Join(t.TempDir(), "acl.db")
	runner := openSquashTestRunner(t, dbPath, dir)
	defer runner.Close()
	if err := runner.Up(0); err != nil {
		t.Fatalf("up: %v", err)
	}

	db := openSquashTestDB(t, dbPath)
	defer db.Close()
	var order string
	if err := db.QueryRow(`
SELECT GROUP_CONCAT(sequence, ',') FROM (SELECT sequence FROM migration_order ORDER BY sequence)
`).Scan(&order); err != nil {
		t.Fatalf("read migration order: %v", err)
	}
	if order != "1,2,3" {
		t.Fatalf("migration order=%q, want 1,2,3", order)
	}
}

func TestRunnerContinuesPartialDevModeThenCanonicalizesHistory(t *testing.T) {
	dir := newStructuredMigrationDir(t)
	writeMigrationPair(t, filepath.Join(dir, versionModeDirName), "20260802120000_release", `
CREATE TABLE users (id integer PRIMARY KEY);
CREATE INDEX idx_users_id ON users(id);
`, `
DROP TABLE users;
`)
	devDir := devModeDir(t, dir, "v1")
	writeMigrationPair(t, devDir, "20260725093000_create_users", `
CREATE TABLE users (id integer PRIMARY KEY);
`, `
DROP TABLE users;
`)
	writeMigrationPair(t, devDir, "20260801110000_add_user_index", `
CREATE INDEX idx_users_id ON users(id);
`, `
DROP INDEX idx_users_id;
`)
	writeVersionMapping(t, dir, `"v1": {"version_migration_id": 20260802120000}`)

	firstFullVersion, err := combineDevVersion(1, testDevV1A)
	if err != nil {
		t.Fatal(err)
	}
	secondFullVersion, err := combineDevVersion(1, testDevV1B)
	if err != nil {
		t.Fatal(err)
	}
	dbPath := filepath.Join(t.TempDir(), "acl.db")
	db := openSquashTestDB(t, dbPath)
	defer db.Close()
	seedHistory(t, db, []historyRecord{{Version: firstFullVersion, Name: "v1/create_users"}})
	if _, err := db.Exec(`CREATE TABLE users (id integer PRIMARY KEY)`); err != nil {
		t.Fatalf("seed dev schema: %v", err)
	}

	runner := openSquashTestRunner(t, dbPath, dir)
	defer runner.Close()
	if err := runner.Up(0); err != nil {
		t.Fatalf("continue partial dev mode: %v", err)
	}
	removeMigrationPair(t, devDir, "20260725093000_create_users")
	removeMigrationPair(t, devDir, "20260801110000_add_user_index")
	if err := runner.Up(0); err != nil {
		t.Fatalf("up after canonicalized dev files are deleted: %v", err)
	}

	assertHistoryVersionCount(t, db, firstFullVersion, 0)
	assertHistoryVersionCount(t, db, secondFullVersion, 0)
	assertHistoryVersionCount(t, db, testAggregateV1, 1)
	assertMigrationState(t, db, testAggregateV1)
	var indexCount int
	if err := db.QueryRow(`
SELECT COUNT(1) FROM sqlite_master WHERE type = 'index' AND name = 'idx_users_id'
`).Scan(&indexCount); err != nil {
		t.Fatalf("query index: %v", err)
	}
	if indexCount != 1 {
		t.Fatalf("index count=%d, want 1", indexCount)
	}
}

func TestRunnerAllowsDifferentModesToUseDifferentSources(t *testing.T) {
	dir := newStructuredMigrationDir(t)
	writeMigrationPair(t, filepath.Join(dir, versionModeDirName), "20260802120000_release", `
CREATE TABLE users (id integer PRIMARY KEY);
`, `
DROP TABLE users;
`)
	writeMigrationPair(t, devModeDir(t, dir, "v2"), "20260915100000_create_projects", `
CREATE TABLE projects (id integer PRIMARY KEY);
`, `
DROP TABLE projects;
`)
	writeVersionMapping(t, dir, strings.Join([]string{
		`"v1": {"version_migration_id": 20260802120000}`,
		`"v2": {}`,
	}, ","))

	dbPath := filepath.Join(t.TempDir(), "acl.db")
	runner := openSquashTestRunner(t, dbPath, dir)
	defer runner.Close()
	if err := runner.Up(0); err != nil {
		t.Fatalf("up: %v", err)
	}

	db := openSquashTestDB(t, dbPath)
	defer db.Close()
	devVersion, err := combineDevVersion(2, testDevV2A)
	if err != nil {
		t.Fatal(err)
	}
	assertHistoryVersionCount(t, db, testAggregateV1, 1)
	assertHistoryVersionCount(t, db, devVersion, 1)
	assertTableExists(t, db, "users", true)
	assertTableExists(t, db, "projects", true)
	assertMigrationState(t, db, devVersion)

	if err := runner.Down(1); err != nil {
		t.Fatalf("down latest dev mode: %v", err)
	}
	assertTableExists(t, db, "users", true)
	assertTableExists(t, db, "projects", false)
	assertHistoryVersionCount(t, db, testAggregateV1, 1)
	assertHistoryVersionCount(t, db, devVersion, 0)
	assertMigrationState(t, db, testAggregateV1)
}

func TestRunnerRejectsDeletedPartialDevMigrationBeforeAggregateSQL(t *testing.T) {
	dir := newStructuredMigrationDir(t)
	writeMigrationPair(t, filepath.Join(dir, versionModeDirName), "20260802120000_release", `
CREATE TABLE aggregate_should_not_run (id integer PRIMARY KEY);
`, `
DROP TABLE aggregate_should_not_run;
`)
	devModeDir(t, dir, "v1")
	writeVersionMapping(t, dir, `"v1": {"version_migration_id": 20260802120000}`)

	deletedFullVersion, err := combineDevVersion(1, testDevV1A)
	if err != nil {
		t.Fatal(err)
	}
	dbPath := filepath.Join(t.TempDir(), "acl.db")
	db := openSquashTestDB(t, dbPath)
	defer db.Close()
	seedHistory(t, db, []historyRecord{{
		Version: deletedFullVersion,
		Name:    "v1/deleted_migration",
	}})

	runner := openSquashTestRunner(t, dbPath, dir)
	defer runner.Close()
	err = runner.Up(0)
	if err == nil || !strings.Contains(err.Error(), "has no migration file or version mapping") {
		t.Fatalf("expected deleted dev migration error, got %v", err)
	}
	assertTableExists(t, db, "aggregate_should_not_run", false)
	assertHistoryVersionCount(t, db, deletedFullVersion, 1)
	assertHistoryVersionCount(t, db, testAggregateV1, 0)
}

func TestRunnerRejectsMixedAggregateAndDevHistoryForSameMode(t *testing.T) {
	dir := newStructuredMigrationDir(t)
	writeMigrationPair(t, filepath.Join(dir, versionModeDirName), "20260802120000_release", `
SELECT 1;
`, `
SELECT 1;
`)
	writeMigrationPair(t, devModeDir(t, dir, "v1"), "20260725093000_create_users", `
SELECT 1;
`, `
SELECT 1;
`)
	writeVersionMapping(t, dir, `"v1": {"version_migration_id": 20260802120000}`)

	devVersion, err := combineDevVersion(1, testDevV1A)
	if err != nil {
		t.Fatal(err)
	}
	dbPath := filepath.Join(t.TempDir(), "acl.db")
	db := openSquashTestDB(t, dbPath)
	defer db.Close()
	seedHistory(t, db, []historyRecord{
		{Version: testAggregateV1, Name: "release"},
		{Version: devVersion, Name: "v1/create_users"},
	})

	runner := openSquashTestRunner(t, dbPath, dir)
	defer runner.Close()
	err = runner.Up(0)
	if err == nil || !strings.Contains(err.Error(), "both aggregate version") {
		t.Fatalf("expected mixed mode history error, got %v", err)
	}
}

func newStructuredMigrationDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, versionModeDirName), 0o755); err != nil {
		t.Fatalf("mkdir version_mode: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(dir, devModeDirName), 0o755); err != nil {
		t.Fatalf("mkdir dev_mode: %v", err)
	}
	return dir
}

func devModeDir(t *testing.T, root, release string) string {
	t.Helper()
	dir := filepath.Join(root, devModeDirName, release)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir dev mode %s: %v", release, err)
	}
	return dir
}

func writeVersionMapping(t *testing.T, dir, versions string) {
	t.Helper()
	body := fmt.Sprintf("{\n  \"schema_version\": 1,\n  \"versions\": {%s}\n}\n", versions)
	if err := os.WriteFile(filepath.Join(dir, versionMappingFileName), []byte(body), 0o644); err != nil {
		t.Fatalf("write version mapping: %v", err)
	}
}

func removeMigrationPair(t *testing.T, dir, base string) {
	t.Helper()
	for _, suffix := range []string{".up.sql", ".down.sql"} {
		if err := os.Remove(filepath.Join(dir, base+suffix)); err != nil {
			t.Fatalf("remove migration %s%s: %v", base, suffix, err)
		}
	}
}

func containsMigrationFileVersion(migrations []migrationFile, version uint64) bool {
	for _, migration := range migrations {
		if migration.FileVersion == version {
			return true
		}
	}
	return false
}

func containsVersion(versions []uint64, target uint64) bool {
	for _, version := range versions {
		if version == target {
			return true
		}
	}
	return false
}
