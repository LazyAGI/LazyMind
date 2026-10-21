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
			for _, db := range []*sql.DB{aggregate, dev} {
				if _, err := db.Exec("INSERT INTO user_ui_preferences (user_id, created_at, updated_at) VALUES ('existing', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)"); err != nil {
					t.Fatal(err)
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
			for _, db := range []*sql.DB{aggregate, dev} {
				var enabled bool
				if err := db.QueryRow("SELECT document_directories_enabled FROM user_ui_preferences WHERE user_id='existing'").Scan(&enabled); err != nil || !enabled {
					t.Fatalf("existing user default: %v %v", enabled, err)
				}
				if _, err := db.Exec("UPDATE user_ui_preferences SET document_directories_enabled=FALSE WHERE user_id='existing'; INSERT INTO user_ui_preferences (user_id, created_at, updated_at) VALUES ('new', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)"); err != nil {
					t.Fatal(err)
				}
				if err := db.QueryRow("SELECT document_directories_enabled FROM user_ui_preferences WHERE user_id='new'").Scan(&enabled); err != nil || !enabled {
					t.Fatalf("new user default: %v %v", enabled, err)
				}
				if err := db.QueryRow("SELECT document_directories_enabled FROM user_ui_preferences WHERE user_id='existing'").Scan(&enabled); err != nil || enabled {
					t.Fatalf("explicit false not preserved: %v %v", enabled, err)
				}
			}
			a, b := directoryColumns(t, aggregate, dialect), directoryColumns(t, dev, dialect)
			if len(a) == 0 || strings.Join(a, "\n") != strings.Join(b, "\n") {
				t.Fatalf("directory schema mismatch: aggregate=%v dev=%v", a, b)
			}
			applySQL(t, aggregate, strings.Replace(path, ".up.sql", ".down.sql", 1), dialect)
			if len(directoryColumns(t, aggregate, dialect)) != 0 {
				t.Fatal("directory tables survived aggregate rollback")
			}
			for _, name := range []string{"add_document_directories_control", "document_folders", "document_directories"} {
				applySQL(t, dev, migrationPath(t, "dev_mode/v0_3/*_"+name+".down.sql"), dialect)
			}
			if len(directoryColumns(t, dev, dialect)) != 0 {
				t.Fatal("directory tables survived dev rollback")
			}
			for _, db := range []*sql.DB{aggregate, dev} {
				var users int
				if err := db.QueryRow("SELECT count(*) FROM user_ui_preferences WHERE user_id IN ('existing', 'new')").Scan(&users); err != nil || users != 2 {
					t.Fatalf("rollback lost preferences: %d %v", users, err)
				}
				if _, err := db.Query("SELECT document_directories_enabled FROM user_ui_preferences"); err == nil {
					t.Fatal("control survived rollback")
				}
				var title string
				if err := db.QueryRow("SELECT title FROM directory_migration_existing WHERE id=1").Scan(&title); err != nil || title != "keep" {
					t.Fatalf("existing data changed after upgrade and rollback: title=%q err=%v", title, err)
				}
			}
		})
	}
}
