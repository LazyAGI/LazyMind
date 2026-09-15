package migrate

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"
)

func TestConversationUnpinOrderMigrationRoundTrip(t *testing.T) {
	for _, driver := range []string{"sqlite", "postgres"} {
		t.Run(driver, func(t *testing.T) {
			var db *sql.DB
			if driver == "sqlite" {
				db = openRawSQLite(t, filepath.Join(t.TempDir(), "unpin-order.db"))
			} else {
				dsn := os.Getenv(migrationPostgresDSNEnv)
				if dsn == "" {
					t.Skip("PostgreSQL integration DSN required")
				}
				db = createTemporaryPostgresDatabase(t, dsn, "unpin_order")
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
				if migration.FileVersion == 20260912063102 {
					target = migration
				} else {
					execMigrationFileForDriver(t, db, migration.UpPath, driver)
				}
			}
			if target.UpPath == "" {
				t.Fatal("missing migration")
			}
			if _, err := db.Exec(`INSERT INTO conversations(id,display_name,channel_id,create_user_id,create_user_name,created_at,updated_at,history_order) VALUES ('kept','Kept','default','u','User',CURRENT_TIMESTAMP,CURRENT_TIMESTAMP,7)`); err != nil {
				t.Fatal(err)
			}
			for round := 0; round < 2; round++ {
				execMigrationFileForDriver(t, db, target.UpPath, driver)
				var saved sql.NullInt64
				if err := db.QueryRow(`SELECT unpinned_history_order FROM conversations WHERE id='kept'`).Scan(&saved); err != nil || saved.Valid {
					t.Fatalf("legacy order=%v err=%v", saved, err)
				}
				if _, err := db.Exec(`UPDATE conversations SET unpinned_history_order=7 WHERE id='kept'`); err != nil {
					t.Fatal(err)
				}
				execMigrationFileForDriver(t, db, target.DownPath, driver)
				var title string
				var order int64
				if err := db.QueryRow(`SELECT display_name,history_order FROM conversations WHERE id='kept'`).Scan(&title, &order); err != nil || title != "Kept" || order != 7 {
					t.Fatalf("lost data: %q %d %v", title, order, err)
				}
			}
		})
	}
}
