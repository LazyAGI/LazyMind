package migrate

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"
)

func TestSkillMarketPresentationMigrationRoundTrip(t *testing.T) {
	for _, driver := range []string{"sqlite", "postgres"} {
		t.Run(driver, func(t *testing.T) {
			var db *sql.DB
			if driver == "sqlite" {
				db = openRawSQLite(t, filepath.Join(t.TempDir(), "presentation.db"))
			} else {
				dsn := os.Getenv(migrationPostgresDSNEnv)
				if dsn == "" {
					t.Skip("PostgreSQL DSN required")
				}
				db = createTemporaryPostgresDatabase(t, dsn, "market_presentation")
			}
			if _, err := db.Exec(`CREATE TABLE skill_market_items (id VARCHAR(36) PRIMARY KEY, tags JSON NOT NULL DEFAULT '[]'); INSERT INTO skill_market_items (id,tags) VALUES ('existing','["research"]');`); err != nil {
				t.Fatal(err)
			}
			base := filepath.Join("..", "migrations", "dev_mode", "v0_3", "20261022093852_add_skill_market_presentation")
			execMigrationFileForDriver(t, db, base+".up.sql", driver)
			var raw string
			if err := db.QueryRow("SELECT display_i18n FROM skill_market_items WHERE id='existing'").Scan(&raw); err != nil || raw != "{}" {
				t.Fatalf("existing row fallback=%q err=%v", raw, err)
			}
			if _, err := db.Exec(`UPDATE skill_market_items SET display_i18n='{"zh-CN":{"name":"研究"}}' WHERE id='existing'`); err != nil {
				t.Fatal(err)
			}
			execMigrationFileForDriver(t, db, base+".down.sql", driver)
			if err := db.QueryRow("SELECT tags FROM skill_market_items WHERE id='existing'").Scan(&raw); err != nil || raw != `["research"]` {
				t.Fatalf("existing row lost: %q %v", raw, err)
			}
			if err := db.QueryRow("SELECT display_i18n FROM skill_market_items LIMIT 1").Scan(&raw); err == nil {
				t.Fatal("down retained presentation column")
			}
		})
	}
}
