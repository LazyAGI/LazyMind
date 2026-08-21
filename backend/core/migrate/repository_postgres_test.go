package migrate

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"net/url"
	"os"
	"sort"
	"strings"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
	gormpostgres "gorm.io/driver/postgres"
	"gorm.io/gorm"
)

const migrationPostgresDSNEnv = "MIGRATION_TEST_POSTGRES_DSN"
const targetMigrationsDirEnv = "TARGET_MIGRATIONS_DIR"

// TestRepositoryPostgresMigrationPaths proves that the repository's supported
// construction and target-upgrade paths produce the same PostgreSQL schema:
//
//  1. every released aggregate in order;
//  2. released aggregates through N-1, followed by every dev migration for N.
//  3. target's released state through N-1, upgraded by branch migrations;
//  4. target's released state through N, upgraded by branch post-aggregate dev migrations.
//
// CI sets MIGRATION_TEST_POSTGRES_DSN, making this a required integration test.
// Local unit-test runs skip it when no disposable PostgreSQL server is configured.
func TestRepositoryPostgresMigrationPaths(t *testing.T) {
	adminDSN := strings.TrimSpace(os.Getenv(migrationPostgresDSNEnv))
	if adminDSN == "" {
		t.Skipf("set %s to run repository PostgreSQL migration verification", migrationPostgresDSNEnv)
	}

	runner := &Runner{dir: "../migrations"}
	catalog, err := runner.loadCatalog()
	if err != nil {
		t.Fatalf("load migration catalog: %v", err)
	}
	if len(catalog.Modes) < 2 {
		t.Fatalf("need at least two release modes, got %d", len(catalog.Modes))
	}
	current := catalog.Modes[len(catalog.Modes)-1]
	releaseDB := createTemporaryPostgresDatabase(t, adminDSN, "release")
	devDB := createTemporaryPostgresDatabase(t, adminDSN, "dev")

	if current.Aggregate == nil {
		for _, mode := range catalog.Modes[:len(catalog.Modes)-1] {
			if mode.Aggregate == nil {
				t.Fatalf("previous release %s has no aggregate migration", mode.Name)
			}
			execMigrationFile(t, releaseDB, mode.Aggregate.UpPath)
			execMigrationFile(t, devDB, mode.Aggregate.UpPath)
		}
		for _, migration := range current.Dev {
			execMigrationFile(t, devDB, migration.UpPath)
		}
		assertCredentialMigrationSchema(t, devDB)
		assertPostgresMatchesORMModels(t, devDB)
		return
	}
	baselineDB := createTemporaryPostgresDatabase(t, adminDSN, "baseline")

	for i, mode := range catalog.Modes {
		if i == len(catalog.Modes)-1 {
			break
		}
		if mode.Aggregate == nil {
			t.Fatalf("previous release %s has no aggregate migration", mode.Name)
		}
		execMigrationFile(t, baselineDB, mode.Aggregate.UpPath)
	}

	for _, mode := range catalog.Modes {
		if mode.Aggregate == nil {
			t.Fatalf("release path requires aggregate for %s", mode.Name)
		}
		execMigrationFile(t, releaseDB, mode.Aggregate.UpPath)
	}

	for i, mode := range catalog.Modes {
		if i == len(catalog.Modes)-1 {
			break
		}
		if mode.Aggregate == nil {
			t.Fatalf("previous release %s has no aggregate migration", mode.Name)
		}
		execMigrationFile(t, devDB, mode.Aggregate.UpPath)
	}
	for _, migration := range current.Dev {
		execMigrationFile(t, devDB, migration.UpPath)
	}

	releaseFingerprint := postgresSchemaFingerprint(t, releaseDB)
	devFingerprint := postgresSchemaFingerprint(t, devDB)
	if releaseFingerprint != devFingerprint {
		t.Fatalf(
			"dev and aggregate schemas differ\nrelease=%s\ndev=%s\n%s",
			releaseFingerprint,
			devFingerprint,
			postgresSchemaDiff(t, releaseDB, devDB),
		)
	}
	releaseData := postgresDataFingerprint(t, releaseDB)
	devData := postgresDataFingerprint(t, devDB)
	if releaseData != devData {
		t.Fatalf("dev and aggregate seed/data transformations differ\nrelease=%s\ndev=%s", releaseData, devData)
	}
	assertCredentialMigrationSchema(t, releaseDB)
	assertCredentialMigrationSchema(t, devDB)
	assertPostgresMatchesORMModels(t, releaseDB)
	assertPostgresMatchesORMModels(t, devDB)
	assertTargetPostgresUpgradePaths(t, adminDSN, catalog, devDB)

	execMigrationFile(t, releaseDB, current.Aggregate.DownPath)
	baselineFingerprint := postgresSchemaFingerprint(t, baselineDB)
	if got := postgresSchemaFingerprint(t, releaseDB); got != baselineFingerprint {
		t.Fatalf("current aggregate down migration does not restore previous release schema\n%s", postgresSchemaDiff(t, baselineDB, releaseDB))
	}
}

