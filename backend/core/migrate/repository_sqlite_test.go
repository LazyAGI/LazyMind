package migrate

import (
	"database/sql"
	"path/filepath"
	"strings"
	"testing"

	_ "github.com/glebarez/go-sqlite"
	"gorm.io/gorm"

	"lazymind/core/common/orm"
	"lazymind/core/modelprovider"
)

func TestRepositorySQLiteReleaseAndDevPathsMatch(t *testing.T) {
	catalogRunner := &Runner{dir: "../migrations"}
	catalog, err := catalogRunner.loadCatalog()
	if err != nil {
		t.Fatalf("load migration catalog: %v", err)
	}
	if len(catalog.Modes) < 2 || catalog.Modes[0].Aggregate == nil || catalog.Modes[1].Aggregate == nil {
		t.Fatal("SQLite path test requires v0.1 and v0.2 aggregates")
	}

	releaseDB := openRawSQLite(t, t.TempDir()+"/release.db")
	devDB := openRawSQLite(t, t.TempDir()+"/dev.db")
	for _, db := range []*sql.DB{releaseDB, devDB} {
		execMigrationFileForDriver(t, db, catalog.Modes[0].Aggregate.UpPath, "sqlite")
		if _, err := db.Exec(`INSERT INTO default_models
(id,default_model_provider_id,provider_name,name,model_type,base_url,created_at,updated_at)
VALUES ('legacy-model','provider','Provider','Legacy','VLM','',CURRENT_TIMESTAMP,CURRENT_TIMESTAMP)`); err != nil {
			t.Fatalf("seed v0.1 SQLite data: %v", err)
		}
	}
	execMigrationFileForDriver(t, releaseDB, catalog.Modes[1].Aggregate.UpPath, "sqlite")
	for _, migration := range catalog.Modes[1].Dev {
		execMigrationFileForDriver(t, devDB, migration.UpPath, "sqlite")
	}

	if release, dev := sqliteSchemaFingerprint(t, releaseDB), sqliteSchemaFingerprint(t, devDB); release != dev {
		t.Fatalf("SQLite aggregate and dev schemas differ\nrelease:\n%s\ndev:\n%s", release, dev)
	}
	for label, db := range map[string]*sql.DB{"release": releaseDB, "dev": devDB} {
		var modelType string
		if err := db.QueryRow(`SELECT model_type FROM default_models WHERE id='legacy-model'`).Scan(&modelType); err != nil {
			t.Fatalf("read %s transformed model: %v", label, err)
		}
		if modelType != "vlm" {
			t.Fatalf("%s model_type=%q, want vlm", label, modelType)
		}
		var shards int
		if err := db.QueryRow(`SELECT COUNT(*) FROM eval_set_shards WHERE id='eval_shard_0001'`).Scan(&shards); err != nil || shards != 1 {
			t.Fatalf("%s eval shard seed count=%d err=%v", label, shards, err)
		}
	}
}

