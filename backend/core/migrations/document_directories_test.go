package migrations_test

import (
	"database/sql"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

func directoryColumns(t *testing.T, db *sql.DB, dialect string) []string {
	t.Helper()
	columns := []string{}
	for _, table := range []string{"document_directories", "document_directory_groups", "document_directory_entries", "document_observations", "document_directory_batches", "document_folder_inventories", "document_folder_nodes"} {
		if dialect == "sqlite" {
			rows, err := db.Query("PRAGMA table_info(" + table + ")")
			if err != nil {
				t.Fatal(err)
			}
			for rows.Next() {
				var cid, nn, pk int
				var name, kind string
				var def sql.NullString
				if err := rows.Scan(&cid, &name, &kind, &nn, &def, &pk); err != nil {
					t.Fatal(err)
				}
				columns = append(columns, table+":"+name+":"+kind)
			}
			rows.Close()
		} else {
			rows, err := db.Query("SELECT column_name,data_type FROM information_schema.columns WHERE table_schema='public' AND table_name=$1 ORDER BY ordinal_position", table)
			if err != nil {
				t.Fatal(err)
			}
			for rows.Next() {
				var name, kind string
				if err := rows.Scan(&name, &kind); err != nil {
					t.Fatal(err)
				}
				columns = append(columns, table+":"+name+":"+kind)
			}
			rows.Close()
		}
	}
	return columns
}
func TestDirectoryAggregateMatchesDevAndRollsBack(t *testing.T) {
	for _, dialect := range []string{"sqlite", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			aggregate, dev := openDatabase(t, dialect), openDatabase(t, dialect)
			for _, db := range []*sql.DB{aggregate, dev} {
				if _, err := db.Exec("CREATE TABLE directory_migration_existing (id INTEGER PRIMARY KEY, title TEXT); INSERT INTO directory_migration_existing VALUES (1, 'keep');"); err != nil {
					t.Fatal(err)
				}
				for _, version := range []string{"v0_1", "v0_2"} {
					applySQL(t, db, migrationPath(t, "version_mode/"+version+"/*.up.sql"), dialect)
				}
			}
			path := migrationPath(t, "version_mode/v0_3/*.up.sql")
			applySQL(t, aggregate, path, dialect)
			paths, err := filepath.Glob("dev_mode/v0_3/*.up.sql")
			if err != nil {
				t.Fatal(err)
			}
			sort.Strings(paths)
			for _, p := range paths {
				applySQL(t, dev, p, dialect)
			}
			a, b := directoryColumns(t, aggregate, dialect), directoryColumns(t, dev, dialect)
			if len(a) == 0 || strings.Join(a, "\n") != strings.Join(b, "\n") {
				t.Fatalf("directory schema mismatch: aggregate=%v dev=%v", a, b)
			}
			applySQL(t, aggregate, strings.Replace(path, ".up.sql", ".down.sql", 1), dialect)
			if len(directoryColumns(t, aggregate, dialect)) != 0 {
				t.Fatal("directory tables survived aggregate rollback")
			}
			for _, name := range []string{"document_folders", "document_directories"} {
				applySQL(t, dev, migrationPath(t, "dev_mode/v0_3/*_"+name+".down.sql"), dialect)
			}
			if len(directoryColumns(t, dev, dialect)) != 0 {
				t.Fatal("directory tables survived dev rollback")
			}
			for _, db := range []*sql.DB{aggregate, dev} {
				var title string
				if err := db.QueryRow("SELECT title FROM directory_migration_existing WHERE id=1").Scan(&title); err != nil || title != "keep" {
					t.Fatalf("existing data changed after upgrade and rollback: title=%q err=%v", title, err)
				}
			}
		})
	}
}
