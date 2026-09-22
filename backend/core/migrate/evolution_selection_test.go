package migrate

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"
)

func TestEvolutionSelectionMigrationRoundTrip(t *testing.T) {
	for _, driver := range []string{"sqlite", "postgres"} {
		t.Run(driver, func(t *testing.T) {
			var db *sql.DB
			if driver == "sqlite" {
				db = openRawSQLite(t, filepath.Join(t.TempDir(), "evolution.db"))
			} else {
				dsn := os.Getenv(migrationPostgresDSNEnv)
				if dsn == "" {
					t.Skip("PostgreSQL integration DSN required")
				}
				db = createTemporaryPostgresDatabase(t, dsn, "evolution")
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
				if migration.FileVersion == 20260918033746 {
					target = migration
				} else {
					execMigrationFileForDriver(t, db, migration.UpPath, driver)
				}
			}
			if target.UpPath == "" {
				t.Fatal("missing migration")
			}
			if _, err := db.Exec(`INSERT INTO agent_threads(thread_id,create_user_id,status,thread_payload,created_at,updated_at) VALUES ('retained','u','running','{}',CURRENT_TIMESTAMP,CURRENT_TIMESTAMP)`); err != nil {
				t.Fatal(err)
			}
			for round := 0; round < 2; round++ {
				execMigrationFileForDriver(t, db, target.UpPath, driver)
				var observed sql.NullTime
				if err := db.QueryRow(`SELECT status_observed_at FROM agent_threads WHERE thread_id='retained'`).Scan(&observed); err != nil || observed.Valid {
					t.Fatalf("legacy timestamp fabricated: %v %v", observed, err)
				}
				if _, err := db.Exec(`INSERT INTO evolution_model_validations(model_ref,validation_version,evidence_id,passed,verified_at,expires_at) VALUES ('test-ref','test-version','test-evidence',TRUE,CURRENT_TIMESTAMP,CURRENT_TIMESTAMP)`); err != nil {
					t.Fatal(err)
				}
				execMigrationFileForDriver(t, db, target.DownPath, driver)
				var status string
				if err := db.QueryRow(`SELECT status FROM agent_threads WHERE thread_id='retained'`).Scan(&status); err != nil || status != "running" {
					t.Fatalf("lost existing task: %q %v", status, err)
				}
			}
		})
	}
}
