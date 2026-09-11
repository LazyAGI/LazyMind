package migrate

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"
)

func TestConversationGroupMigrationsRoundTrip(t *testing.T) {
	for _, driver := range []string{"sqlite", "postgres"} {
		t.Run(driver, func(t *testing.T) {
			var db *sql.DB
			if driver == "sqlite" {
				db = openRawSQLite(t, filepath.Join(t.TempDir(), "groups.db"))
			} else {
				dsn := os.Getenv(migrationPostgresDSNEnv)
				if dsn == "" {
					t.Skip("PostgreSQL integration DSN required")
				}
				db = createTemporaryPostgresDatabase(t, dsn, "groups_roundtrip")
			}
			catalog, err := (&Runner{dir: "../migrations"}).loadCatalog()
			if err != nil {
				t.Fatal(err)
			}
			for _, mode := range catalog.Modes[:len(catalog.Modes)-1] {
				execMigrationFileForDriver(t, db, mode.Aggregate.UpPath, driver)
			}
			var groupMigrations []migrationFile
			for _, migration := range catalog.Modes[len(catalog.Modes)-1].Dev {
				switch migration.FileVersion {
				case 20260910050131:
					groupMigrations = append(groupMigrations, migration)
				default:
					execMigrationFileForDriver(t, db, migration.UpPath, driver)
				}
			}
			if len(groupMigrations) != 1 {
				t.Fatalf("expected one conversation group migration, got %d", len(groupMigrations))
			}
			if _, err := db.Exec(`INSERT INTO conversations(id,display_name,channel_id,create_user_id,create_user_name,created_at,updated_at) VALUES ('retained','Retained','default','u','User',CURRENT_TIMESTAMP,CURRENT_TIMESTAMP)`); err != nil {
				t.Fatal(err)
			}
			for round := 0; round < 2; round++ {
				for _, migration := range groupMigrations {
					execMigrationFileForDriver(t, db, migration.UpPath, driver)
				}
				if _, err := db.Exec(`INSERT INTO conversation_organizer_runs(id,user_id,status,snapshot_json,snapshot_hash,model_config_json,preparation_json,stream_json,created_at,updated_at) VALUES ('run','u','pending','{}','hash','{}','{}','{}',CURRENT_TIMESTAMP,CURRENT_TIMESTAMP)`); err != nil {
					t.Fatal(err)
				}
				for i := len(groupMigrations) - 1; i >= 0; i-- {
					execMigrationFileForDriver(t, db, groupMigrations[i].DownPath, driver)
				}
				var title string
				if err := db.QueryRow(`SELECT display_name FROM conversations WHERE id='retained'`).Scan(&title); err != nil || title != "Retained" {
					t.Fatalf("migration did not preserve conversation: %q %v", title, err)
				}
			}
		})
	}
}
