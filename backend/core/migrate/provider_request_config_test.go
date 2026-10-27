package migrate

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"
)

const requestConfigMigration = "20261022092432_add_provider_request_config"

func TestProviderRequestConfigMigrationRoundTripAndAggregate(t *testing.T) {
	for _, driver := range []string{"sqlite", "postgres"} {
		t.Run(driver, func(t *testing.T) {
			open := func(name string) *sql.DB {
				if driver == "sqlite" {
					return openRawSQLite(t, filepath.Join(t.TempDir(), name+".db"))
				}
				dsn := os.Getenv(migrationPostgresDSNEnv)
				if dsn == "" {
					t.Skip("PostgreSQL DSN required")
				}
				return createTemporaryPostgresDatabase(t, dsn, "request_config_"+name)
			}
			dev, aggregate := open("dev"), open("aggregate")
			catalog, err := (&Runner{dir: "../migrations"}).loadCatalog()
			if err != nil {
				t.Fatal(err)
			}
			for i, mode := range catalog.Modes {
				execMigrationFileForDriver(t, aggregate, mode.Aggregate.UpPath, driver)
				if i < len(catalog.Modes)-1 {
					execMigrationFileForDriver(t, dev, mode.Aggregate.UpPath, driver)
					continue
				}
				for _, m := range mode.Dev {
					if m.FileVersion < 20261022092432 {
						execMigrationFileForDriver(t, dev, m.UpPath, driver)
					}
				}
			}
			if _, err := dev.Exec(`INSERT INTO user_model_providers (id,default_model_provider_id,name,description,base_url,protocol_name,category,create_user_id,create_user_name,created_at,updated_at) VALUES ('p','fixture','Proxy','','https://proxy.example/v1','OpenAI','model','owner','',CURRENT_TIMESTAMP,CURRENT_TIMESTAMP)`); err != nil {
				t.Fatal(err)
			}
			base := filepath.Join("..", "migrations", "dev_mode", "v0_3", requestConfigMigration)
			execMigrationFileForDriver(t, dev, base+".up.sql", driver)
			var cfg, protocol, url string
			if err := dev.QueryRow("SELECT request_config,protocol_name,base_url FROM user_model_providers WHERE id='p'").Scan(&cfg, &protocol, &url); err != nil || cfg != "" || protocol != "OpenAI" || url != "https://proxy.example/v1" {
				t.Fatal("upgrade changed active configuration", err)
			}
			// Compare both paths at the current release state, including later
			// independent migrations, rather than against a partial dev schema.
			for _, mode := range catalog.Modes {
				for _, migration := range mode.Dev {
					if migration.FileVersion > 20261022092432 {
						execMigrationFileForDriver(t, dev, migration.UpPath, driver)
					}
				}
			}
			if driver == "sqlite" {
				// Older unrelated aggregate tables have textually different DDL;
				// this migration only alters the supplier table, whose complete
				// final DDL must match exactly on both construction paths.
				var devDDL, aggregateDDL string
				if err := dev.QueryRow("SELECT sql FROM sqlite_master WHERE name='user_model_providers'").Scan(&devDDL); err != nil {
					t.Fatal(err)
				}
				if err := aggregate.QueryRow("SELECT sql FROM sqlite_master WHERE name='user_model_providers'").Scan(&aggregateDDL); err != nil {
					t.Fatal(err)
				}
				if devDDL != aggregateDDL {
					t.Fatalf("SQLite supplier aggregate differs from dev\n%s\n%s", devDDL, aggregateDDL)
				}
			} else {
				if postgresSchemaFingerprint(t, dev) != postgresSchemaFingerprint(t, aggregate) {
					t.Fatal("PostgreSQL request-config aggregate differs from dev")
				}
			}
			if _, err := dev.Exec(`UPDATE user_model_providers SET request_config='{}', protocol_name='Claude',base_url='https://changed.example' WHERE id='p'`); err != nil {
				t.Fatal(err)
			}
			execMigrationFileForDriver(t, dev, base+".down.sql", driver)
			if err := dev.QueryRow("SELECT protocol_name,base_url FROM user_model_providers WHERE id='p'").Scan(&protocol, &url); err != nil || protocol != "Claude" || url != "https://changed.example" {
				t.Fatal("rollback replaced current effective protocol or endpoint", err)
			}
		})
	}
}