// TestRepositorySQLiteFreshAndUpgradePaths verifies that both fresh and legacy
// Desktop databases reach the current schema exclusively through migrations.
func TestRepositorySQLiteFreshAndUpgradePaths(t *testing.T) {
	t.Run("fresh database matches all ORM models", func(t *testing.T) {
		dsn := t.TempDir() + "/core.db"
		runRepositorySQLiteMigrations(t, dsn)
		db := openRepositorySQLite(t, dsn)
		assertGORMModelsMatchDatabase(t, db)
		assertSQLiteRepairIndexes(t, db)
		assertSQLiteCredentialColumns(t, db)
	})

	t.Run("legacy database upgrades data and is idempotent", func(t *testing.T) {
		t.Setenv("LAZYMIND_MODEL_PROVIDER_SECRET_KEY", "sqlite-device-derived-test-key")
		dsn := t.TempDir() + "/legacy.db"
		raw, err := sql.Open("sqlite", dsn)
		if err != nil {
			t.Fatalf("open legacy SQLite database: %v", err)
		}
		_, err = raw.Exec(`
CREATE TABLE user_model_provider_groups (
  id text PRIMARY KEY,
  user_model_provider_id text NOT NULL,
  name text NOT NULL,
  base_url text NOT NULL,
  api_key text NOT NULL,
  is_verified boolean NOT NULL DEFAULT false,
  create_user_id text NOT NULL,
  create_user_name text NOT NULL,
  created_at datetime NOT NULL,
  updated_at datetime NOT NULL,
  deleted_at datetime
);
INSERT INTO user_model_provider_groups (
  id, user_model_provider_id, name, base_url, api_key, is_verified,
  create_user_id, create_user_name, created_at, updated_at
) VALUES (
  'legacy-group', 'legacy-provider', 'default', 'https://example.test',
  'legacy-secret', true, 'user-1', 'User 1', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP
);`)
		if err != nil {
			raw.Close()
			t.Fatalf("seed legacy SQLite database: %v", err)
		}
		if err := raw.Close(); err != nil {
			t.Fatalf("close legacy SQLite seed database: %v", err)
		}

		runRepositorySQLiteMigrations(t, dsn)
		db := openRepositorySQLite(t, dsn)
		for attempt := 1; attempt <= 2; attempt++ {
			if err := modelprovider.MigrateLegacyAPIKeys(db); err != nil {
				t.Fatalf("migrate legacy API keys attempt %d: %v", attempt, err)
			}
		}

		assertGORMModelsMatchDatabase(t, db)
		assertSQLiteRepairIndexes(t, db)
		assertSQLiteCredentialColumns(t, db)
		var row orm.UserModelProviderGroup
		if err := db.Where("id = ?", "legacy-group").Take(&row).Error; err != nil {
			t.Fatalf("read migrated provider group: %v", err)
		}
		if row.APIKey != "" {
			t.Fatalf("legacy plaintext API key was not cleared: %q", row.APIKey)
		}
		if row.APIKeyCiphertext == "" || row.CredentialVersion != 1 {
			t.Fatalf("legacy API key was not encrypted: %#v", row)
		}
		plain, err := modelprovider.ResolveAPIKey(row.APIKey, row.APIKeyCiphertext)
		if err != nil {
			t.Fatalf("decrypt migrated API key: %v", err)
		}
		if plain != "legacy-secret" {
			t.Fatalf("decrypted API key=%q, want legacy-secret", plain)
		}
	})
}

func openRepositorySQLite(t *testing.T, dsn string) *gorm.DB {
	t.Helper()
	db, err := orm.Connect(orm.DriverSQLite, dsn)
	if err != nil {
		t.Fatalf("connect SQLite database: %v", err)
	}
	closeGORMDatabase(t, db.DB)
	return db.DB
}

func openRawSQLite(t *testing.T, dsn string) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		t.Fatalf("open SQLite database: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func sqliteSchemaFingerprint(t *testing.T, db *sql.DB) string {
	t.Helper()
	rows, err := db.Query(`SELECT type, name, sql FROM sqlite_master
WHERE sql IS NOT NULL AND name NOT LIKE 'sqlite_%'
  AND name NOT IN ('schema_migrations', 'schema_migration_history', 'schema_migration_lock')
ORDER BY type, name`)
	if err != nil {
		t.Fatalf("read SQLite schema: %v", err)
	}
	defer rows.Close()
	var out strings.Builder
	for rows.Next() {
		var objectType, name, ddl string
		if err := rows.Scan(&objectType, &name, &ddl); err != nil {
			t.Fatalf("scan SQLite schema: %v", err)
		}
		out.WriteString(objectType + "|" + name + "|" + ddl + "\n")
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate SQLite schema: %v", err)
	}
	return out.String()
}

