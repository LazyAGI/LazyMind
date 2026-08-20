package migrate

import (
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
	if len(catalog.VersionMigrations) != 3 {
		t.Fatalf("version migration count=%d, want 3", len(catalog.VersionMigrations))
	}
	if len(catalog.Modes) != 3 {
		t.Fatalf("mode count=%d, want 3", len(catalog.Modes))
	}
	v01 := catalog.Modes[0]
	if v01.Name != "v0_1" || v01.ModeVersion != 1 ||
		v01.Aggregate == nil || v01.Aggregate.Version != 20260321131500 {
		t.Fatalf("unexpected v0_1 mode: %#v", v01)
	}
	if len(v01.Dev) != 0 {
		t.Fatalf("v0_1 dev migration count=%d, want 0", len(v01.Dev))
	}
	mode := catalog.Modes[1]
	if mode.Name != "v0_2" || mode.ModeVersion != 2 ||
		mode.Aggregate == nil || mode.Aggregate.Version != 20260723183515 {
		t.Fatalf("unexpected v0_2 mode: %#v", mode)
	}
	if len(mode.Dev) != 91 {
		t.Fatalf("v0_2 dev migration count=%d, want 91", len(mode.Dev))
	}
	if !containsMigrationFileVersion(mode.Dev, 20260703130000) {
		t.Fatal("v0_2 dev migrations are missing create_plugin_step_intents")
	}
	if !containsVersion(mode.Aggregate.Supersedes, 20260703130000) {
		t.Fatal("v0_2 aggregate Supersedes is missing create_plugin_step_intents")
	}
	if len(mode.Aggregate.Supersedes) != 88 {
		t.Fatalf(
			"v0_2 aggregate Supersedes count=%d, want frozen baseline count 88",
			len(mode.Aggregate.Supersedes),
		)
	}
	for _, version := range mode.Aggregate.Supersedes {
		if !containsMigrationFileVersion(mode.Dev, version) {
			t.Fatalf("v0_2 aggregate Supersedes references missing dev migration %d", version)
		}
	}
	for _, migration := range mode.Dev {
		wantVersion, err := combineDevVersion(2, migration.FileVersion)
		if err != nil {
			t.Fatalf("combine v0_2 dev migration %d: %v", migration.FileVersion, err)
		}
		if migration.Version != wantVersion {
			t.Fatalf(
				"v0_2 dev migration %d full version=%d, want %d",
				migration.FileVersion,
				migration.Version,
				wantVersion,
			)
		}
	}
	up, err := os.ReadFile(mode.Aggregate.UpPath)
	if err != nil {
		t.Fatalf("read v0_2 aggregate up: %v", err)
	}
	down, err := os.ReadFile(mode.Aggregate.DownPath)
	if err != nil {
		t.Fatalf("read v0_2 aggregate down: %v", err)
	}
	if !strings.Contains(string(up), "CREATE TABLE public.plugin_step_intents") ||
		!strings.Contains(string(up), "CREATE UNIQUE INDEX uk_plugin_step_intent") {
		t.Fatal("v0_2 aggregate up is missing plugin_step_intents schema")
	}
	if !strings.Contains(string(up), `ADD COLUMN "api_key_ciphertext"`) ||
		!strings.Contains(string(up), `ADD COLUMN "credential_version"`) {
		t.Fatal("v0_2 aggregate up is missing encrypted provider credential columns")
	}
	if !strings.Contains(string(down), "DROP TABLE IF EXISTS public.plugin_step_intents CASCADE") {
		t.Fatal("v0_2 aggregate down is missing plugin_step_intents rollback")
	}
	if !strings.Contains(string(down), `DROP COLUMN "api_key_ciphertext"`) ||
		!strings.Contains(string(down), `DROP COLUMN "credential_version"`) {
		t.Fatal("v0_2 aggregate down is missing encrypted provider credential rollback")
	}

	v03 := catalog.Modes[2]
	if v03.Name != "v0_3" || v03.ModeVersion != 3 ||
		v03.Aggregate == nil || v03.Aggregate.Version != 20260805000000 {
		t.Fatalf("unexpected v0_3 mode: %#v", v03)
	}
	if len(v03.Dev) != 28 {
		t.Fatalf("v0_3 dev migration count=%d, want 28", len(v03.Dev))
	}
	for _, version := range []uint64{20260730100000, 20260803120000, 20260803150000, 20260803160000, 20260803220000, 20260804090000, 20260804100000, 20260805100000, 20260805120000, 20260805121000, 20260805173000, 20260806120000, 20260806173000, 20260807120000, 20260807160000, 20260809203000, 20260810100000, 20260811120000, 20260811153000, 20260811173000, 20260813120000, 20260813140000, 20260813190000, 20260814110000, 20260814120000, 20260815160000, 20260816120000, 20260817084853} {
		if !containsMigrationFileVersion(v03.Dev, version) {
			t.Fatalf("v0_3 dev migrations are missing %d", version)
		}
	}
	v03Up, err := os.ReadFile(v03.Aggregate.UpPath)
	if err != nil {
		t.Fatalf("read v0_3 aggregate up: %v", err)
	}
	for _, token := range []string{"workflow_preparations", "workflow_outbox", "workflow_input_resources", "driver_content", "chat_executor", "external_chat_run_events", "external_chat_hosts", "lease_token", "sub_agent_tasks", "sources"} {
		if !strings.Contains(string(v03Up), token) {
			t.Fatalf("v0_3 aggregate up is missing %s", token)
		}
	}
}