func assertTargetPostgresUpgradePaths(
	t *testing.T,
	adminDSN string,
	branchCatalog migrationCatalog,
	wantDB *sql.DB,
) {
	t.Helper()
	targetDir := strings.TrimSpace(os.Getenv(targetMigrationsDirEnv))
	if targetDir == "" {
		t.Logf("%s is unset; skipping target-branch upgrade paths", targetMigrationsDirEnv)
		return
	}
	targetRunner := &Runner{dir: targetDir}
	targetCatalog, err := targetRunner.loadCatalog()
	if err != nil {
		t.Fatalf("load target migration catalog: %v", err)
	}
	current := branchCatalog.Modes[len(branchCatalog.Modes)-1]

	t.Run("target_previous_release_to_branch_dev", func(t *testing.T) {
		db := createTemporaryPostgresDatabase(t, adminDSN, "target_previous")
		state := targetStateThroughMode(t, targetCatalog, current.ModeVersion-1)
		if len(state.schema) == 0 {
			t.Fatalf("target has no aggregate before branch %s", current.Name)
		}
		prepareTargetDatabase(t, adminDSN, db, state)
		runBranchMigrations(t, adminDSN, db)
		assertPostgresDatabaseMatches(t, "target previous release upgrade", wantDB, db)
	})

	t.Run("target_current_release_to_branch_post_aggregate_dev", func(t *testing.T) {
		var hasCurrent bool
		for _, mode := range targetCatalog.Modes {
			if mode.ModeVersion == current.ModeVersion && mode.Aggregate != nil {
				hasCurrent = true
				break
			}
		}
		if !hasCurrent {
			t.Skipf("target has no aggregate for branch %s", current.Name)
		}
		state := targetStateThroughMode(t, targetCatalog, current.ModeVersion)
		db := createTemporaryPostgresDatabase(t, adminDSN, "target_current")
		prepareTargetDatabase(t, adminDSN, db, state)
		runBranchMigrations(t, adminDSN, db)
		assertPostgresDatabaseMatches(t, "target current release upgrade", wantDB, db)
	})
}

type targetMigrationState struct {
	schema  []migrationFile
	history []migrationFile
}

func targetStateThroughMode(
	t *testing.T,
	catalog migrationCatalog,
	maxModeVersion uint64,
) targetMigrationState {
	t.Helper()
	var state targetMigrationState
	for _, mode := range catalog.Modes {
		if mode.ModeVersion > maxModeVersion {
			continue
		}
		if mode.Aggregate == nil {
			t.Fatalf("target release %s has no aggregate migration", mode.Name)
		}
		state.schema = append(state.schema, *mode.Aggregate)
		state.history = append(state.history, *mode.Aggregate)
		// Released aggregate SQL already contains the mode's complete target
		// schema. Preserve post-aggregate history without executing that SQL a
		// second time, matching databases canonicalized during development.
		state.history = append(state.history, postAggregateDevMigrations(mode)...)
	}
	return state
}

func prepareTargetDatabase(
	t *testing.T,
	adminDSN string,
	db *sql.DB,
	state targetMigrationState,
) {
	t.Helper()
	for _, migration := range state.schema {
		execMigrationFile(t, db, migration.UpPath)
	}
	runner := newPostgresRunnerForDatabase(t, adminDSN, db)
	defer runner.Close()
	if err := runner.prepare(); err != nil {
		t.Fatalf("prepare target migration history: %v", err)
	}
	tx, err := runner.db.Begin()
	if err != nil {
		t.Fatalf("begin target migration history: %v", err)
	}
	for _, migration := range state.history {
		if err := runner.insertHistory(tx, migration.Version, migration.Name); err != nil {
			_ = tx.Rollback()
			t.Fatalf("record target migration %d: %v", migration.Version, err)
		}
	}
	latest := highestMigrationVersion(state.history)
	if err := runner.writeState(tx, &latest, false); err != nil {
		_ = tx.Rollback()
		t.Fatalf("write target migration state: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit target migration history: %v", err)
	}
}

func highestMigrationVersion(migrations []migrationFile) uint64 {
	var highest uint64
	for _, migration := range migrations {
		if migration.Version > highest {
			highest = migration.Version
		}
	}
	return highest
}

func runBranchMigrations(t *testing.T, adminDSN string, db *sql.DB) {
	t.Helper()
	runner := newPostgresRunnerForDatabase(t, adminDSN, db)
	defer runner.Close()
	if err := runner.Up(0); err != nil {
		t.Fatalf("upgrade target database with branch migrations: %v", err)
	}
}

