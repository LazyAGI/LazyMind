package migrate

import (
	"database/sql"
	"encoding/json"
	"os"
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
		if migration.FileVersion > catalog.Modes[1].Aggregate.Version {
			execMigrationFileForDriver(t, releaseDB, migration.UpPath, "sqlite")
		}
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

func TestManagedExternalBindingMigrationBackfillsOwnership(t *testing.T) {
	db := openRawSQLite(t, t.TempDir()+"/managed-external-binding.db")
	if _, err := db.Exec(`
CREATE TABLE external_agent_bindings (
  id text PRIMARY KEY,
  conversation_id text NOT NULL,
  provider text NOT NULL,
  host_id text NOT NULL,
  provider_thread_id text NOT NULL,
  created_at datetime NOT NULL
);
CREATE TABLE external_agent_runs (
  id text PRIMARY KEY,
  conversation_id text NOT NULL,
  provider text NOT NULL,
  host_id text NOT NULL,
  provider_thread_id text NOT NULL,
  action text NOT NULL,
  created_at datetime NOT NULL
);
INSERT INTO external_agent_bindings
  (id, conversation_id, provider, host_id, provider_thread_id, created_at) VALUES
  ('managed', 'managed-conversation', 'codex', 'host-1', 'managed-thread', '2026-09-03 10:01:00'),
  ('native', 'native-conversation', 'codex', 'host-1', 'native-thread', '2026-09-03 10:00:00'),
  ('unrelated', 'unrelated-conversation', 'codex', 'host-1', 'unrelated-thread', '2026-09-03 10:00:00');
INSERT INTO external_agent_runs
  (id, conversation_id, provider, host_id, provider_thread_id, action, created_at) VALUES
  ('managed-run', 'managed-conversation', 'codex', 'host-1', 'managed-thread', 'start', '2026-09-03 10:00:00'),
  ('native-later-run', 'native-conversation', 'codex', 'host-1', 'native-thread', 'resume', '2026-09-03 10:01:00');
`); err != nil {
		t.Fatalf("seed external bindings: %v", err)
	}

	migrationDir := filepath.Join("..", "migrations", "dev_mode", "v0_3")
	upPath := filepath.Join(migrationDir, "20260903044957_restore_managed_external_agent_binding.up.sql")
	execMigrationFileForDriver(t, db, upPath, "sqlite")

	rows, err := db.Query(`SELECT id, managed_by_lazymind FROM external_agent_bindings ORDER BY id`)
	if err != nil {
		t.Fatalf("read migrated bindings: %v", err)
	}
	defer rows.Close()
	got := map[string]bool{}
	for rows.Next() {
		var id string
		var managed bool
		if err := rows.Scan(&id, &managed); err != nil {
			t.Fatalf("scan migrated binding: %v", err)
		}
		got[id] = managed
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate migrated bindings: %v", err)
	}
	if !got["managed"] || got["native"] || got["unrelated"] {
		t.Fatalf("managed binding ownership=%v", got)
	}

	downPath := filepath.Join(migrationDir, "20260903044957_restore_managed_external_agent_binding.down.sql")
	execMigrationFileForDriver(t, db, downPath, "sqlite")
	var columnCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('external_agent_bindings') WHERE name = 'managed_by_lazymind'`).Scan(&columnCount); err != nil {
		t.Fatalf("inspect rolled back binding schema: %v", err)
	}
	if columnCount != 0 {
		t.Fatal("managed_by_lazymind should be removed by the down migration")
	}
}

func TestExternalBindingProviderScopeMigration(t *testing.T) {
	db := openRawSQLite(t, t.TempDir()+"/external-binding-provider-scope.db")
	if _, err := db.Exec(`
CREATE TABLE external_agent_bindings (
  id text PRIMARY KEY,
  conversation_id text NOT NULL UNIQUE,
  provider text NOT NULL,
  host_id text NOT NULL,
  provider_thread_id text NOT NULL,
  managed_by_lazymind numeric NOT NULL DEFAULT false,
  created_by_user_id text NOT NULL,
  created_at datetime NOT NULL,
  updated_at datetime NOT NULL,
  UNIQUE (provider, host_id, provider_thread_id)
);
INSERT INTO external_agent_bindings
  (id, conversation_id, provider, host_id, provider_thread_id, managed_by_lazymind,
   created_by_user_id, created_at, updated_at)
VALUES
  ('codex-binding', 'conversation-1', 'codex', 'host-1', 'codex-thread', true,
   'user-1', '2026-09-03 15:00:00', '2026-09-03 15:00:00');
`); err != nil {
		t.Fatalf("seed external binding: %v", err)
	}

	migrationDir := filepath.Join("..", "migrations", "dev_mode", "v0_3")
	upPath := filepath.Join(migrationDir, "20260903154000_scope_external_agent_binding_by_provider.up.sql")
	execMigrationFileForDriver(t, db, upPath, "sqlite")
	if _, err := db.Exec(`
INSERT INTO external_agent_bindings
  (id, conversation_id, provider, host_id, provider_thread_id, managed_by_lazymind,
   created_by_user_id, created_at, updated_at)
VALUES
  ('workbuddy-binding', 'conversation-1', 'workbuddy', 'host-1', 'workbuddy-thread', true,
   'user-1', '2026-09-03 15:01:00', '2026-09-03 15:01:00')
`); err != nil {
		t.Fatalf("bind second provider after migration: %v", err)
	}
	if _, err := db.Exec(`
INSERT INTO external_agent_bindings
  (id, conversation_id, provider, host_id, provider_thread_id, created_by_user_id, created_at, updated_at)
VALUES
  ('second-workbuddy', 'conversation-1', 'workbuddy', 'host-1', 'other-thread',
   'user-1', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)
`); err == nil {
		t.Fatal("migration allowed two WorkBuddy threads in one conversation")
	}

	downPath := filepath.Join(migrationDir, "20260903154000_scope_external_agent_binding_by_provider.down.sql")
	execMigrationFileForDriver(t, db, downPath, "sqlite")
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM external_agent_bindings WHERE conversation_id='conversation-1'`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("rolled back conversation bindings=%d err=%v", count, err)
	}
}

