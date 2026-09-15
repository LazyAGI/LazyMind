package migrate

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"
)

func TestConversationProjectMigrationPreservesHistoryAndConstraints(t *testing.T) {
	for _, driver := range []string{"sqlite", "postgres"} {
		t.Run(driver, func(t *testing.T) {
			var db *sql.DB
			if driver == "sqlite" {
				db = openRawSQLite(t, filepath.Join(t.TempDir(), "projects.db"))
			} else {
				dsn := os.Getenv(migrationPostgresDSNEnv)
				if dsn == "" {
					t.Skip("PostgreSQL integration DSN required")
				}
				db = createTemporaryPostgresDatabase(t, dsn, "projects_roundtrip")
			}
			catalog, err := (&Runner{dir: "../migrations"}).loadCatalog()
			if err != nil {
				t.Fatal(err)
			}
			for _, mode := range catalog.Modes[:len(catalog.Modes)-1] {
				execMigrationFileForDriver(t, db, mode.Aggregate.UpPath, driver)
			}
			var target migrationFile
			for _, migration := range catalog.Modes[len(catalog.Modes)-1].Dev {
				if migration.FileVersion == 20260915065847 {
					target = migration
				} else {
					execMigrationFileForDriver(t, db, migration.UpPath, driver)
				}
			}
			if target.UpPath == "" {
				t.Fatal("project migration missing")
			}
			if _, err := db.Exec(`INSERT INTO conversation_groups(id,user_id,name,normalized_name,created_at,updated_at) VALUES('old','u','Same','same',CURRENT_TIMESTAMP,CURRENT_TIMESTAMP)`); err != nil {
				t.Fatal(err)
			}
			execMigrationFileForDriver(t, db, target.UpPath, driver)
			var kind string
			if err := db.QueryRow(`SELECT kind FROM conversation_groups WHERE id='old'`).Scan(&kind); err != nil || kind != "group" {
				t.Fatalf("history changed: %q %v", kind, err)
			}
			for _, query := range []string{
				`INSERT INTO conversation_groups(id,user_id,name,normalized_name,kind,project_path,created_at,updated_at) VALUES('p1','u','Same','same','project','/a',CURRENT_TIMESTAMP,CURRENT_TIMESTAMP)`,
				`INSERT INTO conversation_groups(id,user_id,name,normalized_name,kind,project_path,created_at,updated_at) VALUES('p2','u','Same','same','project','/b',CURRENT_TIMESTAMP,CURRENT_TIMESTAMP)`,
			} {
				if _, err := db.Exec(query); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := db.Exec(`UPDATE conversation_groups SET project_path='/a' WHERE id='p2'`); err == nil {
				t.Fatal("duplicate directory accepted")
			}
			if _, err := db.Exec(`INSERT INTO conversation_groups(id,user_id,name,normalized_name,created_at,updated_at) VALUES('duplicate','u','Same','same',CURRENT_TIMESTAMP,CURRENT_TIMESTAMP)`); err == nil {
				t.Fatal("duplicate group accepted")
			}
			execMigrationFileForDriver(t, db, target.DownPath, driver)
			var count int
			if err := db.QueryRow(`SELECT COUNT(*) FROM conversation_groups`).Scan(&count); err != nil || count != 3 {
				t.Fatalf("rollback lost records: %d %v", count, err)
			}
			execMigrationFileForDriver(t, db, target.UpPath, driver)
		})
	}
}
