package migrate

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"
)

func TestArchiveFolderKindsMigration(t *testing.T) {
	for _, driver := range []string{"sqlite", "postgres"} {
		t.Run(driver, func(t *testing.T) {
			var db *sql.DB
			if driver == "sqlite" {
				db = openRawSQLite(t, t.TempDir()+"/archive.db")
			} else {
				dsn := os.Getenv(migrationPostgresDSNEnv)
				if dsn == "" {
					t.Skip("PostgreSQL DSN not configured")
				}
				db = createTemporaryPostgresDatabase(t, dsn, "archive_kinds")
			}
			exec := func(q string) {
				t.Helper()
				if _, err := db.Exec(q); err != nil {
					t.Fatal(err)
				}
			}
			exec(`CREATE TABLE conversation_archive_folders(id VARCHAR(36) PRIMARY KEY,user_id VARCHAR(255) NOT NULL,name VARCHAR(255) NOT NULL,normalized_name VARCHAR(255) NOT NULL,created_at TIMESTAMP NOT NULL,updated_at TIMESTAMP NOT NULL,CONSTRAINT uk_conversation_archive_folders_user_name UNIQUE(user_id,normalized_name));
CREATE TABLE conversations(id VARCHAR(36) PRIMARY KEY,create_user_id VARCHAR(255),is_task_conv BOOLEAN,archive_folder_id VARCHAR(36));
INSERT INTO conversation_archive_folders VALUES('mixed','u','Research','research',CURRENT_TIMESTAMP,CURRENT_TIMESTAMP),('empty','u','Empty','empty',CURRENT_TIMESTAMP,CURRENT_TIMESTAMP);
INSERT INTO conversations VALUES('dialog','u',FALSE,'mixed'),('task','u',TRUE,'mixed');`)
			paths, err := filepath.Glob("../migrations/dev_mode/v0_3/*_isolate_archive_folder_kinds.up.sql")
			if err != nil || len(paths) != 1 {
				t.Fatalf("migration files: %v %v", paths, err)
			}
			scalar := func(q string, want int) {
				t.Helper()
				var got int
				if err := db.QueryRow(q).Scan(&got); err != nil {
					t.Fatal(err)
				}
				if got != want {
					t.Fatalf("%s: %d != %d", q, got, want)
				}
			}
			execMigrationFileForDriver(t, db, paths[0], driver)
			scalar("SELECT COUNT(*) FROM conversation_archive_folders", 3)
			scalar("SELECT COUNT(*) FROM conversations c JOIN conversation_archive_folders f ON c.archive_folder_id=f.id WHERE c.is_task_conv<>f.is_task_conv", 0)
			scalar("SELECT COUNT(*) FROM conversation_archive_folders WHERE id='empty' AND is_task_conv=FALSE", 1)
			execMigrationFileForDriver(t, db, paths[0][:len(paths[0])-len("up.sql")]+"down.sql", driver)
			scalar("SELECT COUNT(*) FROM conversation_archive_folders", 2)
			scalar("SELECT COUNT(*) FROM conversations WHERE archive_folder_id='mixed'", 2)
			execMigrationFileForDriver(t, db, paths[0], driver)
			scalar("SELECT COUNT(*) FROM conversations c JOIN conversation_archive_folders f ON c.archive_folder_id=f.id WHERE c.is_task_conv<>f.is_task_conv", 0)
		})
	}
}