func TestFixTaskCenterWorkflowRunsMigrationBackfillsLifecycle(t *testing.T) {
	db := openRawSQLite(t, t.TempDir()+"/task-center-workflow-runs.db")
	if _, err := db.Exec(`
CREATE TABLE conversations (
  id text PRIMARY KEY,
  display_name text,
  create_user_id text,
  archived_at datetime,
  deleted_at datetime
);
CREATE TABLE plugin_sessions (
  id text PRIMARY KEY,
  conversation_id text NOT NULL,
  plugin_id text,
  status text NOT NULL,
  dismissed numeric NOT NULL DEFAULT false,
  create_user_id text,
  created_at datetime NOT NULL,
  updated_at datetime NOT NULL
);
CREATE TABLE task_center_tasks (
  id text PRIMARY KEY,
  user_id text NOT NULL,
  conversation_id text NOT NULL,
  plugin_session_id text,
  task_type text NOT NULL,
  title text,
  status text NOT NULL,
  progress_json text,
  created_at datetime NOT NULL,
  updated_at datetime NOT NULL,
  finished_at datetime,
  archived_at datetime,
  archived_reason text NOT NULL DEFAULT ''
);
INSERT INTO conversations (id, display_name, create_user_id, archived_at, deleted_at) VALUES
  ('conv-active', 'Active workflow', 'user-from-conversation', NULL, NULL),
  ('conv-archived', 'Archived workflow', 'user-archived', '2026-08-25 12:00:00', NULL),
  ('conv-trashed', 'Trashed workflow', 'user-trashed', '2026-08-25 11:00:00', '2026-08-25 13:00:00');
INSERT INTO plugin_sessions
  (id, conversation_id, plugin_id, status, dismissed, create_user_id, created_at, updated_at) VALUES
  ('ps-active', 'conv-active', 'ppt-workflow', 'active', false, '', '2026-08-25 10:00:00', '2026-08-25 10:01:00'),
  ('ps-archived', 'conv-archived', 'ppt-workflow', 'completed', false, 'user-session', '2026-08-25 10:00:00', '2026-08-25 10:02:00'),
  ('ps-trashed', 'conv-trashed', 'ppt-workflow', 'waiting', false, 'user-session', '2026-08-25 10:00:00', '2026-08-25 10:03:00'),
  ('ps-dismissed', 'conv-active', 'ppt-workflow', 'stopped', true, 'user-session', '2026-08-25 10:00:00', '2026-08-25 10:04:00'),
  ('ps-missing', 'conv-purged', 'missing-plugin', 'failed', false, 'user-missing', '2026-08-25 10:00:00', '2026-08-25 10:05:00'),
  ('ps-existing', 'conv-active', 'ppt-workflow', 'completed', false, 'user-session', '2026-08-25 10:00:00', '2026-08-25 10:06:00');
INSERT INTO task_center_tasks
  (id, user_id, conversation_id, plugin_session_id, task_type, title, status,
   progress_json, created_at, updated_at, finished_at, archived_at, archived_reason) VALUES
  ('existing-task', 'user-session', 'conv-active', 'ps-existing', 'plugin_run',
   'Existing task', 'succeeded', '{}', '2026-08-25 10:00:00', '2026-08-25 10:06:00',
   '2026-08-25 10:06:00', NULL, ''),
  ('background-task', 'user-session', 'conv-active', NULL, 'background_chat',
   'Background chat', 'succeeded', '{}', '2026-08-25 10:00:00', '2026-08-25 10:06:00',
   '2026-08-25 10:06:00', NULL, '');
`); err != nil {
		t.Fatalf("seed task-center migration data: %v", err)
	}

	migrationDir := filepath.Join("..", "migrations", "dev_mode", "v0_3")
	upPath := filepath.Join(migrationDir, "20260826065814_fix_task_center_workflow_runs.up.sql")
	for attempt := 1; attempt <= 2; attempt++ {
		execMigrationFileForDriver(t, db, upPath, "sqlite")
	}

	var workflowCount, totalCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM task_center_tasks WHERE task_type = 'workflow_run'`).Scan(&workflowCount); err != nil {
		t.Fatalf("count workflow task rows: %v", err)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM task_center_tasks`).Scan(&totalCount); err != nil {
		t.Fatalf("count all task rows: %v", err)
	}
	if workflowCount != 6 || totalCount != 7 {
		t.Fatalf("unexpected backfill counts: workflow=%d total=%d", workflowCount, totalCount)
	}

	type taskState struct {
		status, title, userID, archivedReason string
		finished, archived                    bool
	}
	readTaskState := func(sessionID string) taskState {
		t.Helper()
		var state taskState
		if err := db.QueryRow(`
SELECT status, title, user_id, archived_reason,
       finished_at IS NOT NULL, archived_at IS NOT NULL
FROM task_center_tasks WHERE plugin_session_id = ?`, sessionID).Scan(
			&state.status, &state.title, &state.userID, &state.archivedReason,
			&state.finished, &state.archived,
		); err != nil {
			t.Fatalf("read task state for %s: %v", sessionID, err)
		}
		return state
	}

	if got := readTaskState("ps-active"); got != (taskState{
		status: "running", title: "Active workflow", userID: "user-from-conversation",
	}) {
		t.Fatalf("active workflow state=%#v", got)
	}
	if got := readTaskState("ps-archived"); got != (taskState{
		status: "succeeded", title: "Archived workflow", userID: "user-session",
		archivedReason: "conversation_archive", finished: true, archived: true,
	}) {
		t.Fatalf("archived workflow state=%#v", got)
	}
	if got := readTaskState("ps-trashed"); got != (taskState{
		status: "waiting", title: "Trashed workflow", userID: "user-session",
		archivedReason: "conversation_trash", archived: true,
	}) {
		t.Fatalf("trashed workflow state=%#v", got)
	}
	if got := readTaskState("ps-dismissed"); got.status != "canceled" || !got.finished {
		t.Fatalf("dismissed workflow was not backfilled: %#v", got)
	}
	if got := readTaskState("ps-missing"); got != (taskState{
		status: "failed", title: "missing-plugin", userID: "user-missing",
		archivedReason: "conversation_purged", finished: true, archived: true,
	}) {
		t.Fatalf("purged workflow state=%#v", got)
	}
	var existingID string
	if err := db.QueryRow(`SELECT id FROM task_center_tasks WHERE plugin_session_id = 'ps-existing'`).Scan(&existingID); err != nil {
		t.Fatalf("read existing task id: %v", err)
	}
	if existingID != "existing-task" {
		t.Fatalf("existing task was duplicated or replaced: %q", existingID)
	}

	downPath := filepath.Join(migrationDir, "20260826065814_fix_task_center_workflow_runs.down.sql")
	execMigrationFileForDriver(t, db, downPath, "sqlite")
	var legacyWorkflowCount, backgroundCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM task_center_tasks WHERE task_type = 'plugin_run'`).Scan(&legacyWorkflowCount); err != nil {
		t.Fatalf("count reverted plugin rows: %v", err)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM task_center_tasks WHERE task_type = 'background_chat'`).Scan(&backgroundCount); err != nil {
		t.Fatalf("count preserved background rows: %v", err)
	}
	if legacyWorkflowCount != 6 || backgroundCount != 1 {
		t.Fatalf("unexpected down migration counts: plugin=%d background=%d", legacyWorkflowCount, backgroundCount)
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

func sqliteIndexSQL(t *testing.T, db *sql.DB, name string) string {
	t.Helper()
	var ddl string
	if err := db.QueryRow(`SELECT sql FROM sqlite_master WHERE type = 'index' AND name = ?`, name).Scan(&ddl); err != nil {
		t.Fatalf("read SQLite index %s: %v", name, err)
	}
	return ddl
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

func TestSchedulesFeatureControlMigrationPreservesLegacyPauseState(t *testing.T) {
	db := openRawSQLite(t, t.TempDir()+"/schedules-control.db")
	if _, err := db.Exec(`
CREATE TABLE user_ui_preferences (
  user_id varchar(255) PRIMARY KEY,
  task_center_enabled boolean NOT NULL DEFAULT true
);
INSERT INTO user_ui_preferences (user_id, task_center_enabled)
VALUES ('enabled-user', true), ('paused-user', false);
`); err != nil {
		t.Fatalf("seed legacy preferences: %v", err)
	}
	migrationDir := filepath.Join("..", "migrations", "dev_mode", "v0_3")
	execMigrationFileForDriver(t, db, filepath.Join(migrationDir, "20260825022749_add_schedules_feature_control.up.sql"), "sqlite")

	for _, tc := range []struct {
		userID string
		want   bool
	}{{"enabled-user", true}, {"paused-user", false}} {
		var got bool
		if err := db.QueryRow(`SELECT schedules_enabled FROM user_ui_preferences WHERE user_id = ?`, tc.userID).Scan(&got); err != nil {
			t.Fatalf("read schedules control for %s: %v", tc.userID, err)
		}
		if got != tc.want {
			t.Fatalf("schedules_enabled for %s=%v, want %v", tc.userID, got, tc.want)
		}
	}

	execMigrationFileForDriver(t, db, filepath.Join(migrationDir, "20260825022749_add_schedules_feature_control.down.sql"), "sqlite")
	var columnCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('user_ui_preferences') WHERE name = 'schedules_enabled'`).Scan(&columnCount); err != nil {
		t.Fatalf("inspect schedules control rollback: %v", err)
	}
	if columnCount != 0 {
		t.Fatal("schedules_enabled should be removed by the down migration")
	}
}

func TestChatEntryDefaultsMigrationPreservesLegacySettings(t *testing.T) {
	db := openRawSQLite(t, t.TempDir()+"/chat-entry-defaults.db")
	if _, err := db.Exec(`
CREATE TABLE user_chat_settings (
  user_id varchar(255) PRIMARY KEY,
  enable_workflow boolean NOT NULL DEFAULT true,
  plugin_mode varchar(16) NOT NULL DEFAULT 'dynamic',
  enable_subagent boolean NOT NULL DEFAULT true,
  updated_at datetime NOT NULL
);
INSERT INTO user_chat_settings (user_id, enable_workflow, plugin_mode, enable_subagent, updated_at)
VALUES ('custom-user', false, 'auto', false, CURRENT_TIMESTAMP);
INSERT INTO user_chat_settings (user_id, enable_workflow, plugin_mode, enable_subagent, updated_at)
VALUES ('invalid-mode-user', true, 'legacy', true, CURRENT_TIMESTAMP);
CREATE TABLE conversations (
  id varchar(36) PRIMARY KEY,
  create_user_id varchar(255) NOT NULL,
  enable_plugin boolean,
  plugin_mode varchar(16),
  enable_subagent boolean
);
INSERT INTO conversations (id, create_user_id)
VALUES ('legacy-conversation', 'custom-user');
INSERT INTO conversations (id, create_user_id)
VALUES ('no-settings-conversation', 'missing-user');
INSERT INTO conversations (id, create_user_id)
VALUES ('invalid-mode-conversation', 'invalid-mode-user');
INSERT INTO conversations (
  id, create_user_id, enable_plugin, plugin_mode, enable_subagent
) VALUES ('partial-conversation', 'custom-user', true, 'dynamic', NULL);
`); err != nil {
		t.Fatalf("seed legacy chat settings: %v", err)
	}
	migrationDir := filepath.Join("..", "migrations", "dev_mode", "v0_3")
	migrationPath := filepath.Join(migrationDir, "20260825031307_add_chat_entry_defaults.up.sql")
	migrationBody, err := os.ReadFile(migrationPath)
	if err != nil {
		t.Fatalf("read chat entry defaults migration: %v", err)
	}
	sqliteMigration, err := migrationSQLForDriver(string(migrationBody), "sqlite")
	if err != nil {
		t.Fatalf("select SQLite chat entry defaults migration: %v", err)
	}
	backfillMarker := "UPDATE conversations\nSET enable_plugin"
	backfillOffset := strings.Index(sqliteMigration, backfillMarker)
	if backfillOffset < 0 {
		t.Fatalf("conversation policy backfill statement missing from migration")
	}
	if _, err := db.Exec(sqliteMigration[:backfillOffset]); err != nil {
		t.Fatalf("run chat defaults migration through backup capture: %v", err)
	}
	if _, err := db.Exec(`
INSERT INTO conversations (id, create_user_id)
VALUES ('migration-window-conversation', 'custom-user')
`); err != nil {
		t.Fatalf("create migration-window conversation: %v", err)
	}
	if _, err := db.Exec(sqliteMigration[backfillOffset:]); err != nil {
		t.Fatalf("finish chat defaults migration after concurrent insert: %v", err)
	}

	var quickRaw, taskRaw string
	if err := db.QueryRow(`
SELECT quick_question_defaults, new_task_defaults
FROM user_chat_settings WHERE user_id = 'custom-user'
`).Scan(&quickRaw, &taskRaw); err != nil {
		t.Fatalf("read migrated chat defaults: %v", err)
	}
	var quick, task struct {
		ThinkingDepth        string `json:"thinking_depth"`
		ConversationSettings struct {
			ChatExecutor   string `json:"chat_executor"`
			EnableWorkflow bool   `json:"enable_workflow"`
			WorkflowMode   string `json:"workflow_mode"`
			EnableSubagent bool   `json:"enable_subagent"`
		} `json:"conversation_settings"`
	}
	if err := json.Unmarshal([]byte(quickRaw), &quick); err != nil {
		t.Fatalf("decode quick-question defaults: %v, raw=%s", err, quickRaw)
	}
	if err := json.Unmarshal([]byte(taskRaw), &task); err != nil {
		t.Fatalf("decode new-task defaults: %v, raw=%s", err, taskRaw)
	}
	if quick.ThinkingDepth != "medium" || quick.ConversationSettings.EnableWorkflow ||
		quick.ConversationSettings.WorkflowMode != "auto" || quick.ConversationSettings.EnableSubagent ||
		quick.ConversationSettings.ChatExecutor != "lazymind" {
		t.Fatalf("unexpected migrated quick-question defaults: %#v", quick)
	}
	if task.ThinkingDepth != "high" || task.ConversationSettings.EnableWorkflow ||
		task.ConversationSettings.WorkflowMode != "auto" || task.ConversationSettings.EnableSubagent ||
		task.ConversationSettings.ChatExecutor != "lazymind" {
		t.Fatalf("unexpected migrated new-task defaults: %#v", task)
	}
	var conversationDepth string
	if err := db.QueryRow(`SELECT thinking_depth FROM conversations WHERE id = 'legacy-conversation'`).Scan(&conversationDepth); err != nil {
		t.Fatalf("read migrated conversation thinking depth: %v", err)
	}
	if conversationDepth != "medium" {
		t.Fatalf("legacy conversation thinking depth=%q, want medium", conversationDepth)
	}
	for _, tc := range []struct {
		id             string
		enableWorkflow bool
		workflowMode   string
		enableSubagent bool
	}{
		{id: "legacy-conversation", enableWorkflow: false, workflowMode: "auto", enableSubagent: false},
		{id: "no-settings-conversation", enableWorkflow: true, workflowMode: "dynamic", enableSubagent: true},
		{id: "invalid-mode-conversation", enableWorkflow: true, workflowMode: "dynamic", enableSubagent: true},
		{id: "partial-conversation", enableWorkflow: true, workflowMode: "dynamic", enableSubagent: false},
	} {
		var enableWorkflow, enableSubagent bool
		var workflowMode string
		if err := db.QueryRow(`
SELECT enable_plugin, plugin_mode, enable_subagent
FROM conversations WHERE id = ?
`, tc.id).Scan(&enableWorkflow, &workflowMode, &enableSubagent); err != nil {
			t.Fatalf("read migrated conversation policy for %s: %v", tc.id, err)
		}
		if enableWorkflow != tc.enableWorkflow || workflowMode != tc.workflowMode || enableSubagent != tc.enableSubagent {
			t.Fatalf(
				"conversation %s policy=(%v,%q,%v), want (%v,%q,%v)",
				tc.id, enableWorkflow, workflowMode, enableSubagent,
				tc.enableWorkflow, tc.workflowMode, tc.enableSubagent,
			)
		}
	}
	var backupCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM conversation_policy_snapshot_backups`).Scan(&backupCount); err != nil {
		t.Fatalf("count conversation policy rollback masks: %v", err)
	}
	if backupCount != 4 {
		t.Fatalf("conversation policy rollback mask count=%d, want 4", backupCount)
	}
	var windowEnableWorkflow, windowEnableSubagent sql.NullBool
	var windowWorkflowMode sql.NullString
	if err := db.QueryRow(`
SELECT enable_plugin, plugin_mode, enable_subagent
FROM conversations WHERE id = 'migration-window-conversation'
`).Scan(&windowEnableWorkflow, &windowWorkflowMode, &windowEnableSubagent); err != nil {
		t.Fatalf("read migration-window conversation policy: %v", err)
	}
	if windowEnableWorkflow.Valid || windowWorkflowMode.Valid || windowEnableSubagent.Valid {
		t.Fatalf("unbacked migration-window conversation was backfilled: (%#v,%#v,%#v)",
			windowEnableWorkflow, windowWorkflowMode, windowEnableSubagent)
	}
	if _, err := db.Exec(`
INSERT INTO conversations (
  id, create_user_id, enable_plugin, plugin_mode, enable_subagent
) VALUES ('post-migration-conversation', 'custom-user', false, 'auto', false)
`); err != nil {
		t.Fatalf("create post-migration conversation: %v", err)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM conversation_policy_snapshot_backups`).Scan(&backupCount); err != nil {
		t.Fatalf("recount conversation policy rollback masks: %v", err)
	}
	if backupCount != 4 {
		t.Fatalf("post-migration conversation was added to rollback masks: count=%d", backupCount)
	}

	execMigrationFileForDriver(t, db, filepath.Join(migrationDir, "20260825031307_add_chat_entry_defaults.down.sql"), "sqlite")
	for _, column := range []string{"quick_question_defaults", "new_task_defaults"} {
		var count int
		if err := db.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('user_chat_settings') WHERE name = ?`, column).Scan(&count); err != nil {
			t.Fatalf("inspect %s rollback: %v", column, err)
		}
		if count != 0 {
			t.Fatalf("%s should be removed by the down migration", column)
		}
	}
	var conversationColumnCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('conversations') WHERE name = 'thinking_depth'`).Scan(&conversationColumnCount); err != nil {
		t.Fatalf("inspect conversation thinking depth rollback: %v", err)
	}
	if conversationColumnCount != 0 {
		t.Fatal("conversations.thinking_depth should be removed by the down migration")
	}
	for _, tc := range []struct {
		id                               string
		enableValid, modeValid, subValid bool
		enableValue, subValue            bool
		modeValue                        string
	}{
		{id: "legacy-conversation"},
		{id: "no-settings-conversation"},
		{id: "invalid-mode-conversation"},
		{id: "migration-window-conversation"},
		{id: "partial-conversation", enableValid: true, modeValid: true, enableValue: true, modeValue: "dynamic"},
		{id: "post-migration-conversation", enableValid: true, modeValid: true, subValid: true, modeValue: "auto"},
	} {
		var enableWorkflow, enableSubagent sql.NullBool
		var workflowMode sql.NullString
		if err := db.QueryRow(`
SELECT enable_plugin, plugin_mode, enable_subagent
FROM conversations WHERE id = ?
`, tc.id).Scan(&enableWorkflow, &workflowMode, &enableSubagent); err != nil {
			t.Fatalf("read rolled-back conversation policy for %s: %v", tc.id, err)
		}
		if enableWorkflow.Valid != tc.enableValid || workflowMode.Valid != tc.modeValid || enableSubagent.Valid != tc.subValid ||
			(enableWorkflow.Valid && enableWorkflow.Bool != tc.enableValue) ||
			(workflowMode.Valid && workflowMode.String != tc.modeValue) ||
			(enableSubagent.Valid && enableSubagent.Bool != tc.subValue) {
			t.Fatalf("rolled-back conversation %s policy=(%#v,%#v,%#v), want valid=(%v,%v,%v)",
				tc.id, enableWorkflow, workflowMode, enableSubagent, tc.enableValid, tc.modeValid, tc.subValid)
		}
	}
	var backupTableCount int
	if err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = 'conversation_policy_snapshot_backups'`).Scan(&backupTableCount); err != nil {
		t.Fatalf("inspect conversation policy rollback table: %v", err)
	}
	if backupTableCount != 0 {
		t.Fatal("conversation policy rollback table should be removed by down migration")
	}
}

