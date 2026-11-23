package migrations_test

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestPPTPageRevisionMigration(t *testing.T) {
	for _, dialect := range []string{"sqlite", "postgres"} {
		for _, mode := range []string{"dev", "version"} {
			t.Run(dialect+"/"+mode, func(t *testing.T) {
				db := openDatabase(t, dialect)
				for _, version := range []string{"v0_1", "v0_2"} {
					applySQL(t, db, migrationPath(t, "version_mode/"+version+"/*.up.sql"), dialect)
				}
				up := migrationPath(t, "version_mode/v0_3/*.up.sql")
				if mode == "dev" {
					up = migrationPath(t, "dev_mode/v0_3/*_ppt_page_revision.up.sql")
					paths, err := filepath.Glob("dev_mode/v0_3/*.up.sql")
					if err != nil {
						t.Fatal(err)
					}
					for _, path := range paths {
						if path == up {
							break
						}
						applySQL(t, db, path, dialect)
					}
				}
				applySQL(t, db, up, dialect)
				if _, err := db.Exec("SELECT ppt_notes_revision_id FROM plugin_slot_revisions"); err != nil {
					t.Fatal(err)
				}
				applySQL(t, db, strings.Replace(up, ".up.sql", ".down.sql", 1), dialect)
				if _, err := db.Exec("SELECT ppt_notes_revision_id FROM plugin_slot_revisions"); err == nil {
					t.Fatal("page revision association survived rollback")
				}
			})
		}
	}
}
