package migrations_test

import (
	"database/sql"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

func TestPaperNoteSourceMigrationMatchesReleaseAndRollsBack(t *testing.T) {
	for _, dialect := range []string{"sqlite", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			dev, release := openDatabase(t, dialect), openDatabase(t, dialect)
			for _, version := range []string{"v0_1", "v0_2"} {
				up := migrationPath(t, "version_mode/"+version+"/*.up.sql")
				applySQL(t, dev, up, dialect)
				applySQL(t, release, up, dialect)
			}
			up := migrationPath(t, "dev_mode/v0_3/*_add_paper_note_source.up.sql")
			paths, _ := filepath.Glob("dev_mode/v0_3/*.up.sql")
			sort.Strings(paths)
			for _, path := range paths {
				applySQL(t, dev, path, dialect)
			}
			releaseUp := migrationPath(t, "version_mode/v0_3/*.up.sql")
			applySQL(t, release, releaseUp, dialect)
			for _, db := range []struct {
				name   string
				handle *sql.DB
			}{{"dev", dev}, {"release", release}} {
				rows, err := db.handle.Query("SELECT source_json FROM paper_library_notes WHERE 1=0")
				if err != nil {
					t.Fatalf("%s path missing note source: %v", db.name, err)
				}
				_ = rows.Close()
			}
			applySQL(t, dev, strings.Replace(up, ".up.sql", ".down.sql", 1), dialect)
			if rows, err := dev.Query("SELECT source_json FROM paper_library_notes WHERE 1=0"); err == nil {
				_ = rows.Close()
				t.Fatal("note source column survived dev rollback")
			}
		})
	}
}