func TestParseReleaseVersionUsesV0MinorVersion(t *testing.T) {
	got, err := parseReleaseVersion("v0_2")
	if err != nil {
		t.Fatalf("parse v0_2: %v", err)
	}
	if got != 2 {
		t.Fatalf("parse v0_2=%d, want 2", got)
	}

	for _, release := range []string{"v1", "v2", "v0_0", "v0_02", "v0_"} {
		if _, err := parseReleaseVersion(release); err == nil {
			t.Fatalf("parseReleaseVersion(%q) succeeded, want error", release)
		}
	}
}

func TestVersionModeRejectsMultipleAggregateMigrations(t *testing.T) {
	dir := newStructuredMigrationDir(t)
	versionDir := versionModeDir(t, dir, "v0_1")
	writeMigrationPair(t, versionDir, "20260725093000_first", "SELECT 1;", "SELECT 1;")
	writeMigrationPair(t, versionDir, "20260801110000_second", "SELECT 1;", "SELECT 1;")

	runner := &Runner{dir: dir}
	_, err := runner.loadCatalog()
	if err == nil || !strings.Contains(err.Error(), "must contain exactly one aggregate migration") {
		t.Fatalf("expected multiple aggregate migrations error, got %v", err)
	}
}

func TestVersionModeRejectsMigrationOutsideReleaseDirectory(t *testing.T) {
	dir := newStructuredMigrationDir(t)
	writeMigrationPair(
		t,
		filepath.Join(dir, versionModeDirName),
		"20260802120000_release",
		"SELECT 1;",
		"SELECT 1;",
	)

	runner := &Runner{dir: dir}
	_, err := runner.loadCatalog()
	if err == nil || !strings.Contains(err.Error(), "must contain v0_N directories") {
		t.Fatalf("expected release directory error, got %v", err)
	}
}

func TestRunnerPrefersDevForUntouchedMode(t *testing.T) {
	dir := newStructuredMigrationDir(t)
	writeMigrationPair(t, versionModeDir(t, dir, "v0_1"), "20260802120000_release", `
CREATE TABLE users (id integer PRIMARY KEY, source text NOT NULL);
INSERT INTO users (id, source) VALUES (1, 'aggregate');
`, `
DROP TABLE users;
`)
	writeMigrationPair(t, devModeDir(t, dir, "v0_1"), "20260725093000_create_users", `
CREATE TABLE users (id integer PRIMARY KEY, source text NOT NULL);
INSERT INTO users (id, source) VALUES (1, 'dev');
`, `
DROP TABLE users;
`)

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
	assertHistoryVersionCount(t, db, testAggregateV1, 0)
	assertHistoryVersionCount(t, db, devVersion, 1)
	var source string
	if err := db.QueryRow(`SELECT source FROM users WHERE id = 1`).Scan(&source); err != nil {
		t.Fatalf("read users: %v", err)
	}
	if source != "dev" {
		t.Fatalf("source=%q, want dev", source)
	}
}