func newPostgresRunnerForDatabase(t *testing.T, adminDSN string, db *sql.DB) *Runner {
	t.Helper()
	var database string
	if err := db.QueryRow(`SELECT current_database()`).Scan(&database); err != nil {
		t.Fatalf("read temporary PostgreSQL database name: %v", err)
	}
	dsn, err := postgresDSNWithDatabase(adminDSN, database)
	if err != nil {
		t.Fatalf("build temporary PostgreSQL DSN: %v", err)
	}
	runner, err := NewRunner("postgres", dsn, "../migrations")
	if err != nil {
		t.Fatalf("create branch migration runner: %v", err)
	}
	return runner
}

func assertPostgresDatabaseMatches(t *testing.T, label string, want, got *sql.DB) {
	t.Helper()
	if postgresSchemaFingerprint(t, want) != postgresSchemaFingerprint(t, got) {
		t.Fatalf("%s schema differs\n%s", label, postgresSchemaDiff(t, want, got))
	}
	if postgresDataFingerprint(t, want) != postgresDataFingerprint(t, got) {
		t.Fatalf("%s seed/data transformations differ", label)
	}
	assertCredentialMigrationSchema(t, got)
	assertPostgresMatchesORMModels(t, got)
}

func createTemporaryPostgresDatabase(t *testing.T, adminDSN, label string) *sql.DB {
	t.Helper()
	admin, err := sql.Open("pgx", adminDSN)
	if err != nil {
		t.Fatalf("open PostgreSQL admin connection: %v", err)
	}
	if err := admin.Ping(); err != nil {
		admin.Close()
		t.Fatalf("ping PostgreSQL: %v", err)
	}
	name := fmt.Sprintf("lazymind_migration_%s_%d", label, time.Now().UnixNano())
	if _, err := admin.Exec(`CREATE DATABASE ` + quotePostgresIdentifier(name)); err != nil {
		admin.Close()
		t.Fatalf("create PostgreSQL database %s: %v", name, err)
	}
	t.Cleanup(func() {
		_, _ = admin.Exec(`DROP DATABASE IF EXISTS ` + quotePostgresIdentifier(name) + ` WITH (FORCE)`)
		_ = admin.Close()
	})

	dsn, err := postgresDSNWithDatabase(adminDSN, name)
	if err != nil {
		t.Fatalf("build database DSN: %v", err)
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatalf("open PostgreSQL database %s: %v", name, err)
	}
	if err := db.Ping(); err != nil {
		db.Close()
		t.Fatalf("ping PostgreSQL database %s: %v", name, err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func postgresDSNWithDatabase(rawDSN, database string) (string, error) {
	parsed, err := url.Parse(rawDSN)
	if err != nil {
		return "", err
	}
	parsed.Path = "/" + database
	return parsed.String(), nil
}

func quotePostgresIdentifier(value string) string {
	return `"` + strings.ReplaceAll(value, `"`, `""`) + `"`
}

func execMigrationFile(t *testing.T, db *sql.DB, path string) {
	execMigrationFileForDriver(t, db, path, "postgres")
}

func execMigrationFileForDriver(t *testing.T, db *sql.DB, path, driver string) {
	t.Helper()
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read migration %s: %v", path, err)
	}
	sqlBody, err := migrationSQLForDriver(string(body), driver)
	if err != nil {
		t.Fatalf("select %s dialect for migration %s: %v", driver, path, err)
	}
	tx, err := db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatalf("begin migration %s: %v", path, err)
	}
	if err := execMigrationSQL(tx, driver, sqlBody); err != nil {
		_ = tx.Rollback()
		t.Fatalf("execute migration %s: %v", path, err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit migration %s: %v", path, err)
	}
}

func postgresSchemaFingerprint(t *testing.T, db *sql.DB) string {
	t.Helper()
	lines := postgresSchemaLines(t, db)
	sum := sha256.Sum256([]byte(strings.Join(lines, "\n")))
	return hex.EncodeToString(sum[:])
}

func postgresDataFingerprint(t *testing.T, db *sql.DB) string {
	t.Helper()
	rows, err := db.Query(`SELECT tablename FROM pg_tables
		WHERE schemaname = 'public'
		AND tablename NOT IN ('schema_migrations', 'schema_migration_history', 'schema_migration_lock')
		ORDER BY tablename`)
	if err != nil {
		t.Fatalf("list PostgreSQL tables for data fingerprint: %v", err)
	}
	var tables []string
	for rows.Next() {
		var table string
		if err := rows.Scan(&table); err != nil {
			rows.Close()
			t.Fatalf("scan PostgreSQL table: %v", err)
		}
		tables = append(tables, table)
	}
	if err := rows.Close(); err != nil {
		t.Fatalf("close PostgreSQL table rows: %v", err)
	}

	lines := make([]string, 0, len(tables))
	for _, table := range tables {
		query := fmt.Sprintf(`SELECT COALESCE(jsonb_agg(row_data ORDER BY row_data::text)::text, '[]')
			FROM (SELECT to_jsonb(t) - ARRAY['created_at','updated_at','applied_at','last_run_at','next_run_at'] AS row_data
			FROM public.%s AS t) AS normalized`, quotePostgresIdentifier(table))
		var data string
		if err := db.QueryRow(query).Scan(&data); err != nil {
			t.Fatalf("fingerprint data in %s: %v", table, err)
		}
		lines = append(lines, table+"|"+data)
	}
	sum := sha256.Sum256([]byte(strings.Join(lines, "\n")))
	return hex.EncodeToString(sum[:])
}

func postgresSchemaLines(t *testing.T, db *sql.DB) []string {
	t.Helper()
	queries := []string{
		`SELECT 'column|' || table_name || '|' || column_name || '|' ||
		 data_type || '|' || COALESCE(udt_name, '') || '|' || is_nullable || '|' || COALESCE(column_default, '')
		 FROM information_schema.columns WHERE table_schema = 'public'
		 AND table_name NOT IN ('schema_migrations', 'schema_migration_history', 'schema_migration_lock')`,
		`SELECT 'constraint|' || c.relname || '|' || con.conname || '|' || con.contype::text || '|' ||
		 pg_get_constraintdef(con.oid, true)
		 FROM pg_constraint con JOIN pg_class c ON c.oid = con.conrelid
		 JOIN pg_namespace n ON n.oid = c.relnamespace WHERE n.nspname = 'public'
		 AND c.relname NOT IN ('schema_migrations', 'schema_migration_history', 'schema_migration_lock')`,
		`SELECT 'index|' || tablename || '|' || indexname || '|' || indexdef
		 FROM pg_indexes WHERE schemaname = 'public'
		 AND tablename NOT IN ('schema_migrations', 'schema_migration_history', 'schema_migration_lock')`,
		`SELECT 'sequence|' || sequence_name || '|' || data_type || '|' || start_value || '|' || increment
		 FROM information_schema.sequences WHERE sequence_schema = 'public'`,
		`SELECT 'view|' || table_name || '|' || view_definition
		 FROM information_schema.views WHERE table_schema = 'public'`,
	}
	var lines []string
	for _, query := range queries {
		rows, err := db.Query(query)
		if err != nil {
			t.Fatalf("query PostgreSQL schema: %v", err)
		}
		for rows.Next() {
			var line string
			if err := rows.Scan(&line); err != nil {
				rows.Close()
				t.Fatalf("scan PostgreSQL schema: %v", err)
			}
			lines = append(lines, line)
		}
		if err := rows.Close(); err != nil {
			t.Fatalf("close PostgreSQL schema rows: %v", err)
		}
	}
	sort.Strings(lines)
	return lines
}

func postgresSchemaDiff(t *testing.T, left, right *sql.DB) string {
	t.Helper()
	leftLines := postgresSchemaLines(t, left)
	rightLines := postgresSchemaLines(t, right)
	leftSet := make(map[string]struct{}, len(leftLines))
	rightSet := make(map[string]struct{}, len(rightLines))
	for _, line := range leftLines {
		leftSet[line] = struct{}{}
	}
	for _, line := range rightLines {
		rightSet[line] = struct{}{}
	}
	var onlyLeft, onlyRight []string
	for _, line := range leftLines {
		if _, ok := rightSet[line]; !ok {
			onlyLeft = append(onlyLeft, line)
		}
	}
	for _, line := range rightLines {
		if _, ok := leftSet[line]; !ok {
			onlyRight = append(onlyRight, line)
		}
	}
	return fmt.Sprintf("only release:\n%s\nonly dev:\n%s", strings.Join(onlyLeft, "\n"), strings.Join(onlyRight, "\n"))
}

func assertCredentialMigrationSchema(t *testing.T, db *sql.DB) {
	t.Helper()
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM information_schema.columns
		WHERE table_schema = 'public' AND table_name = 'user_model_provider_groups'
		AND column_name IN ('api_key_ciphertext', 'credential_version')`).Scan(&count); err != nil {
		t.Fatalf("query credential columns: %v", err)
	}
	if count != 2 {
		t.Fatalf("encrypted credential columns=%d, want 2", count)
	}
}

func assertPostgresMatchesORMModels(t *testing.T, db *sql.DB) {
	t.Helper()
	gormDB, err := gorm.Open(gormpostgres.New(gormpostgres.Config{Conn: db}), &gorm.Config{})
	if err != nil {
		t.Fatalf("open GORM PostgreSQL connection: %v", err)
	}
	assertGORMModelsMatchDatabase(t, gormDB)
}
