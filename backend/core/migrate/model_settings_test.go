package migrate

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"testing"
)

const modelSettingsMigration = "20261023163745_add_model_settings"

func TestModelSettingsMigrationRoundTripAndAggregate(t *testing.T) {
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
				return createTemporaryPostgresDatabase(t, dsn, "model_settings_"+name)
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
					if migration.FileVersion < 20261023163745 {
						execMigrationFileForDriver(t, dev, migration.UpPath, driver)
					}
				}
			}
			for _, statement := range []string{
				`INSERT INTO default_models(id,default_model_provider_id,provider_name,name,model_type,vision,max_input_tokens,created_at,updated_at) VALUES ('default-settings','provider','OpenAI','catalog-model','llm',TRUE,'256K',CURRENT_TIMESTAMP,CURRENT_TIMESTAMP)`,
				`INSERT INTO user_model_provider_group_models(id,user_model_provider_id,user_model_provider_group_id,provider_name,name,model_type,vision,max_input_tokens,input_token_limit,max_output_tokens,is_default,create_user_id,create_user_name,created_at,updated_at) VALUES ('user-settings','provider','group','OpenAI','saved-model','llm',TRUE,'256K','128K','32K',FALSE,'owner','',CURRENT_TIMESTAMP,CURRENT_TIMESTAMP)`,
			} {
				if _, err := dev.Exec(statement); err != nil {
					t.Fatal(err)
				}
			}
			base := filepath.Join("..", "migrations", "dev_mode", "v0_3", modelSettingsMigration)
			execMigrationFileForDriver(t, dev, base+".up.sql", driver)
			for _, table := range []string{"default_models", "user_model_provider_group_models"} {
				var settings, context string
				var vision bool
				if err := dev.QueryRow("SELECT model_settings,max_input_tokens,vision FROM "+table).Scan(&settings, &context, &vision); err != nil || settings != "{}" || context != "256K" || !vision {
					t.Fatalf("%s legacy configuration changed: %q %q %v %v", table, settings, context, vision, err)
				}
				const configured = "{\"feature_support\":{\"tool_calling\":false},\"feature_enabled\":{\"tool_calling\":false},\"embedding\":{\"query_prefix\":\"query: \",\"passage_prefix\":\"passage:\\n\",\"batch_size\":16}}"
				if _, err := dev.Exec("UPDATE "+table+" SET model_settings=$1", configured); err != nil {
					t.Fatal(err)
				}
				if err := dev.QueryRow("SELECT model_settings FROM " + table).Scan(&settings); err != nil || settings != configured {
					t.Fatalf("%s did not preserve exact JSON: %q %v", table, settings, err)
				}
				if driver == "sqlite" && !reflect.DeepEqual(sqliteModelColumns(t, dev, table), sqliteModelColumns(t, aggregate, table)) {
					t.Fatalf("%s aggregate columns differ from dev", table)
				}
			}
			if driver == "postgres" && postgresSchemaFingerprint(t, dev) != postgresSchemaFingerprint(t, aggregate) {
				t.Fatal("PostgreSQL model settings aggregate differs from dev")
			}
			execMigrationFileForDriver(t, dev, base+".down.sql", driver)
			for _, table := range []string{"default_models", "user_model_provider_group_models"} {
				var context string
				if err := dev.QueryRow("SELECT max_input_tokens FROM " + table).Scan(&context); err != nil || context != "256K" {
					t.Fatalf("rollback changed %s model: %q %v", table, context, err)
				}
				if _, err := dev.Query("SELECT model_settings FROM " + table); err == nil {
					t.Fatalf("rollback retained %s model_settings", table)
				}
			}
			var input, output string
			if err := dev.QueryRow("SELECT input_token_limit,max_output_tokens FROM user_model_provider_group_models WHERE id='user-settings'").Scan(&input, &output); err != nil || input != "128K" || output != "32K" {
				t.Fatal("rollback changed directional token limits", err)
			}
			execMigrationFileForDriver(t, dev, base+".up.sql", driver)
			var restored string
			if err := dev.QueryRow("SELECT model_settings FROM user_model_provider_group_models WHERE id='user-settings'").Scan(&restored); err != nil || restored != "{}" {
				t.Fatalf("re-upgrade failed: %q %v", restored, err)
			}
		})
	}
}

// Adding independent columns in a different order does not change their
// persistence contract. Compare names, types, defaults and constraints.
func sqliteModelColumns(t *testing.T, db *sql.DB, table string) []string {
	t.Helper()
	rows, err := db.Query(`PRAGMA table_info("` + table + `")`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var columns []string
	for rows.Next() {
		var cid, notNull, primaryKey int
		var name, dataType string
		var defaultValue sql.NullString
		if err := rows.Scan(&cid, &name, &dataType, &notNull, &defaultValue, &primaryKey); err != nil {
			t.Fatal(err)
		}
		columns = append(columns, fmt.Sprintf("%s|%s|%d|%t:%s|%d", name, dataType, notNull, defaultValue.Valid, defaultValue.String, primaryKey))
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	sort.Strings(columns)
	return columns
}
