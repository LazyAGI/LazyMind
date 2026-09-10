package migrate

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"
)

func TestConversationOpeningMigrationRoundTrip(t *testing.T) {
	for _, driver := range []string{"sqlite", "postgres"} {
		t.Run(driver, func(t *testing.T) {
			var db *sql.DB
			if driver == "sqlite" {
				db = openRawSQLite(t, filepath.Join(t.TempDir(), "opening.db"))
			} else {
				dsn := os.Getenv(migrationPostgresDSNEnv)
				if dsn == "" {
					t.Skip("PostgreSQL integration DSN required")
				}
				db = createTemporaryPostgresDatabase(t, dsn, "opening_roundtrip")
			}
			catalog, err := (&Runner{dir: "../migrations"}).loadCatalog()
			if err != nil {
				t.Fatal(err)
			}
			for _, mode := range catalog.Modes[:len(catalog.Modes)-1] {
				execMigrationFileForDriver(t, db, mode.Aggregate.UpPath, driver)
			}
			current := catalog.Modes[len(catalog.Modes)-1]
			var up, down string
			for _, migration := range current.Dev {
				if migration.FileVersion == 20260907081757 {
					up = migration.UpPath
					down = migration.DownPath
					continue
				}
				execMigrationFileForDriver(t, db, migration.UpPath, driver)
			}
			if _, err := db.Exec(`INSERT INTO conversations(id,display_name,channel_id,create_user_id,create_user_name,created_at,updated_at) VALUES ('opening-retained','人工标题','default','u1','User',CURRENT_TIMESTAMP,CURRENT_TIMESTAMP)`); err != nil {
				t.Fatal(err)
			}
			execMigrationFileForDriver(t, db, up, driver)
			if _, err := db.Exec(`INSERT INTO conversation_opening_metadata(conversation_id,user_id,input_json,source_history_ids,source_hash,evidence_hash,opening_turns,generator_version,status) VALUES ('opening-retained','u1','{}','[]','hash','hash',1,'v1','pending')`); err != nil {
				t.Fatal(err)
			}
			execMigrationFileForDriver(t, db, down, driver)
			var title string
			if err := db.QueryRow(`SELECT display_name FROM conversations WHERE id='opening-retained'`).Scan(&title); err != nil || title != "人工标题" {
				t.Fatal("rollback did not preserve conversation", title, err)
			}
			execMigrationFileForDriver(t, db, up, driver)
			var source string
			if err := db.QueryRow(`SELECT title_source FROM conversations WHERE id='opening-retained'`).Scan(&source); err != nil || source != "unknown" {
				t.Fatal("upgrade default", source, err)
			}
		})
	}
}