func runRepositorySQLiteMigrations(t *testing.T, dsn string) {
	t.Helper()
	runner, err := NewRunner("sqlite", dsn, "../migrations")
	if err != nil {
		t.Fatalf("create SQLite migration runner: %v", err)
	}
	defer runner.Close()
	if err := runner.Up(0); err != nil {
		t.Fatalf("run SQLite migrations: %v", err)
	}
	if err := runner.Up(0); err != nil {
		t.Fatalf("repeat SQLite migrations: %v", err)
	}
	history, err := runner.readHistory()
	if err != nil {
		t.Fatalf("read SQLite migration history: %v", err)
	}
	if len(history) == 0 {
		t.Fatal("SQLite migration history is empty")
	}

}

func TestRepositorySQLiteAddsAcceptedUserAgreementColumnOnUpgrade(t *testing.T) {
	dsn := t.TempDir() + "/agreement-upgrade.db"
	runRepositorySQLiteMigrations(t, dsn)

	raw := openRawSQLite(t, dsn)
	if _, err := raw.Exec(`ALTER TABLE user_ui_preferences DROP COLUMN accepted_user_agreement_version`); err != nil {
		t.Fatalf("strip agreement column to simulate legacy aggregate schema: %v", err)
	}
	if _, err := raw.Exec(`
DELETE FROM schema_migration_history
WHERE name = 'v0_2/add_accepted_user_agreement_version'
   OR CAST(version AS TEXT) LIKE '%114817%'`); err != nil {
		t.Fatalf("remove agreement migration history: %v", err)
	}
	if err := raw.Close(); err != nil {
		t.Fatalf("close legacy preferences database: %v", err)
	}

	runRepositorySQLiteMigrations(t, dsn)
	db := openRepositorySQLite(t, dsn)
	if !db.Migrator().HasColumn(&orm.UserUIPreferences{}, "accepted_user_agreement_version") {
		t.Fatal("SQLite upgrade did not add accepted_user_agreement_version")
	}
}

func TestRepositorySQLiteExistingAggregateAppliesUncoveredAgreementMigration(t *testing.T) {
	dir := t.TempDir()
	writeMigrationPair(t, versionModeDir(t, dir, "v0_2"), "20260723183515_baseline", `
-- +migrate Dialect sqlite
CREATE TABLE user_ui_preferences (
  user_id varchar(255) PRIMARY KEY,
  chat_preference_notice_dismissed numeric NOT NULL DEFAULT false,
  developer_mode_active numeric NOT NULL DEFAULT false,
  created_at datetime NOT NULL,
  updated_at datetime NOT NULL
);
`, "DROP TABLE user_ui_preferences;")
	writeMigrationPair(t, devModeDir(t, dir, "v0_2"), "20260728114817_add_accepted_user_agreement_version", `
-- +migrate Dialect postgres
ALTER TABLE user_ui_preferences
    ADD COLUMN IF NOT EXISTS accepted_user_agreement_version VARCHAR(64) NOT NULL DEFAULT '';
-- +migrate Dialect sqlite
ALTER TABLE user_ui_preferences ADD COLUMN accepted_user_agreement_version varchar(64) NOT NULL DEFAULT '';
`, "ALTER TABLE user_ui_preferences DROP COLUMN accepted_user_agreement_version;")

	dsn := t.TempDir() + "/fresh-agreement.db"
	raw := openRawSQLite(t, dsn)
	execMigrationFileForDriver(t, raw, filepath.Join(versionModeDir(t, dir, "v0_2"), "20260723183515_baseline.up.sql"), "sqlite")
	seedHistory(t, raw, []historyRecord{{Version: 20260723183515, Name: "baseline"}})
	if err := raw.Close(); err != nil {
		t.Fatalf("close aggregate seed database: %v", err)
	}
	runner, err := NewRunner("sqlite", dsn, dir)
	if err != nil {
		t.Fatalf("create SQLite runner: %v", err)
	}
	defer runner.Close()
	if err := runner.Up(0); err != nil {
		t.Fatalf("fresh SQLite Up: %v", err)
	}

	var column string
	if err := runner.db.QueryRow(`
SELECT name FROM pragma_table_info('user_ui_preferences')
WHERE name = 'accepted_user_agreement_version'
`).Scan(&column); err != nil {
		t.Fatalf("existing aggregate did not apply uncovered agreement column: %v", err)
	}
}

