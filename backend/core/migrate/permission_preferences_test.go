package migrate

import (
	"database/sql"
	"os"
	"testing"
)

func TestPermissionPreferencesMigrationRoundTrip(t *testing.T) {
	for _, driver := range []string{"sqlite", "postgres"} {
		t.Run(driver, func(t *testing.T) {
			var db *sql.DB
			if driver == "postgres" {
				dsn := os.Getenv(migrationPostgresDSNEnv)
				if dsn == "" {
					t.Skip("set MIGRATION_TEST_POSTGRES_DSN to an isolated test server")
				}
				db = createTemporaryPostgresDatabase(t, dsn, "permission_preferences")
			} else {
				db = openRawSQLite(t, t.TempDir()+"/permissions.db")
			}
			_, err := db.Exec(`CREATE TABLE user_chat_settings (user_id VARCHAR(255) PRIMARY KEY);
CREATE TABLE conversations (id VARCHAR(36) PRIMARY KEY, display_name TEXT);
CREATE TABLE conversation_workspace_bindings (conversation_id VARCHAR(36) PRIMARY KEY, permission_mode VARCHAR(32), permission_version BIGINT);
INSERT INTO user_chat_settings VALUES ('owner');
INSERT INTO conversations VALUES ('bound','keep bound'),('unbound','keep unbound');
INSERT INTO conversation_workspace_bindings VALUES ('bound','allow_all',7);`)
			if err != nil {
				t.Fatal(err)
			}
			up := "../migrations/dev_mode/v0_3/20260922020700_user_permission_preferences.up.sql"
			down := "../migrations/dev_mode/v0_3/20260922020700_user_permission_preferences.down.sql"
			for round := 0; round < 2; round++ {
				execMigrationFileForDriver(t, db, up, driver)
				for _, tc := range []struct {
					id, mode, title string
					version         int
				}{{"bound", "allow_all", "keep bound", 7}, {"unbound", "always_ask", "keep unbound", 1}} {
					var mode, title string
					var version int
					if err := db.QueryRow("SELECT permission_mode,permission_version,display_name FROM conversations WHERE id='"+tc.id+"'").Scan(&mode, &version, &title); err != nil || mode != tc.mode || version != tc.version || title != tc.title {
						t.Fatalf("migration: %s %d %s %v", mode, version, title, err)
					}
				}
				var mode string
				if err := db.QueryRow("SELECT default_permission_mode FROM user_chat_settings WHERE user_id='owner'").Scan(&mode); err != nil || mode != "always_ask" {
					t.Fatalf("retroactive trust: %s %v", mode, err)
				}
				execMigrationFileForDriver(t, db, down, driver)
				var version int
				if err := db.QueryRow("SELECT permission_mode,permission_version FROM conversation_workspace_bindings WHERE conversation_id='bound'").Scan(&mode, &version); err != nil || mode != "allow_all" || version != 7 {
					t.Fatalf("rollback lost permission: %s %d %v", mode, version, err)
				}
			}
		})
	}
}
