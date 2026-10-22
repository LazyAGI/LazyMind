package migrate

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"
)

func TestAcademicWorkflowMigrationRoundTrip(t *testing.T) {
	for _, driver := range []string{"sqlite", "postgres"} {
		t.Run(driver, func(t *testing.T) {
			var db *sql.DB
			if driver == "sqlite" {
				db = openRawSQLite(t, filepath.Join(t.TempDir(), "papers.db"))
			} else {
				dsn := os.Getenv(migrationPostgresDSNEnv)
				if dsn == "" {
					t.Skip("PostgreSQL integration DSN required")
				}
				db = createTemporaryPostgresDatabase(t, dsn, "academic_workflows")
			}
			if _, err := db.Exec("CREATE TABLE datasets (id VARCHAR(255) PRIMARY KEY); INSERT INTO datasets(id) VALUES ('retained');"); err != nil {
				t.Fatal(err)
			}
			const base = "../migrations/dev_mode/v0_3/20261021091623_academic_workflows"
			execMigrationFileForDriver(t, db, base+".up.sql", driver)
			var scenario string
			if err := db.QueryRow("SELECT scenario_key FROM datasets WHERE id='retained'").Scan(&scenario); err != nil || scenario != "general" {
				t.Fatalf("legacy dataset: %s %v", scenario, err)
			}
			if _, err := db.Exec("INSERT INTO academic_user_paper_states(user_id,paper_id,is_important,updated_at) VALUES ('u','p',TRUE,CURRENT_TIMESTAMP),('other','p',FALSE,CURRENT_TIMESTAMP)"); err != nil {
				t.Fatal(err)
			}
			execMigrationFileForDriver(t, db, base+".down.sql", driver)
			var id string
			if err := db.QueryRow("SELECT id FROM datasets").Scan(&id); err != nil || id != "retained" {
				t.Fatal("down lost existing data", err)
			}
		})
	}
}
