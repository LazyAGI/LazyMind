package migrate

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"
)

const activeKeyMigration = "20261022065811_add_supplier_active_key"

func TestSupplierActiveKeyMigrationRoundTrip(t *testing.T) {
	for _, driver := range []string{"sqlite", "postgres"} {
		t.Run(driver, func(t *testing.T) {
			var db *sql.DB
			if driver == "sqlite" {
				db = openRawSQLite(t, filepath.Join(t.TempDir(), "active-key.db"))
			} else {
				dsn := os.Getenv(migrationPostgresDSNEnv)
				if dsn == "" {
					t.Skip("PostgreSQL DSN required")
				}
				db = createTemporaryPostgresDatabase(t, dsn, "active_key")
			}
			catalog, err := (&Runner{dir: "../migrations"}).loadCatalog()
			if err != nil {
				t.Fatal(err)
			}
			for i, mode := range catalog.Modes {
				if i < len(catalog.Modes)-1 {
					execMigrationFileForDriver(t, db, mode.Aggregate.UpPath, driver)
					continue
				}
				for _, migration := range mode.Dev {
					if migration.FileVersion < 20261022065811 {
						execMigrationFileForDriver(t, db, migration.UpPath, driver)
					}
				}
			}
			if _, err := db.Exec(`INSERT INTO user_model_providers (id,default_model_provider_id,name,description,base_url,category,create_user_id,create_user_name,created_at,updated_at) VALUES ('p','fixture','Fixture','','https://shared.example/v1','model','user','','2026-01-01','2026-01-01');
INSERT INTO user_model_provider_groups (id,user_model_provider_id,name,base_url,api_key,api_key_ciphertext,credential_revision,is_verified,create_user_id,create_user_name,created_at,updated_at) VALUES ('g','p','Saved pool','https://shared.example/v1','','existing-ciphertext',7,TRUE,'user','','2026-01-01','2026-01-01');`); err != nil {
				t.Fatal(err)
			}
			base := filepath.Join("..", "migrations", "dev_mode", "v0_3", activeKeyMigration)
			execMigrationFileForDriver(t, db, base+".up.sql", driver)
			var groupID, keyID string
			var initialized bool
			var revision int64
			if err := db.QueryRow("SELECT active_key_group_id,active_key_id,active_key_initialized,active_key_revision FROM user_model_providers WHERE id='p'").Scan(&groupID, &keyID, &initialized, &revision); err != nil || groupID != "" || keyID != "" || initialized || revision != 0 {
				t.Fatal("legacy row is not eligible for one-time initialization", err)
			}
			if _, err := db.Exec("UPDATE user_model_providers SET active_key_group_id='g',active_key_id='opaque-key-id',active_key_initialized=TRUE,active_key_revision=1 WHERE id='p'"); err != nil {
				t.Fatal(err)
			}
			execMigrationFileForDriver(t, db, base+".down.sql", driver)
			var ciphertext string
			if err := db.QueryRow("SELECT api_key_ciphertext,credential_revision FROM user_model_provider_groups WHERE id='g'").Scan(&ciphertext, &revision); err != nil || ciphertext != "existing-ciphertext" || revision != 7 {
				t.Fatal("migration changed saved credentials", err)
			}
			if err := db.QueryRow("SELECT active_key_id FROM user_model_providers WHERE id='p'").Scan(&keyID); err == nil {
				t.Fatal("down retained selection columns")
			}
		})
	}
}