func TestRepositorySQLiteRunsLaterVersionedMigrations(t *testing.T) {
	dir := t.TempDir()
	writeMigrationPair(t, versionModeDir(t, dir, "v0_2"), "20260723183515_baseline",
		"CREATE TABLE baseline (id integer PRIMARY KEY);",
		"DROP TABLE baseline;")
	dsn := t.TempDir() + "/versioned.db"
	runner, err := NewRunner("sqlite", dsn, dir)
	if err != nil {
		t.Fatalf("create SQLite runner: %v", err)
	}
	defer runner.Close()
	if err := runner.Up(0); err != nil {
		t.Fatalf("create SQLite release baseline: %v", err)
	}

	writeMigrationPair(t, devModeDir(t, dir, "v0_2"), "20260728120000_versioned_probe", `
-- +migrate Dialect postgres
CREATE TABLE versioned_probe (id bigint PRIMARY KEY);
-- +migrate Dialect sqlite
CREATE TABLE versioned_probe (id integer PRIMARY KEY);
`, "DROP TABLE versioned_probe;")
	if err := runner.Up(0); err != nil {
		t.Fatalf("apply post-bootstrap SQLite migration: %v", err)
	}
	var table string
	if err := runner.db.QueryRow(
		`SELECT name FROM sqlite_master WHERE type = 'table' AND name = 'versioned_probe'`,
	).Scan(&table); err != nil {
		t.Fatalf("versioned SQLite migration did not create probe table: %v", err)
	}
}

func closeGORMDatabase(t *testing.T, db *gorm.DB) {
	t.Helper()
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("get underlying database: %v", err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
}

func assertSQLiteCredentialColumns(t *testing.T, db *gorm.DB) {
	t.Helper()
	for _, column := range []string{"api_key", "api_key_ciphertext", "credential_version"} {
		if !db.Migrator().HasColumn(&orm.UserModelProviderGroup{}, column) {
			t.Fatalf("SQLite user_model_provider_groups is missing column %s", column)
		}
	}
}

func assertSQLiteRepairIndexes(t *testing.T, db *gorm.DB) {
	t.Helper()
	for _, check := range []struct {
		model any
		index string
	}{
		{&orm.SkillMarketInstall{}, "idx_skill_market_installs_user"},
		{&orm.SkillMarketInstall{}, "idx_skill_market_installs_skill"},
		{&orm.PluginGenerationAnalysis{}, "idx_plugin_generation_analyses_draft"},
		{&orm.PluginRepairRun{}, "idx_plugin_repair_runs_draft"},
	} {
		if !db.Migrator().HasIndex(check.model, check.index) {
			t.Fatalf("SQLite migration is missing index %s", check.index)
		}
	}
}

func assertGORMModelsMatchDatabase(t *testing.T, db *gorm.DB) {
	t.Helper()
	for _, model := range orm.AllModelsForDDL() {
		stmt := &gorm.Statement{DB: db}
		if err := stmt.Parse(model); err != nil {
			t.Fatalf("parse ORM model %T: %v", model, err)
		}
		if !db.Migrator().HasTable(model) {
			t.Fatalf("database is missing ORM table %s", stmt.Schema.Table)
		}
		for _, field := range stmt.Schema.Fields {
			if field.DBName == "" {
				continue
			}
			if !db.Migrator().HasColumn(model, field.DBName) {
				t.Fatalf("database table %s is missing ORM column %s", stmt.Schema.Table, field.DBName)
			}
		}
	}
}