func TestRunnerRepairsMixedAggregateHistoryAndAppliesPostAggregateDev(t *testing.T) {
	dir := newStructuredMigrationDir(t)
	writeMigrationPair(t, versionModeDir(t, dir, "v0_1"), "20260802120000_release", `
-- +migrate Supersedes: 20260725093000
CREATE TABLE users (id integer PRIMARY KEY);
`, `
DROP TABLE users;
`)
	devDir := devModeDir(t, dir, "v0_1")
	writeMigrationPair(t, devDir, "20260725093000_create_users", `
CREATE TABLE users (id integer PRIMARY KEY);
`, `
DROP TABLE users;
`)
	writeMigrationPair(t, devDir, "20260803110000_add_users_name", `
ALTER TABLE users ADD COLUMN name text NOT NULL DEFAULT '';
`, `
ALTER TABLE users DROP COLUMN name;
`)

	dbPath := filepath.Join(t.TempDir(), "acl.db")
	db := openSquashTestDB(t, dbPath)
	defer db.Close()
	seedHistory(t, db, []historyRecord{
		{Version: testAggregateV1, Name: "release"},
		{Version: testDevV1A, Name: "create_users"},
	})
	if _, err := db.Exec(`CREATE TABLE users (id integer PRIMARY KEY)`); err != nil {
		t.Fatalf("seed aggregate schema: %v", err)
	}

	runner := openSquashTestRunner(t, dbPath, dir)
	defer runner.Close()
	if err := runner.Up(0); err != nil {
		t.Fatalf("repair mixed history and apply post-aggregate dev: %v", err)
	}

	postVersion, err := combineDevVersion(1, 20260803110000)
	if err != nil {
		t.Fatal(err)
	}
	assertHistoryVersionCount(t, db, testAggregateV1, 1)
	assertHistoryVersionCount(t, db, testDevV1A, 0)
	assertHistoryVersionCount(t, db, postVersion, 1)
	var columnCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('users') WHERE name = 'name'`).Scan(&columnCount); err != nil {
		t.Fatalf("query users.name: %v", err)
	}
	if columnCount != 1 {
		t.Fatalf("users.name column count=%d, want 1", columnCount)
	}
}

func TestRunnerExecutesVersionModesInReleaseOrder(t *testing.T) {
	dir := newStructuredMigrationDir(t)
	writeMigrationPair(t, versionModeDir(t, dir, "v0_1"), "20260101000000_release_one", `
CREATE TABLE migration_order (sequence integer PRIMARY KEY);
INSERT INTO migration_order (sequence) VALUES (1);
`, `
DROP TABLE migration_order;
`)
	writeMigrationPair(t, versionModeDir(t, dir, "v0_2"), "20260301000000_release_two", `
INSERT INTO migration_order (sequence) VALUES (2);
`, `
DELETE FROM migration_order WHERE sequence = 2;
`)

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
	if order != "1,2" {
		t.Fatalf("migration order=%q, want 1,2", order)
	}
}

func TestRunnerContinuesPartialDevModeAndKeepsHistory(t *testing.T) {
	dir := newStructuredMigrationDir(t)
	writeMigrationPair(t, versionModeDir(t, dir, "v0_1"), "20260802120000_release", `
CREATE TABLE users (id integer PRIMARY KEY);
CREATE INDEX idx_users_id ON users(id);
`, `
DROP TABLE users;
`)
	devDir := devModeDir(t, dir, "v0_1")
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
	seedHistory(t, db, []historyRecord{{Version: firstFullVersion, Name: "v0_1/create_users"}})
	if _, err := db.Exec(`CREATE TABLE users (id integer PRIMARY KEY)`); err != nil {
		t.Fatalf("seed dev schema: %v", err)
	}

	runner := openSquashTestRunner(t, dbPath, dir)
	defer runner.Close()
	if err := runner.Up(0); err != nil {
		t.Fatalf("continue partial dev mode: %v", err)
	}
	if err := runner.Up(0); err != nil {
		t.Fatalf("repeat completed dev mode: %v", err)
	}

	assertHistoryVersionCount(t, db, firstFullVersion, 1)
	assertHistoryVersionCount(t, db, secondFullVersion, 1)
	assertHistoryVersionCount(t, db, testAggregateV1, 0)
	assertMigrationState(t, db, secondFullVersion)
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

func TestRunnerRecognizesCompleteArchivedDevHistory(t *testing.T) {
	dir := newStructuredMigrationDir(t)
	versionDir := versionModeDir(t, dir, "v0_1")
	writeMigrationPair(t, versionDir, "20260802120000_release", `
CREATE TABLE users (id integer PRIMARY KEY);
CREATE INDEX idx_users_id ON users(id);
`, `
DROP TABLE users;
`)
	devDir := devModeDir(t, dir, "v0_1")
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

	dbPath := filepath.Join(t.TempDir(), "acl.db")
	runner := openSquashTestRunner(t, dbPath, dir)
	if err := runner.Up(0); err != nil {
		t.Fatalf("apply dev mode: %v", err)
	}
	runner.Close()

	removeMigrationPair(t, devDir, "20260725093000_create_users")
	removeMigrationPair(t, devDir, "20260801110000_add_user_index")
	if err := os.Remove(devDir); err != nil {
		t.Fatalf("archive dev directory: %v", err)
	}
	runner = openSquashTestRunner(t, dbPath, dir)
	defer runner.Close()
	if err := runner.Up(0); err != nil {
		t.Fatalf("recognize complete archived dev history: %v", err)
	}

	db := openSquashTestDB(t, dbPath)
	defer db.Close()
	firstFullVersion, err := combineDevVersion(1, testDevV1A)
	if err != nil {
		t.Fatal(err)
	}
	secondFullVersion, err := combineDevVersion(1, testDevV1B)
	if err != nil {
		t.Fatal(err)
	}
	assertHistoryVersionCount(t, db, firstFullVersion, 1)
	assertHistoryVersionCount(t, db, secondFullVersion, 1)
	assertHistoryVersionCount(t, db, testAggregateV1, 0)
	assertTableExists(t, db, "users", true)
}

func TestRunnerTreatsAnyArchivedDevHistoryAsCompletedDevPath(t *testing.T) {
	dir := newStructuredMigrationDir(t)
	writeMigrationPair(t, versionModeDir(t, dir, "v0_1"), "20260802120000_release", `
CREATE TABLE aggregate_should_not_run (id integer PRIMARY KEY);
`, `
DROP TABLE aggregate_should_not_run;
`)

	firstFullVersion, err := combineDevVersion(1, testDevV1A)
	if err != nil {
		t.Fatal(err)
	}
	dbPath := filepath.Join(t.TempDir(), "acl.db")
	db := openSquashTestDB(t, dbPath)
	defer db.Close()
	seedHistory(t, db, []historyRecord{{Version: firstFullVersion, Name: "v0_1/create_users"}})

	runner := openSquashTestRunner(t, dbPath, dir)
	defer runner.Close()
	if err := runner.Up(0); err != nil {
		t.Fatalf("recognize archived dev path: %v", err)
	}
	assertHistoryVersionCount(t, db, firstFullVersion, 1)
	assertHistoryVersionCount(t, db, testAggregateV1, 0)
	assertTableExists(t, db, "aggregate_should_not_run", false)
}

func TestRunnerSkipsOlderDevAfterAggregate(t *testing.T) {
	dir := newStructuredMigrationDir(t)
	writeMigrationPair(t, versionModeDir(t, dir, "v0_1"), "20260802120000_release", `
-- +migrate Supersedes: 20260725093000
CREATE TABLE users (id integer PRIMARY KEY);
`, `
DROP TABLE users;
`)
	devDir := devModeDir(t, dir, "v0_1")
	writeMigrationPair(t, devDir, "20260725093000_create_users", `
CREATE TABLE users (id integer PRIMARY KEY);
`, `
DROP TABLE users;
`)
	const lateMergedVersion = uint64(20260710120000)
	writeMigrationPair(t, devDir, "20260710120000_create_late_project", `
CREATE TABLE late_project (id integer PRIMARY KEY);
`, `
DROP TABLE late_project;
`)

	dbPath := filepath.Join(t.TempDir(), "acl.db")
	db := openSquashTestDB(t, dbPath)
	defer db.Close()
	seedHistory(t, db, []historyRecord{{Version: testAggregateV1, Name: "release"}})
	if _, err := db.Exec(`CREATE TABLE users (id integer PRIMARY KEY)`); err != nil {
		t.Fatalf("seed aggregate schema: %v", err)
	}

	runner := openSquashTestRunner(t, dbPath, dir)
	defer runner.Close()
	if err := runner.Up(0); err != nil {
		t.Fatalf("skip older dev covered by aggregate cutoff: %v", err)
	}

	coveredVersion, err := combineDevVersion(1, testDevV1A)
	if err != nil {
		t.Fatal(err)
	}
	lateFullVersion, err := combineDevVersion(1, lateMergedVersion)
	if err != nil {
		t.Fatal(err)
	}
	assertHistoryVersionCount(t, db, testAggregateV1, 1)
	assertHistoryVersionCount(t, db, coveredVersion, 0)
	assertHistoryVersionCount(t, db, lateFullVersion, 0)
	assertTableExists(t, db, "late_project", false)
}

func TestRunnerAllowsDifferentModesToUseDifferentSources(t *testing.T) {
	dir := newStructuredMigrationDir(t)
	writeMigrationPair(t, versionModeDir(t, dir, "v0_1"), "20260802120000_release", `
CREATE TABLE users (id integer PRIMARY KEY);
`, `
DROP TABLE users;
`)
	writeMigrationPair(t, devModeDir(t, dir, "v0_2"), "20260915100000_create_projects", `
CREATE TABLE projects (id integer PRIMARY KEY);
`, `
DROP TABLE projects;
`)

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
	writeMigrationPair(t, versionModeDir(t, dir, "v0_1"), "20260802120000_release", `
CREATE TABLE aggregate_should_not_run (id integer PRIMARY KEY);
`, `
DROP TABLE aggregate_should_not_run;
`)
	devModeDir(t, dir, "v0_1")

	deletedFullVersion, err := combineDevVersion(1, testDevV1A)
	if err != nil {
		t.Fatal(err)
	}
	dbPath := filepath.Join(t.TempDir(), "acl.db")
	db := openSquashTestDB(t, dbPath)
	defer db.Close()
	seedHistory(t, db, []historyRecord{{
		Version: deletedFullVersion,
		Name:    "v0_1/deleted_migration",
	}})

	runner := openSquashTestRunner(t, dbPath, dir)
	defer runner.Close()
	err = runner.Up(0)
	if err == nil || !strings.Contains(err.Error(), "has no migration file or release directory") {
		t.Fatalf("expected deleted dev migration error, got %v", err)
	}
	assertTableExists(t, db, "aggregate_should_not_run", false)
	assertHistoryVersionCount(t, db, deletedFullVersion, 1)
	assertHistoryVersionCount(t, db, testAggregateV1, 0)
}

func TestRunnerRejectsMixedAggregateAndDevHistoryForSameMode(t *testing.T) {
	dir := newStructuredMigrationDir(t)
	writeMigrationPair(t, versionModeDir(t, dir, "v0_1"), "20260802120000_release", `
SELECT 1;
`, `
SELECT 1;
`)
	writeMigrationPair(t, devModeDir(t, dir, "v0_1"), "20260725093000_create_users", `
SELECT 1;
`, `
SELECT 1;
`)

	devVersion, err := combineDevVersion(1, testDevV1A)
	if err != nil {
		t.Fatal(err)
	}
	dbPath := filepath.Join(t.TempDir(), "acl.db")
	db := openSquashTestDB(t, dbPath)
	defer db.Close()
	seedHistory(t, db, []historyRecord{
		{Version: testAggregateV1, Name: "release"},
		{Version: devVersion, Name: "v0_1/create_users"},
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

func versionModeDir(t *testing.T, root, release string) string {
	t.Helper()
	dir := filepath.Join(root, versionModeDirName, release)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir version mode %s: %v", release, err)
	}
	return dir
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
