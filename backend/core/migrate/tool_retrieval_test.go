package migrate

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"
)

func TestToolRetrievalMigrationRoundTrip(t *testing.T) {
	for _, driver := range []string{"sqlite", "postgres"} {
		t.Run(driver, func(t *testing.T) {
			var db *sql.DB
			if driver == "sqlite" {
				db = openRawSQLite(t, filepath.Join(t.TempDir(), "retrieval.db"))
			} else {
				dsn := os.Getenv(migrationPostgresDSNEnv)
				if dsn == "" {
					t.Skip("PostgreSQL integration DSN required")
				}
				db = createTemporaryPostgresDatabase(t, dsn, "tool_retrieval")
			}
			if _, err := db.Exec(`CREATE TABLE user_chat_settings (user_id TEXT PRIMARY KEY, enable_subagent BOOLEAN NOT NULL DEFAULT true);
                INSERT INTO user_chat_settings (user_id) VALUES ('existing')`); err != nil {
				t.Fatal(err)
			}
			base := filepath.Join("..", "migrations", "dev_mode", "v0_3", "20260917115139_add_tool_retrieval")
			execMigrationFileForDriver(t, db, base+".up.sql", driver)
			var enabled bool
			if err := db.QueryRow(`SELECT enable_tool_retrieval FROM user_chat_settings WHERE user_id='existing'`).Scan(&enabled); err != nil || enabled {
				t.Fatal("default must be false", enabled, err)
			}
			if _, err := db.Exec(`UPDATE user_chat_settings SET enable_tool_retrieval=true WHERE user_id='existing'`); err != nil {
				t.Fatal(err)
			}
			execMigrationFileForDriver(t, db, base+".down.sql", driver)
			if err := db.QueryRow(`SELECT enable_subagent FROM user_chat_settings WHERE user_id='existing'`).Scan(&enabled); err != nil || !enabled {
				t.Fatal("rollback must preserve settings", enabled, err)
			}
			execMigrationFileForDriver(t, db, base+".up.sql", driver)
		})
	}
}
