package migrate

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"
)

func TestModelVisionMigrationRoundTrip(t *testing.T) {
	for _, driver := range []string{"sqlite", "postgres"} {
		t.Run(driver, func(t *testing.T) {
			var db *sql.DB
			if driver == "sqlite" {
				db = openRawSQLite(t, filepath.Join(t.TempDir(), "vision.db"))
			} else {
				dsn := os.Getenv(migrationPostgresDSNEnv)
				if dsn == "" {
					t.Skip("PostgreSQL DSN required")
				}
				db = createTemporaryPostgresDatabase(t, dsn, "vision")
			}
			for _, table := range []string{"default_models", "user_model_provider_group_models"} {
				if _, err := db.Exec("CREATE TABLE " + table + " (id TEXT PRIMARY KEY, name TEXT NOT NULL); INSERT INTO " + table + " VALUES ('keep','existing')"); err != nil {
					t.Fatal(err)
				}
			}
			execMigrationFileForDriver(t, db, "../migrations/dev_mode/v0_3/20260917064606_add_model_vision.up.sql", driver)
			for _, table := range []string{"default_models", "user_model_provider_group_models"} {
				var vision bool
				var name string
				if err := db.QueryRow("SELECT name,vision FROM "+table+" WHERE id='keep'").Scan(&name, &vision); err != nil || vision || name != "existing" {
					t.Fatalf("existing row changed: %v", err)
				}
				if _, err := db.Exec("UPDATE " + table + " SET vision=TRUE"); err != nil {
					t.Fatal(err)
				}
			}
			execMigrationFileForDriver(t, db, "../migrations/dev_mode/v0_3/20260917064606_add_model_vision.down.sql", driver)
			for _, table := range []string{"default_models", "user_model_provider_group_models"} {
				var name string
				if err := db.QueryRow("SELECT name FROM " + table + " WHERE id='keep'").Scan(&name); err != nil || name != "existing" {
					t.Fatal("rollback lost data", err)
				}
			}
		})
	}
}
