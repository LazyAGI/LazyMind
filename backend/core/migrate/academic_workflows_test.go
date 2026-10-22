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

func TestPaperSourceMigrationPreservesCachesAndRejectsLossyDown(t *testing.T) {
	for _, driver := range []string{"sqlite", "postgres"} {
		t.Run(driver, func(t *testing.T) {
			var db *sql.DB
			if driver == "sqlite" {
				db = openRawSQLite(t, filepath.Join(t.TempDir(), "sources.db"))
			} else {
				dsn := os.Getenv(migrationPostgresDSNEnv)
				if dsn == "" {
					t.Skip("PostgreSQL integration DSN required")
				}
				db = createTemporaryPostgresDatabase(t, dsn, "paper_sources")
			}
			if _, err := db.Exec("CREATE TABLE datasets (id VARCHAR(255) PRIMARY KEY)"); err != nil {
				t.Fatal(err)
			}
			execMigrationFileForDriver(t, db, "../migrations/dev_mode/v0_3/20261021091623_academic_workflows.up.sql", driver)
			if _, err := db.Exec("INSERT INTO academic_paper_contents(id,user_id,paper_id,status,pages_json,updated_at) VALUES ('old','u','paper','ready','[]',CURRENT_TIMESTAMP)"); err != nil {
				t.Fatal(err)
			}
			const base = "../migrations/dev_mode/v0_3/20261022072101_paper_sources"
			execMigrationFileForDriver(t, db, base+".up.sql", driver)
			var kind string
			if err := db.QueryRow("SELECT source_kind FROM academic_paper_contents WHERE id='old'").Scan(&kind); err != nil || kind != "legacy" {
				t.Fatal("old cache lost its provenance fence", kind, err)
			}
			if _, err := db.Exec("INSERT INTO academic_paper_contents(id,user_id,paper_id,status,pages_json,source_kind,source_key,source_version,updated_at) VALUES ('new','u','paper','ready','[]','external','arxiv-v2','v2',CURRENT_TIMESTAMP)"); err != nil {
				t.Fatal(err)
			}
			// The first statement is intentionally the old unique constraint. It
			// must fail before any source columns or cache records can be dropped.
			if _, err := db.Exec("CREATE UNIQUE INDEX uk_academic_content_downgrade ON academic_paper_contents(user_id,paper_id,parser_version)"); err == nil {
				t.Fatal("lossy downgrade accepted")
			}
			var count int
			if err := db.QueryRow("SELECT COUNT(*) FROM academic_paper_contents").Scan(&count); err != nil || count != 2 {
				t.Fatal("downgrade check lost data", count, err)
			}
			if _, err := db.Exec("DELETE FROM academic_paper_contents WHERE id='new'"); err != nil {
				t.Fatal(err)
			}
			execMigrationFileForDriver(t, db, base+".down.sql", driver)
			if err := db.QueryRow("SELECT COUNT(*) FROM academic_paper_contents WHERE id='old'").Scan(&count); err != nil || count != 1 {
				t.Fatal("down lost legacy cache", count, err)
			}
		})
	}
}