func TestSkillUniqueIndexSQLiteDownRemovesTrashedDuplicates(t *testing.T) {
	db := openRawSQLite(t, t.TempDir()+"/skill-unique-down.db")
	if _, err := db.Exec(`
CREATE TABLE skills (
  id text PRIMARY KEY,
  owner_user_id text NOT NULL,
  category text NOT NULL,
  skill_name text NOT NULL,
  relative_root text NOT NULL,
  deleted_at datetime
);
CREATE UNIQUE INDEX uk_skills_owner_identity
  ON skills(owner_user_id, category, skill_name);
CREATE UNIQUE INDEX uk_skills_owner_relative_root
  ON skills(owner_user_id, relative_root);
INSERT INTO skills (id, owner_user_id, category, skill_name, relative_root, deleted_at)
VALUES
  ('identity-original', 'user-a', 'research', 'demo', 'research/demo-old', NULL),
  ('root-original', 'user-b', 'research', 'alpha', 'shared/root', NULL),
  ('keep-trashed', 'user-c', 'personal', 'archived', 'personal/archived', CURRENT_TIMESTAMP);
`); err != nil {
		t.Fatalf("seed pre-migration skills: %v", err)
	}

	migrationDir := filepath.Join("..", "migrations", "dev_mode", "v0_3")
	execMigrationFileForDriver(t, db, filepath.Join(migrationDir, "20260827120000_unique_skill_name_per_owner.up.sql"), "sqlite")
	for _, name := range []string{"uk_skills_owner_identity", "uk_skills_owner_relative_root"} {
		if sql := sqliteIndexSQL(t, db, name); !strings.Contains(strings.ToLower(sql), "deleted_at is null") {
			t.Fatalf("up migration did not create a partial unique index for %s: %s", name, sql)
		}
	}

	if _, err := db.Exec(`
UPDATE skills SET deleted_at = CURRENT_TIMESTAMP WHERE id IN ('identity-original', 'root-original');
INSERT INTO skills (id, owner_user_id, category, skill_name, relative_root, deleted_at)
VALUES
  ('identity-recreated', 'user-a', 'research', 'demo', 'research/demo', NULL),
  ('root-recreated', 'user-b', 'personal', 'beta', 'shared/root', NULL);
`); err != nil {
		t.Fatalf("soft-delete and recreate same-name skills: %v", err)
	}

	execMigrationFileForDriver(t, db, filepath.Join(migrationDir, "20260827120000_unique_skill_name_per_owner.down.sql"), "sqlite")
	for _, name := range []string{"uk_skills_owner_identity", "uk_skills_owner_relative_root"} {
		if sql := sqliteIndexSQL(t, db, name); strings.Contains(strings.ToLower(sql), "deleted_at is null") {
			t.Fatalf("down migration did not restore a full unique index for %s: %s", name, sql)
		}
	}

	rows, err := db.Query(`SELECT id FROM skills ORDER BY id`)
	if err != nil {
		t.Fatalf("list skills after down: %v", err)
	}
	defer rows.Close()
	var got []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			t.Fatalf("scan skill id: %v", err)
		}
		got = append(got, id)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate skills after down: %v", err)
	}
	want := []string{"identity-recreated", "keep-trashed", "root-recreated"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("skills after down=%v, want %v", got, want)
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

func TestRepositorySQLiteDownContinuesPastPostAggregateRelationColumns(t *testing.T) {
	dir := newStructuredMigrationDir(t)
	writeMigrationPair(t, versionModeDir(t, dir, "v0_1"), "20260101000000_baseline",
		"CREATE TABLE conversations (id integer PRIMARY KEY);",
		"DROP TABLE conversations;")
	writeMigrationPair(t, versionModeDir(t, dir, "v0_2"), "20260201000000_release", `
ALTER TABLE conversations ADD COLUMN parent_conversation_id text;
ALTER TABLE conversations ADD COLUMN relation_type text NOT NULL DEFAULT '';
CREATE TABLE aggregate_tail (id integer PRIMARY KEY);
`, `
ALTER TABLE conversations DROP COLUMN relation_type;
ALTER TABLE conversations DROP COLUMN parent_conversation_id;
DROP TABLE aggregate_tail;
`)
	runner := openSquashTestRunner(t, filepath.Join(t.TempDir(), "acl.db"), dir)
	defer runner.Close()
	if err := runner.Up(0); err != nil {
		t.Fatalf("up through aggregate migration: %v", err)
	}

	writeMigrationPair(t, devModeDir(t, dir, "v0_2"), "20260301000000_add_relations", `
ALTER TABLE conversations ADD COLUMN parent_conversation_id text;
ALTER TABLE conversations ADD COLUMN relation_type text NOT NULL DEFAULT '';
CREATE INDEX idx_conversations_relation ON conversations(parent_conversation_id, relation_type);
`, `
DROP INDEX IF EXISTS idx_conversations_relation;
ALTER TABLE conversations DROP COLUMN relation_type;
ALTER TABLE conversations DROP COLUMN parent_conversation_id;
`)
	if err := runner.Up(0); err != nil {
		t.Fatalf("up post-aggregate relation migration: %v", err)
	}
	var indexCount int
	if err := runner.db.QueryRow(`
SELECT COUNT(*) FROM sqlite_master
WHERE type = 'index' AND name = 'idx_conversations_relation'
`).Scan(&indexCount); err != nil {
		t.Fatalf("query relation index: %v", err)
	}
	if indexCount != 1 {
		t.Fatalf("post-aggregate migration stopped after duplicate columns; index count=%d", indexCount)
	}

	if err := runner.Down(2); err != nil {
		t.Fatalf("down post-aggregate relation migration and aggregate: %v", err)
	}
	var tailCount int
	if err := runner.db.QueryRow(`
SELECT COUNT(*) FROM sqlite_master
WHERE type = 'table' AND name = 'aggregate_tail'
`).Scan(&tailCount); err != nil {
		t.Fatalf("query aggregate tail table: %v", err)
	}
	if tailCount != 0 {
		t.Fatalf("aggregate down stopped after missing relation columns; table count=%d", tailCount)
	}
	var relationColumnCount int
	if err := runner.db.QueryRow(`
SELECT COUNT(*) FROM pragma_table_info('conversations')
WHERE name IN ('parent_conversation_id', 'relation_type')
`).Scan(&relationColumnCount); err != nil {
		t.Fatalf("query relation columns: %v", err)
	}
	if relationColumnCount != 0 {
		t.Fatalf("relation columns remain after down -n 2: count=%d", relationColumnCount)
	}
	history, err := runner.readHistory()
	if err != nil {
		t.Fatalf("read migration history: %v", err)
	}
	if len(history) != 1 || history[0].Name != "baseline" {
		t.Fatalf("history after down -n 2=%#v, want only baseline", history)
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
		{&orm.WorkflowGenerationAnalysis{}, "idx_plugin_generation_analyses_draft"},
		{&orm.WorkflowRepairRun{}, "idx_plugin_repair_runs_draft"},
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
