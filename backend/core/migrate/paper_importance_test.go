package migrate

import (
	"database/sql"
	"testing"
)

const paperImportanceMigration = "../migrations/dev_mode/v0_3/20261026115739_scope_paper_importance_to_library"

func assertScopedImportanceStorage(t *testing.T, db *sql.DB) {
	t.Helper()
	if _, err := db.Exec(`INSERT INTO academic_user_paper_states(user_id,dataset_id,paper_id,is_important,updated_at)
VALUES ('u','first','paper',TRUE,CURRENT_TIMESTAMP),('u','second','paper',FALSE,CURRENT_TIMESTAMP),('other','first','paper',FALSE,CURRENT_TIMESTAMP)`); err != nil {
		t.Fatal(err)
	}
	var important bool
	if err := db.QueryRow(`SELECT is_important FROM academic_user_paper_states WHERE user_id='u' AND dataset_id='second' AND paper_id='paper'`).Scan(&important); err != nil || important {
		t.Fatalf("library importance was not stored independently: %v %v", important, err)
	}
	if _, err := db.Exec(`INSERT INTO academic_user_paper_states(user_id,dataset_id,paper_id,updated_at) VALUES ('u','first','paper',CURRENT_TIMESTAMP)`); err == nil {
		t.Fatal("duplicate mark accepted within one user and library")
	}
}

func TestPaperImportanceMigrationRoundTrip(t *testing.T) {
	for _, driver := range []string{"sqlite", "postgres"} {
		t.Run(driver, func(t *testing.T) {
			db := paperLibraryMigrationDB(t, driver, "importance")
			if _, err := db.Exec(`CREATE TABLE datasets (id VARCHAR(255) PRIMARY KEY, display_name TEXT NOT NULL);
INSERT INTO datasets VALUES ('existing-library','Existing papers');`); err != nil {
				t.Fatal(err)
			}
			execMigrationFileForDriver(t, db, "../migrations/dev_mode/v0_3/20261021091623_academic_workflows.up.sql", driver)
			execMigrationFileForDriver(t, db, paperLibraryMigration+".up.sql", driver)
			assertPaperLibraryStorage(t, db)
			for round := 0; round < 2; round++ {
				if _, err := db.Exec(`INSERT INTO academic_user_paper_states(user_id,paper_id,is_important,updated_at) VALUES ('legacy','paper',TRUE,CURRENT_TIMESTAMP)`); err != nil {
					t.Fatal(err)
				}
				execMigrationFileForDriver(t, db, paperImportanceMigration+".up.sql", driver)
				var count int
				if err := db.QueryRow(`SELECT COUNT(*) FROM academic_user_paper_states`).Scan(&count); err != nil || count != 0 {
					t.Fatalf("old global importance was not cleared: %d %v", count, err)
				}
				assertScopedImportanceStorage(t, db)
				execMigrationFileForDriver(t, db, paperImportanceMigration+".down.sql", driver)
				if _, err := db.Exec(`SELECT dataset_id FROM academic_user_paper_states`); err == nil {
					t.Fatal("down did not restore the former importance schema")
				}
				var title, data string
				var revision int64
				if err := db.QueryRow(`SELECT display_name FROM datasets WHERE id='existing-library'`).Scan(&title); err != nil || title != "Existing papers" {
					t.Fatalf("migration damaged the library: %q %v", title, err)
				}
				if err := db.QueryRow(`SELECT data_json,revision FROM paper_libraries WHERE dataset_id='existing-library'`).Scan(&data, &revision); err != nil || data != `{"schemaVersion":3,"notes":[{"content":"保留笔记"}]}` || revision != 7 {
					t.Fatalf("migration damaged library contents: %s %d %v", data, revision, err)
				}
			}
		})
	}
}
