package migrate

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"
)

const modelTokenLimitsMigration = "20261023032750_add_model_token_limits"

func TestModelTokenLimitsMigrationRoundTripAndAggregate(t *testing.T) {
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
				return createTemporaryPostgresDatabase(t, dsn, "model_token_limits_"+name)
			}
			dev, aggregate := open("dev"), open("aggregate")
			catalog, err := (&Runner{dir: "../migrations"}).loadCatalog()
			if err != nil {
				t.Fatal(err)
			}
			for index, mode := range catalog.Modes {
				execMigrationFileForDriver(t, aggregate, mode.Aggregate.UpPath, driver)
				if index < len(catalog.Modes)-1 {
					execMigrationFileForDriver(t, dev, mode.Aggregate.UpPath, driver)
					continue
				}
				for _, migration := range mode.Dev {
					if migration.FileVersion < 20261023032750 {
						execMigrationFileForDriver(t, dev, migration.UpPath, driver)
					}
				}
			}
			if _, err := dev.Exec(`INSERT INTO user_model_provider_group_models (id,user_model_provider_id,user_model_provider_group_id,provider_name,name,model_type,max_input_tokens,is_default,create_user_id,create_user_name,created_at,updated_at) VALUES ('limit-fixture','fixture-provider','fixture-group','OpenAI','fixture-model','llm','256K',FALSE,'owner','',CURRENT_TIMESTAMP,CURRENT_TIMESTAMP)`); err != nil {
				t.Fatal(err)
			}
			base := filepath.Join("..", "migrations", "dev_mode", "v0_3", modelTokenLimitsMigration)
			execMigrationFileForDriver(t, dev, base+".up.sql", driver)
			var context string
			var input, output sql.NullString
			if err := dev.QueryRow(`SELECT max_input_tokens,input_token_limit,max_output_tokens FROM user_model_provider_group_models WHERE id='limit-fixture'`).Scan(&context, &input, &output); err != nil || context != "256K" || input.Valid || output.Valid {
				t.Fatalf("upgrade changed existing limits: %q %#v %#v %v", context, input, output, err)
			}
			if driver == "sqlite" {
				var devDDL, aggregateDDL string
				if err := dev.QueryRow(`SELECT sql FROM sqlite_master WHERE name='user_model_provider_group_models'`).Scan(&devDDL); err != nil {
					t.Fatal(err)
				}
				if err := aggregate.QueryRow(`SELECT sql FROM sqlite_master WHERE name='user_model_provider_group_models'`).Scan(&aggregateDDL); err != nil {
					t.Fatal(err)
				}
				if devDDL != aggregateDDL {
					t.Fatalf("model aggregate differs from dev\n%s\n%s", devDDL, aggregateDDL)
				}
			} else if postgresSchemaFingerprint(t, dev) != postgresSchemaFingerprint(t, aggregate) {
				t.Fatal("PostgreSQL aggregate differs from dev")
			}
			if _, err := dev.Exec(`UPDATE user_model_provider_group_models SET input_token_limit='128K',max_output_tokens='32K' WHERE id='limit-fixture'`); err != nil {
				t.Fatal(err)
			}
			execMigrationFileForDriver(t, dev, base+".down.sql", driver)
			if err := dev.QueryRow(`SELECT max_input_tokens FROM user_model_provider_group_models WHERE id='limit-fixture'`).Scan(&context); err != nil || context != "256K" {
				t.Fatal("rollback changed existing model", err)
			}
		})
	}
}
