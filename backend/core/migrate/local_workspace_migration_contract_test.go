package migrate

import (
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

var workspaceMigrationNames = []string{
	"20260901061506_create_local_workspaces",
	"20260902120000_allow_local_workspace_writes",
	"20260903023152_add_workspace_permission_mode",
	"20260908065108_fix_workspace_binding_timestamp",
}

func TestLocalWorkspaceMigrationPairsExist(t *testing.T) {
	for _, name := range workspaceMigrationNames {
		for _, direction := range []string{"up", "down"} {
			t.Run(name+"/"+direction, func(t *testing.T) {
				p := filepath.Join("..", "migrations", "dev_mode", "v0_3", name+"."+direction+".sql")
				if _, err := os.ReadFile(p); err != nil {
					t.Fatalf("required shared migration missing: %v", err)
				}
			})
		}
	}
}

func TestLocalWorkspaceSQLiteUpgradePreservesBindingAndDown(t *testing.T) {
	db := openRawSQLite(t, filepath.Join(t.TempDir(), "workspace.db"))
	testWorkspaceUpgradeAndDown(t, db, "sqlite")
}

func TestLocalWorkspacePostgresUpgradePreservesBindingAndDown(t *testing.T) {
	dsn := strings.TrimSpace(os.Getenv(migrationPostgresDSNEnv))
	if dsn == "" {
		t.Skip("set MIGRATION_TEST_POSTGRES_DSN to a disposable server")
	}
	db := createTemporaryPostgresDatabase(t, dsn, "workspace")
	testWorkspaceUpgradeAndDown(t, db, "postgres")
}

func testWorkspaceUpgradeAndDown(t *testing.T, db *sql.DB, driver string) {
	t.Helper()
	if driver == "sqlite" {
		if _, err := db.Exec(`PRAGMA foreign_keys=ON`); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.Exec(`CREATE TABLE conversations(id TEXT PRIMARY KEY); INSERT INTO conversations VALUES ('task');`); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join("..", "migrations", "dev_mode", "v0_3")
	execMigrationFileForDriver(t, db, filepath.Join(dir, workspaceMigrationNames[0]+".up.sql"), driver)
	if _, err := db.Exec(`INSERT INTO local_workspaces(id,create_user_id,display_name,canonical_path,directory_identity,status,source,authorized_at,last_used_at,created_at,updated_at)
 VALUES ('grant','owner','project','/fixture','identity','active','local',CURRENT_TIMESTAMP,CURRENT_TIMESTAMP,CURRENT_TIMESTAMP,CURRENT_TIMESTAMP);
 INSERT INTO conversation_workspace_bindings(conversation_id,workspace_id,created_at) VALUES ('task','grant',CURRENT_TIMESTAMP);`); err != nil {
		t.Fatal(err)
	}
	for _, name := range workspaceMigrationNames[1:] {
		execMigrationFileForDriver(t, db, filepath.Join(dir, name+".up.sql"), driver)
		if name == "20260903023152_add_workspace_permission_mode" {
			if _, err := db.Exec(`UPDATE conversation_workspace_bindings SET updated_at='2026-01-02 03:04:05'`); err != nil {
				t.Fatal(err)
			}
		}
	}
	var preserved int
	if err := db.QueryRow(`SELECT COUNT(*) FROM conversation_workspace_bindings WHERE updated_at='2026-01-02 03:04:05'`).Scan(&preserved); err != nil || preserved != 1 {
		t.Fatalf("correction lost timestamp: count=%d err=%v", preserved, err)
	}
	var mode, workspace string
	var version int64
	if err := db.QueryRow(`SELECT workspace_id,permission_mode,permission_version FROM conversation_workspace_bindings WHERE conversation_id='task'`).Scan(&workspace, &mode, &version); err != nil {
		t.Fatal(err)
	}
	if workspace != "grant" || mode != "ask_as_needed" || version != 1 {
		t.Fatalf("upgrade lost binding/defaults: %s %s %d", workspace, mode, version)
	}
	if _, err := db.Exec(`UPDATE conversation_workspace_bindings SET permission_mode='invalid'`); err == nil {
		t.Fatal("invalid permission accepted")
	}
	if _, err := db.Exec(`INSERT INTO conversation_workspace_bindings SELECT * FROM conversation_workspace_bindings`); err == nil {
		t.Fatal("duplicate task binding accepted")
	}
	for i := len(workspaceMigrationNames) - 1; i >= 0; i-- {
		execMigrationFileForDriver(t, db, filepath.Join(dir, workspaceMigrationNames[i]+".down.sql"), driver)
	}
	for _, table := range []string{"local_workspaces", "conversation_workspace_bindings"} {
		if _, err := db.Exec("SELECT * FROM " + table); err == nil {
			t.Fatalf("down retained %s", table)
		}
	}
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM conversations WHERE id='task'`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("down lost conversation: count=%d err=%v", count, err)
	}
}

func TestLocalWorkspaceAggregateAndDevSchemasMatch(t *testing.T) {
	for _, driver := range []string{"sqlite", "postgres"} {
		t.Run(driver, func(t *testing.T) {
			var release, dev *sql.DB
			if driver == "postgres" {
				dsn := strings.TrimSpace(os.Getenv(migrationPostgresDSNEnv))
				if dsn == "" {
					t.Skip("set MIGRATION_TEST_POSTGRES_DSN to a disposable server")
				}
				release = createTemporaryPostgresDatabase(t, dsn, "workspace_release")
				dev = createTemporaryPostgresDatabase(t, dsn, "workspace_dev")
			} else {
				release = openRawSQLite(t, filepath.Join(t.TempDir(), "release.db"))
				dev = openRawSQLite(t, filepath.Join(t.TempDir(), "dev.db"))
			}
			runner := &Runner{dir: "../migrations"}
			catalog, err := runner.loadCatalog()
			if err != nil {
				t.Fatal(err)
			}
			for _, mode := range catalog.Modes {
				if mode.Aggregate == nil {
					t.Fatalf("missing aggregate %s", mode.Name)
				}
				if mode.Name != "v0_3" {
					for _, db := range []*sql.DB{release, dev} {
						execMigrationFileForDriver(t, db, mode.Aggregate.UpPath, driver)
					}
					continue
				}
				execMigrationFileForDriver(t, release, mode.Aggregate.UpPath, driver)
				for _, migration := range mode.Dev {
					execMigrationFileForDriver(t, dev, migration.UpPath, driver)
				}
				for _, db := range []*sql.DB{release, dev} {
					if _, err := db.Exec(`SELECT permission_mode, permission_version FROM conversation_workspace_bindings`); err != nil {
						t.Fatalf("workspace schema missing: %v", err)
					}
					if driver == "sqlite" {
						assertWorkspaceSQLiteConstraints(t, db)
					}
					if _, err := db.Exec(`SELECT directory_identity, status, version FROM local_workspaces`); err != nil {
						t.Fatalf("grant schema missing: %v", err)
					}
				}
				fingerprint := workspaceSQLiteSchemaFingerprint
				if driver == "postgres" {
					fingerprint = postgresSchemaFingerprint
				}
				if a, b := fingerprint(t, release), fingerprint(t, dev); a != b {
					t.Fatalf("aggregate/dev mismatch: release=%s dev=%s", a, b)
				}
				execMigrationFileForDriver(t, release, mode.Aggregate.DownPath, driver)
				for _, table := range []string{"local_workspaces", "conversation_workspace_bindings"} {
					if _, err := release.Exec("SELECT * FROM " + table); err == nil {
						t.Fatalf("aggregate down retained %s", table)
					}
				}
				return
			}
			t.Fatal("v0_3 catalog not found")
		})
	}
}

func workspaceSQLiteSchemaFingerprint(t *testing.T, db *sql.DB) string {
	t.Helper()
	var result strings.Builder
	for _, table := range []string{"local_workspaces", "conversation_workspace_bindings"} {
		for _, query := range []string{
			`SELECT name,type,"notnull",COALESCE(dflt_value,''),pk FROM pragma_table_info('` + table + `') ORDER BY name`,
			`SELECT "table","from","to",on_update,on_delete FROM pragma_foreign_key_list('` + table + `') ORDER BY "from"`,
		} {
			rows, err := db.Query(query)
			if err != nil {
				t.Fatal(err)
			}
			for rows.Next() {
				var a, b, c, d, e string
				if err := rows.Scan(&a, &b, &c, &d, &e); err != nil {
					t.Fatal(err)
				}
				result.WriteString(table + "|" + a + "|" + b + "|" + c + "|" + d + "|" + e + "\n")
			}
			if err := rows.Err(); err != nil {
				t.Fatal(err)
			}
			rows.Close()
		}
	}
	return result.String()
}

func TestLocalWorkspaceRunnerPreservesBindingWithForeignKeysEnabled(t *testing.T) {
	db := openRawSQLite(t, filepath.Join(t.TempDir(), "runner.db"))
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(`PRAGMA foreign_keys=ON; CREATE TABLE conversations(id TEXT PRIMARY KEY); INSERT INTO conversations VALUES ('task');`); err != nil {
		t.Fatal(err)
	}
	runner := &Runner{driver: "sqlite", db: db, dir: "../migrations"}
	if err := runner.prepare(); err != nil {
		t.Fatal(err)
	}
	catalog, err := runner.loadCatalog()
	if err != nil {
		t.Fatal(err)
	}
	var applied []migrationFile
	for _, mode := range catalog.Modes {
		if mode.Name != "v0_3" {
			continue
		}
		for i, name := range workspaceMigrationNames {
			var migration migrationFile
			for _, candidate := range mode.Dev {
				if strings.HasSuffix(candidate.UpPath, name+".up.sql") {
					migration = candidate
					break
				}
			}
			if migration.UpPath == "" {
				t.Fatalf("missing %s", name)
			}
			if err := runner.applyUpMigration(migration, 0); err != nil {
				t.Fatalf("runner %s: %v", name, err)
			}
			applied = append(applied, migration)
			if i == 0 {
				if _, err := db.Exec(`INSERT INTO local_workspaces(id,create_user_id,display_name,canonical_path,directory_identity,status,source,authorized_at,last_used_at,created_at,updated_at)
 VALUES ('grant','owner','project','/fixture','identity','active','local',CURRENT_TIMESTAMP,CURRENT_TIMESTAMP,CURRENT_TIMESTAMP,CURRENT_TIMESTAMP);
 INSERT INTO conversation_workspace_bindings(conversation_id,workspace_id,created_at) VALUES ('task','grant',CURRENT_TIMESTAMP);`); err != nil {
					t.Fatal(err)
				}
			}
		}
	}
	var count, enabled int
	if err := db.QueryRow(`SELECT COUNT(*) FROM conversation_workspace_bindings WHERE conversation_id='task' AND workspace_id='grant'`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`PRAGMA foreign_keys`).Scan(&enabled); err != nil {
		t.Fatal(err)
	}
	if count != 1 || enabled != 1 {
		t.Fatalf("binding count=%d foreign_keys=%d", count, enabled)
	}
	for i := len(applied) - 1; i >= 0; i-- {
		var remaining []historyRecord
		for _, migration := range applied[:i] {
			remaining = append(remaining, historyRecord{Version: migration.Version, Name: migration.Name})
		}
		if err := runner.applyDownMigration(applied[i], remaining); err != nil {
			t.Fatalf("runner down %s: %v", applied[i].Name, err)
		}
	}
	if err := db.QueryRow(`PRAGMA foreign_keys`).Scan(&enabled); err != nil || enabled != 1 {
		t.Fatalf("down foreign_keys=%d err=%v", enabled, err)
	}
	if err := db.QueryRow(`SELECT COUNT(*) FROM conversations WHERE id='task'`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("down conversation count=%d err=%v", count, err)
	}

}

func assertWorkspaceSQLiteConstraints(t *testing.T, db *sql.DB) {
	t.Helper()
	// Use a rolled-back row to exercise CHECK constraints without polluting fixtures.
	tx, err := db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`INSERT INTO local_workspaces(id,create_user_id,display_name,canonical_path,directory_identity,status,source,authorized_at,last_used_at,created_at,updated_at)
 VALUES ('constraint-grant','owner','project','/fixture','identity','active','local',CURRENT_TIMESTAMP,CURRENT_TIMESTAMP,CURRENT_TIMESTAMP,CURRENT_TIMESTAMP)`); err != nil {
		t.Fatal(err)
	}
	for _, column := range []string{"status", "source", "read_policy", "write_policy"} {
		if _, err := tx.Exec("UPDATE local_workspaces SET " + column + "='invalid' WHERE id='constraint-grant'"); err == nil {
			t.Fatalf("%s lacks CHECK", column)
		}
	}
	// Foreign keys are validated independently; use an existing conversation-free fixture here.
	var ddl string
	if err := tx.QueryRow(`SELECT sql FROM sqlite_master WHERE name='conversation_workspace_bindings'`).Scan(&ddl); err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"always_ask", "ask_as_needed", "allow_all"} {
		if !strings.Contains(ddl, "'"+mode+"'") {
			t.Fatalf("permission CHECK lacks %s", mode)
		}
	}
	for index, want := range map[string]string{
		"idx_local_workspaces_user_recent":              "create_user_id:0,status:0,last_used_at:1",
		"idx_conversation_workspace_bindings_workspace": "workspace_id:0",
	} {
		var got string
		query := `SELECT group_concat(name || ':' || "desc", ',') FROM (SELECT name,"desc" FROM pragma_index_xinfo('` + index + `') WHERE key=1 ORDER BY seqno)`
		if err := tx.QueryRow(query).Scan(&got); err != nil {
			t.Fatal(err)
		}
		if got != want {
			t.Fatalf("index %s=%s want %s", index, got, want)
		}
	}
}

func TestWorkspaceMigrationTransactionRestoresForeignKeysAndRollsBack(t *testing.T) {
	for _, enabled := range []string{"ON", "OFF"} {
		for _, failure := range []string{"none", "sql", "integrity", "history"} {
			t.Run(enabled+"/"+failure, func(t *testing.T) {
				db := openRawSQLite(t, filepath.Join(t.TempDir(), "transaction.db"))
				db.SetMaxOpenConns(1)
				if _, err := db.Exec(`PRAGMA foreign_keys=` + enabled + `; CREATE TABLE parent(id INTEGER PRIMARY KEY); CREATE TABLE child(parent_id INTEGER REFERENCES parent(id)); INSERT INTO parent VALUES(1); INSERT INTO child VALUES(1);`); err != nil {
					t.Fatal(err)
				}
				body := "PRAGMA foreign_keys=OFF; UPDATE parent SET id=1;"
				switch failure {
				case "sql":
					body += " INSERT INTO missing_table VALUES(1);"
				case "integrity":
					body += " DELETE FROM parent;"
				}
				err := runMigrationTransaction(db, "sqlite", body, nil, func(tx *sql.Tx) error {
					if failure == "history" {
						return errors.New("history update failed")
					}
					return nil
				})
				wantFailure := failure == "sql" || failure == "history" || (failure == "integrity" && enabled == "ON")
				if (err != nil) != wantFailure {
					t.Fatalf("error=%v wantFailure=%v", err, wantFailure)
				}
				var state, count int
				if err := db.QueryRow("PRAGMA foreign_keys").Scan(&state); err != nil {
					t.Fatal(err)
				}
				if (state == 1) != (enabled == "ON") {
					t.Fatalf("foreign_keys=%d originally=%s", state, enabled)
				}
				if err := db.QueryRow("SELECT COUNT(*) FROM parent").Scan(&count); err != nil {
					t.Fatal(err)
				}
				if wantFailure && count != 1 {
					t.Fatalf("rollback lost parent: %d", count)
				}
			})
		}
	}
}
