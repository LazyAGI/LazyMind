package migrate

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"
)

func TestWorkflowControlMigrationPreservesNativeRuns(t *testing.T) {
	for _, driver := range []string{"sqlite", "postgres"} {
		t.Run(driver, func(t *testing.T) {
			var db *sql.DB
			if driver == "sqlite" {
				db = openRawSQLite(t, filepath.Join(t.TempDir(), "control.db"))
			} else {
				dsn := os.Getenv(migrationPostgresDSNEnv)
				if dsn == "" {
					t.Skip("PostgreSQL integration DSN required")
				}
				db = createTemporaryPostgresDatabase(t, dsn, "workflow_control")
			}
			catalog, err := (&Runner{dir: "../migrations"}).loadCatalog()
			if err != nil {
				t.Fatal(err)
			}
			for _, mode := range catalog.Modes[:len(catalog.Modes)-1] {
				execMigrationFileForDriver(t, db, mode.Aggregate.UpPath, driver)
			}
			var additions []migrationFile
			for _, migration := range catalog.Modes[len(catalog.Modes)-1].Dev {
				if migration.FileVersion == 20260908093904 || migration.FileVersion == 20260909053754 {
					additions = append(additions, migration)
				} else {
					execMigrationFileForDriver(t, db, migration.UpPath, driver)
				}
			}
			if len(additions) != 2 {
				t.Fatal("missing external workflow migrations")
			}
			if _, err := db.Exec(`INSERT INTO plugin_sessions(id,conversation_id,plugin_id,status,created_at,updated_at) VALUES ('native','conv','writer','active',CURRENT_TIMESTAMP,CURRENT_TIMESTAMP)`); err != nil {
				t.Fatal(err)
			}
			for round := 0; round < 2; round++ {
				for _, migration := range additions {
					execMigrationFileForDriver(t, db, migration.UpPath, driver)
				}
				var protocol, binding, status string
				if err := db.QueryRow(`SELECT control_protocol,control_binding_json,status FROM plugin_sessions WHERE id='native'`).Scan(&protocol, &binding, &status); err != nil {
					t.Fatal(err)
				}
				if protocol != "" || binding != "{}" || status != "active" {
					t.Fatalf("native run changed: %q %q %q", protocol, binding, status)
				}
				for i := len(additions) - 1; i >= 0; i-- {
					execMigrationFileForDriver(t, db, additions[i].DownPath, driver)
				}
				if err := db.QueryRow(`SELECT status FROM plugin_sessions WHERE id='native'`).Scan(&status); err != nil || status != "active" {
					t.Fatalf("rollback lost native run: %s %v", status, err)
				}
			}
		})
	}
}

func TestWorkflowMigrationsDoNotCreateControllerSpecificApprovalPreferences(t *testing.T) {
	catalog, err := (&Runner{dir: "../migrations"}).loadCatalog()
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"release", "dev"} {
		t.Run(path, func(t *testing.T) {
			db := openRawSQLite(t, filepath.Join(t.TempDir(), path+".db"))
			for i, mode := range catalog.Modes {
				if path == "release" || i < len(catalog.Modes)-1 {
					execMigrationFileForDriver(t, db, mode.Aggregate.UpPath, "sqlite")
					continue
				}
				for _, migration := range mode.Dev {
					execMigrationFileForDriver(t, db, migration.UpPath, "sqlite")
				}
			}
			var count int
			if err := db.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='external_workflow_approval_preferences'`).Scan(&count); err != nil {
				t.Fatal(err)
			}
			if count != 0 {
				t.Fatal("controller-specific workflow approval preferences table still exists")
			}
		})
	}
}
