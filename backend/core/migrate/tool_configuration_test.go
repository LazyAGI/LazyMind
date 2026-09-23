package migrate

import (
	"database/sql"
	"os"
	"testing"
)

func TestToolConfigurationMigrationRoundTrip(t *testing.T) {
	for _, driver := range []string{"sqlite", "postgres"} {
		t.Run(driver, func(t *testing.T) {
			var db *sql.DB
			if driver == "postgres" {
				dsn := os.Getenv(migrationPostgresDSNEnv)
				if dsn == "" {
					t.Skip("set MIGRATION_TEST_POSTGRES_DSN to an isolated test server")
				}
				db = createTemporaryPostgresDatabase(t, dsn, "tool_configuration")
			} else {
				db = openRawSQLite(t, t.TempDir()+"/configuration.db")
			}
			if _, err := db.Exec("CREATE TABLE mcp_servers (id TEXT PRIMARY KEY, enabled BOOLEAN NOT NULL); INSERT INTO mcp_servers VALUES ('enabled',TRUE),('disabled',FALSE)"); err != nil {
				t.Fatal(err)
			}
			for round := 0; round < 2; round++ {
				execMigrationFileForDriver(t, db, "../migrations/dev_mode/v0_3/20260922085706_tool_configuration_actions.up.sql", driver)
				_, err := db.Exec(`INSERT INTO tool_configuration_actions (id,user_id,conversation_id,history_id,run_id,service,label,status,created_at,updated_at)
      VALUES ('a','u','c','h','r','mail','Mailbox','needs_configuration',CURRENT_TIMESTAMP,CURRENT_TIMESTAMP)`)
				if err != nil {
					t.Fatal(err)
				}
				var version, delivered int
				var revision string
				if err := db.QueryRow("SELECT version,delivered_version,revision FROM tool_configuration_actions WHERE id='a'").Scan(&version, &delivered, &revision); err != nil || version != 1 || delivered != 0 || revision != "" {
					t.Fatal("invalid defaults", err)
				}
				execMigrationFileForDriver(t, db, "../migrations/dev_mode/v0_3/20260922085706_tool_configuration_actions.down.sql", driver)
			}
		})
	}
}
