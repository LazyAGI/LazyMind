package migrate

import (
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPaperPreparationMigrationPreservesRecordsAndNullableChats(t *testing.T) {
	for _, driver := range []string{"sqlite", "postgres"} {
		t.Run(driver, func(t *testing.T) {
			var db *sql.DB
			if driver == "sqlite" {
				db = openRawSQLite(t, filepath.Join(t.TempDir(), "paper-preparation.db"))
			} else {
				dsn := os.Getenv(migrationPostgresDSNEnv)
				if dsn == "" {
					t.Skip("PostgreSQL integration DSN required")
				}
				db = createTemporaryPostgresDatabase(t, dsn, "paper_preparation")
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
				if strings.Contains(migration.UpPath, "paper_expansion_preparation") {
					target = migration
					continue
				}
				execMigrationFileForDriver(t, db, migration.UpPath, driver)
			}
			if target.UpPath == "" {
				t.Fatal("preparation migration missing")
			}
			exec := func(query string) {
				t.Helper()
				if _, err := db.Exec(query); err != nil {
					t.Fatal(err)
				}
			}
			exec(`INSERT INTO academic_expansions(id,user_id,dataset_id,conversation_id,request_key,request_owner,title,goal,status,result_json,created_at,updated_at) VALUES ('retained','u','papers','chat','old','u','saved title','expansion','completed','{}',CURRENT_TIMESTAMP,CURRENT_TIMESTAMP)`)
			execMigrationFileForDriver(t, db, target.UpPath, driver)
			exec(`INSERT INTO academic_expansions(id,user_id,dataset_id,conversation_id,request_key,request_owner,title,goal,status,result_json,created_at,updated_at) VALUES ('prep-one','u','papers',NULL,'one','u','one','expansion','preparing','{}',CURRENT_TIMESTAMP,CURRENT_TIMESTAMP),('prep-two','u','papers',NULL,'two','u','two','expansion','preparing','{}',CURRENT_TIMESTAMP,CURRENT_TIMESTAMP)`)
			var count int
			if err := db.QueryRow("SELECT COUNT(*) FROM academic_expansions WHERE conversation_id IS NULL").Scan(&count); err != nil || count != 2 {
				t.Fatal("multiple uncreated Chats rejected", count, err)
			}
			var title string
			if err := db.QueryRow("SELECT title FROM academic_expansions WHERE id='retained'").Scan(&title); err != nil || title != "saved title" {
				t.Fatal("lost existing result", err)
			}
			execMigrationFileForDriver(t, db, target.DownPath, driver)
			if err := db.QueryRow("SELECT COUNT(*) FROM academic_expansions").Scan(&count); err != nil || count != 3 {
				t.Fatal("downgrade lost records", count, err)
			}
			execMigrationFileForDriver(t, db, target.UpPath, driver)
			if err := db.QueryRow("SELECT COUNT(*) FROM academic_expansions").Scan(&count); err != nil || count != 3 {
				t.Fatal("upgrade lost records", count, err)
			}
		})
	}
}
