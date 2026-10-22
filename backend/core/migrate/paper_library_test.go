package migrate

import (
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const paperLibraryMigration = "../migrations/dev_mode/v0_3/20261021105921_create_paper_libraries"

func paperLibraryMigrationDB(t *testing.T, driver, label string) *sql.DB {
	t.Helper()
	if driver == "postgres" {
		dsn := strings.TrimSpace(os.Getenv(migrationPostgresDSNEnv))
		if dsn == "" {
			t.Skip("set MIGRATION_TEST_POSTGRES_DSN to an isolated test server")
		}
		return createTemporaryPostgresDatabase(t, dsn, "paper_library_"+label)
	}
	return openRawSQLite(t, filepath.Join(t.TempDir(), label+".db"))
}

func TestPaperLibraryMigrationRoundTrip(t *testing.T) {
	for _, driver := range []string{"sqlite", "postgres"} {
		t.Run(driver, func(t *testing.T) {
			db := paperLibraryMigrationDB(t, driver, "roundtrip")
			if _, err := db.Exec(`CREATE TABLE datasets (id VARCHAR(255) PRIMARY KEY, display_name TEXT NOT NULL);
INSERT INTO datasets VALUES ('existing-library','Existing papers');`); err != nil {
				t.Fatal(err)
			}
			for round := 0; round < 2; round++ {
				execMigrationFileForDriver(t, db, paperLibraryMigration+".up.sql", driver)
				assertPaperLibraryStorage(t, db)
				execMigrationFileForDriver(t, db, paperLibraryMigration+".down.sql", driver)
				var title string
				if err := db.QueryRow(`SELECT display_name FROM datasets WHERE id='existing-library'`).Scan(&title); err != nil || title != "Existing papers" {
					t.Fatalf("migration damaged existing dataset: %q %v", title, err)
				}
				if _, err := db.Exec(`SELECT dataset_id FROM paper_libraries`); err == nil {
					t.Fatal("down did not remove paper_libraries")
				}
			}
		})
	}
}

func assertPaperLibraryStorage(t *testing.T, db *sql.DB) {
	t.Helper()
	const payload = `{"schemaVersion":3,"notes":[{"content":"保留笔记"}]}`
	if _, err := db.Exec(`INSERT INTO paper_libraries (dataset_id,data_json,revision,created_at,updated_at)
VALUES ('existing-library','` + payload + `',7,CURRENT_TIMESTAMP,CURRENT_TIMESTAMP)`); err != nil {
		t.Fatal(err)
	}
	var stored string
	var revision int64
	if err := db.QueryRow(`SELECT data_json,revision FROM paper_libraries WHERE dataset_id='existing-library'`).Scan(&stored, &revision); err != nil || stored != payload || revision != 7 {
		t.Fatalf("library payload/revision lost: %s %d %v", stored, revision, err)
	}
	for _, values := range []string{
		`'existing-library','{}',0`,
		`NULL,'{}',0`,
		`'null-data',NULL,0`,
		`'null-revision','{}',NULL`,
		`'negative-revision','{}',-1`,
	} {
		if _, err := db.Exec(`INSERT INTO paper_libraries (dataset_id,data_json,revision,created_at,updated_at) VALUES (` + values + `,CURRENT_TIMESTAMP,CURRENT_TIMESTAMP)`); err == nil {
			t.Fatalf("invalid library row accepted: %s", values)
		}
	}
}

func TestPaperLibraryAggregateAndDevPathsMatch(t *testing.T) {
	catalog, err := (&Runner{dir: "../migrations"}).loadCatalog()
	if err != nil {
		t.Fatal(err)
	}
	for _, driver := range []string{"sqlite", "postgres"} {
		t.Run(driver, func(t *testing.T) {
			aggregate := paperLibraryMigrationDB(t, driver, "aggregate")
			dev := paperLibraryMigrationDB(t, driver, "dev")
			for i, mode := range catalog.Modes {
				if i == len(catalog.Modes)-1 {
					execMigrationFileForDriver(t, aggregate, mode.Aggregate.UpPath, driver)
					for _, migration := range mode.Dev {
						execMigrationFileForDriver(t, dev, migration.UpPath, driver)
					}
					break
				}
				for _, db := range []*sql.DB{aggregate, dev} {
					execMigrationFileForDriver(t, db, mode.Aggregate.UpPath, driver)
				}
			}
			for _, db := range []*sql.DB{aggregate, dev} {
				assertPaperLibraryStorage(t, db)
			}
			fingerprint := func(db *sql.DB) string {
				if driver == "postgres" {
					var lines []string
					for _, line := range postgresSchemaLines(t, db) {
						if strings.Contains(line, "|paper_libraries|") {
							lines = append(lines, line)
						}
					}
					return strings.Join(lines, "\n")
				}
				var ddl string
				if err := db.QueryRow(`SELECT sql FROM sqlite_master WHERE type='table' AND name='paper_libraries'`).Scan(&ddl); err != nil {
					t.Fatal(err)
				}
				return ddl
			}
			if fingerprint(aggregate) != fingerprint(dev) {
				t.Fatal("paper library schema differs between aggregate and dev paths")
			}
			execMigrationFileForDriver(t, aggregate, catalog.Modes[len(catalog.Modes)-1].Aggregate.DownPath, driver)
			if _, err := aggregate.Exec(`SELECT dataset_id FROM paper_libraries`); err == nil {
				t.Fatal("aggregate rollback did not remove paper_libraries")
			}
		})
	}
}
