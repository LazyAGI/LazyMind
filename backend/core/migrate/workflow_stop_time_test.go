package migrate

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"
)

func TestWorkflowStopTimeMigrationRoundTrip(t *testing.T) {
	for _, driver := range []string{"sqlite", "postgres"} {
		t.Run(driver, func(t *testing.T) {
			var db *sql.DB
			if driver == "sqlite" {
				db = openRawSQLite(t, filepath.Join(t.TempDir(), "workflow-stop.db"))
			} else {
				dsn := os.Getenv(migrationPostgresDSNEnv)
				if dsn == "" {
					t.Skip("PostgreSQL integration DSN required")
				}
				db = createTemporaryPostgresDatabase(t, dsn, "workflow_stop")
			}
			catalog, err := (&Runner{dir: "../migrations"}).loadCatalog()
			if err != nil {
				t.Fatal(err)
			}
			for _, mode := range catalog.Modes[:len(catalog.Modes)-1] {
				execMigrationFileForDriver(t, db, mode.Aggregate.UpPath, driver)
			}
			var target migrationFile
			for _, migration := range catalog.Modes[len(catalog.Modes)-1].Dev {
				if migration.FileVersion == 20260912065741 {
					target = migration
				} else {
					execMigrationFileForDriver(t, db, migration.UpPath, driver)
				}
			}
			if target.UpPath == "" {
				t.Fatal("missing migration")
			}
			if _, err := db.Exec(`INSERT INTO plugin_sessions(id,conversation_id,plugin_id,status,created_at,updated_at) VALUES ('kept','conv','writer','waiting',CURRENT_TIMESTAMP,CURRENT_TIMESTAMP)`); err != nil {
				t.Fatal(err)
			}
			for round := 0; round < 2; round++ {
				execMigrationFileForDriver(t, db, target.UpPath, driver)
				var saved any
				if err := db.QueryRow(`SELECT last_stopped_at FROM plugin_sessions WHERE id='kept'`).Scan(&saved); err != nil || saved != nil {
					t.Fatalf("legacy stop=%v err=%v", saved, err)
				}
				if _, err := db.Exec(`UPDATE plugin_sessions SET last_stopped_at=CURRENT_TIMESTAMP WHERE id='kept'`); err != nil {
					t.Fatal(err)
				}
				execMigrationFileForDriver(t, db, target.DownPath, driver)
				var title string
				var status string
				if err := db.QueryRow(`SELECT plugin_id,status FROM plugin_sessions WHERE id='kept'`).Scan(&title, &status); err != nil || title != "writer" || status != "waiting" {
					t.Fatalf("lost data: %q %q %v", title, status, err)
				}
			}
		})
	}
}
