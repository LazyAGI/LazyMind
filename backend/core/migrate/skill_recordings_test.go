package migrate

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"
)

func TestSkillRecordingsMigrationRoundTrip(t *testing.T) {
	for _, driver := range []string{"sqlite", "postgres"} {
		t.Run(driver, func(t *testing.T) {
			var db *sql.DB
			if driver == "sqlite" {
				db = openRawSQLite(t, filepath.Join(t.TempDir(), "recording.db"))
			} else {
				dsn := os.Getenv(migrationPostgresDSNEnv)
				if dsn == "" {
					t.Skip("PostgreSQL integration DSN required")
				}
				db = createTemporaryPostgresDatabase(t, dsn, "recordings")
			}
			up := "../migrations/dev_mode/v0_3/20260916062006_create_skill_recordings.up.sql"
			down := "../migrations/dev_mode/v0_3/20260916062006_create_skill_recordings.down.sql"
			if _, err := db.Exec("CREATE TABLE preserved_recording_test (value TEXT); INSERT INTO preserved_recording_test VALUES ('keep')"); err != nil {
				t.Fatal(err)
			}
			for i := 0; i < 2; i++ {
				execMigrationFileForDriver(t, db, up, driver)
				execMigrationFileForDriver(t, db, up, driver)
				insert := `INSERT INTO skill_recordings(id,user_id,conversation_id,status,created_at,updated_at) VALUES ('r','u','c','generating',CURRENT_TIMESTAMP,CURRENT_TIMESTAMP)`
				if _, err := db.Exec(insert); err != nil {
					t.Fatal(err)
				}
				if _, err := db.Exec(`INSERT INTO skill_recordings(id,user_id,conversation_id,status,created_at,updated_at) VALUES ('duplicate','u','c','generating',CURRENT_TIMESTAMP,CURRENT_TIMESTAMP)`); err == nil {
					t.Fatal("allowed concurrent generation for the same account")
				}
				execMigrationFileForDriver(t, db, down, driver)
				var value string
				if err := db.QueryRow("SELECT value FROM preserved_recording_test").Scan(&value); err != nil || value != "keep" {
					t.Fatalf("migration lost unrelated data: %v", err)
				}
			}
		})
	}
}
