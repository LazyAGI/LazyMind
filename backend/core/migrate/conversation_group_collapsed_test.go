package migrate

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"
)

func TestConversationGroupCollapsedMigration(t *testing.T) {
	for _, driver := range []string{"sqlite", "postgres"} {
		t.Run(driver, func(t *testing.T) {
			var db *sql.DB
			if driver == "sqlite" {
				db = openRawSQLite(t, filepath.Join(t.TempDir(), "collapsed.db"))
			} else {
				dsn := os.Getenv(migrationPostgresDSNEnv)
				if dsn == "" {
					t.Skip("PostgreSQL integration DSN required")
				}
				db = createTemporaryPostgresDatabase(t, dsn, "group_collapsed")
			}
			catalog, err := (&Runner{dir: "../migrations"}).loadCatalog()
			if err != nil {
				t.Fatal(err)
			}
			for _, mode := range catalog.Modes[:len(catalog.Modes)-1] {
				execMigrationFileForDriver(t, db, mode.Aggregate.UpPath, driver)
			}
			var added migrationFile
			for _, migration := range catalog.Modes[len(catalog.Modes)-1].Dev {
				if migration.FileVersion == 20260928084352 {
					added = migration
					continue
				}
				execMigrationFileForDriver(t, db, migration.UpPath, driver)
			}
			if added.UpPath == "" {
				t.Fatal("collapse migration missing")
			}
			exec := func(query string) {
				t.Helper()
				if _, err := db.Exec(query); err != nil {
					t.Fatal(err)
				}
			}
			exec(`INSERT INTO conversation_groups(id,user_id,name,normalized_name,scope,version,created_at,updated_at,sort_order) VALUES ('kept','u','Travel','travel','scope',7,CURRENT_TIMESTAMP,CURRENT_TIMESTAMP,5)`)
			for round := 0; round < 2; round++ {
				execMigrationFileForDriver(t, db, added.UpPath, driver)
				var collapsed bool
				if err := db.QueryRow("SELECT collapsed FROM conversation_groups WHERE id='kept'").Scan(&collapsed); err != nil || collapsed {
					t.Fatalf("default collapsed=%v err=%v", collapsed, err)
				}
				exec("UPDATE conversation_groups SET collapsed=TRUE WHERE id='kept'")
				if err := db.QueryRow("SELECT collapsed FROM conversation_groups WHERE id='kept'").Scan(&collapsed); err != nil || !collapsed {
					t.Fatalf("saved collapsed=%v err=%v", collapsed, err)
				}
				if _, err := db.Exec("UPDATE conversation_groups SET collapsed=NULL WHERE id='kept'"); err == nil {
					t.Fatal("NULL collapse state accepted")
				}
				execMigrationFileForDriver(t, db, added.DownPath, driver)
				var count int
				if err := db.QueryRow("SELECT COUNT(*) FROM conversation_groups WHERE id='kept' AND name='Travel' AND scope='scope' AND version=7 AND sort_order=5").Scan(&count); err != nil || count != 1 {
					t.Fatalf("group data lost: count=%d err=%v", count, err)
				}
				if _, err := db.Query("SELECT collapsed FROM conversation_groups"); err == nil {
					t.Fatal("down migration did not remove column")
				}
			}
		})
	}
}
