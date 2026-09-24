package migrate

import (
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDeletedProjectDirectoryMigration(t *testing.T) {
	for _, driver := range []string{"sqlite", "postgres"} {
		t.Run(driver, func(t *testing.T) {
			var db *sql.DB
			if driver == "sqlite" {
				db = openRawSQLite(t, t.TempDir()+"/projects.db")
			} else {
				dsn := os.Getenv(migrationPostgresDSNEnv)
				if dsn == "" {
					t.Skip("PostgreSQL DSN not configured")
				}
				db = createTemporaryPostgresDatabase(t, dsn, "deleted_projects")
			}
			exec := func(query string) {
				t.Helper()
				if _, err := db.Exec(query); err != nil {
					t.Fatal(err)
				}
			}
			exec(`CREATE TABLE conversation_groups(id TEXT PRIMARY KEY,user_id TEXT,kind TEXT,project_path TEXT,deleted_at TIMESTAMP);
CREATE UNIQUE INDEX uk_conversation_projects_user_path ON conversation_groups(user_id,project_path);
INSERT INTO conversation_groups VALUES('old','u','project','/work',CURRENT_TIMESTAMP);`)
			paths, err := filepath.Glob("../migrations/dev_mode/v0_3/*_release_deleted_project_directories.up.sql")
			if err != nil || len(paths) != 1 {
				t.Fatalf("migration: %v %v", paths, err)
			}
			execMigrationFileForDriver(t, db, paths[0], driver)
			exec("INSERT INTO conversation_groups VALUES('new','u','project','/work',NULL)")
			if _, err := db.Exec("INSERT INTO conversation_groups VALUES('duplicate','u','project','/work',NULL)"); err == nil {
				t.Fatal("two active projects accepted")
			}
			if _, err := db.Exec("UPDATE conversation_groups SET deleted_at=NULL WHERE id='old'"); err == nil {
				t.Fatal("conflicting restore accepted")
			}
			downPath := strings.TrimSuffix(paths[0], "up.sql") + "down.sql"
			body, err := os.ReadFile(downPath)
			if err != nil {
				t.Fatal(err)
			}
			tx, err := db.Begin()
			if err != nil {
				t.Fatal(err)
			}
			if _, err := tx.Exec(string(body)); err == nil {
				tx.Rollback()
				t.Fatal("destructive rollback accepted duplicate history")
			}
			if err := tx.Rollback(); err != nil {
				t.Fatal(err)
			}
			var count int
			if err := db.QueryRow("SELECT COUNT(*) FROM conversation_groups").Scan(&count); err != nil || count != 2 {
				t.Fatalf("history lost: %d %v", count, err)
			}
			if _, err := db.Exec("INSERT INTO conversation_groups VALUES('duplicate','u','project','/work',NULL)"); err == nil {
				t.Fatal("failed rollback removed active uniqueness")
			}
			// A database without duplicate historical paths can downgrade and upgrade.
			exec("DELETE FROM conversation_groups WHERE id='new'")
			execMigrationFileForDriver(t, db, downPath, driver)
			if _, err := db.Exec("INSERT INTO conversation_groups VALUES('new','u','project','/work',NULL)"); err == nil {
				t.Fatal("old uniqueness was not restored")
			}
			execMigrationFileForDriver(t, db, paths[0], driver)
			exec("INSERT INTO conversation_groups VALUES('new','u','project','/work',NULL)")
		})
	}
}
