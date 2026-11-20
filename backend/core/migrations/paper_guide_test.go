package migrations_test

import (
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

func TestPaperGuideMigrationAndAggregate(t *testing.T) {
	for _, dialect := range []string{"sqlite", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			for _, aggregate := range []bool{false, true} {
				db := openDatabase(t, dialect)
				for _, version := range []string{"v0_1", "v0_2"} {
					applySQL(t, db, migrationPath(t, "version_mode/"+version+"/*.up.sql"), dialect)
				}
				if _, err := db.Exec("CREATE TABLE paper_guide_existing (id INTEGER PRIMARY KEY, value TEXT); INSERT INTO paper_guide_existing VALUES (1,'preserved')"); err != nil {
					t.Fatal(err)
				}
				up := migrationPath(t, "dev_mode/v0_3/*_create_paper_guides.up.sql")
				if aggregate {
					up = migrationPath(t, "version_mode/v0_3/*.up.sql")
					applySQL(t, db, up, dialect)
				} else {
					paths, _ := filepath.Glob("dev_mode/v0_3/*.up.sql")
					sort.Strings(paths)
					for _, p := range paths {
						applySQL(t, db, p, dialect)
					}
				}
				if _, err := db.Exec("INSERT INTO paper_guides(id,scope_key,owner_id,dataset_id,document_id,language) VALUES ('g','s','u','d','p','zh')"); err != nil {
					t.Fatal(err)
				}
				var value string
				if err := db.QueryRow("SELECT scope_key FROM paper_guides WHERE id='g'").Scan(&value); err != nil || value != "s" {
					t.Fatalf("schema/data: %s %v", value, err)
				}
				applySQL(t, db, strings.Replace(up, ".up.sql", ".down.sql", 1), dialect)
				if err := db.QueryRow("SELECT value FROM paper_guide_existing WHERE id=1").Scan(&value); err != nil || value != "preserved" {
					t.Fatalf("existing data lost: %s %v", value, err)
				}
			}
		})
	}
}
