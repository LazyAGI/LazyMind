package migrate

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"
)

func TestDocumentPublicationMigrationRoundTrip(t *testing.T) {
	for _, driver := range []string{"sqlite", "postgres"} {
		t.Run(driver, func(t *testing.T) {
			var db *sql.DB
			if driver == "postgres" {
				dsn := os.Getenv(migrationPostgresDSNEnv)
				if dsn == "" {
					t.Skip("requires disposable PostgreSQL")
				}
				db = createTemporaryPostgresDatabase(t, dsn, "publication-roundtrip")
			} else {
				db = openRawSQLite(t, filepath.Join(t.TempDir(), "publication.db"))
			}
			if _, err := db.Exec(`CREATE TABLE preserved_data(id INTEGER PRIMARY KEY, value TEXT); INSERT INTO preserved_data VALUES(1,'unchanged');`); err != nil {
				t.Fatal(err)
			}
			base := filepath.Join("..", "migrations", "dev_mode", "v0_3", "20260912125857_document_publications")
			execMigrationFileForDriver(t, db, base+".up.sql", driver)
			insert := `INSERT INTO document_publication_operations(id,owner_user_id,idempotency_key,session_id,slot_id,item_index,status,source_revision_id) VALUES('op','owner','key','session','slot',-1,'preparing','source')`
			if _, err := db.Exec(insert); err != nil {
				t.Fatal(err)
			}
			if _, err := db.Exec(`INSERT INTO document_publication_operations(id,owner_user_id,idempotency_key,session_id,slot_id,item_index,status,source_revision_id) VALUES('other','owner','key','session','other',-1,'preparing','source')`); err == nil {
				t.Fatal("owner/key uniqueness missing")
			}
			if _, err := db.Exec(`INSERT INTO document_publication_bindings(id,session_id,slot_id,item_index,owner_user_id) VALUES('binding','session','slot',-1,'owner')`); err != nil {
				t.Fatal(err)
			}
			if _, err := db.Exec(`INSERT INTO document_publication_bindings(id,session_id,slot_id,item_index,owner_user_id) VALUES('other-binding','session','slot',-1,'owner')`); err == nil {
				t.Fatal("item uniqueness missing")
			}
			execMigrationFileForDriver(t, db, base+".down.sql", driver)
			var value string
			if err := db.QueryRow(`SELECT value FROM preserved_data WHERE id=1`).Scan(&value); err != nil || value != "unchanged" {
				t.Fatal("rollback affected existing data", err)
			}
			if _, err := db.Exec(`SELECT id FROM document_publication_operations`); err == nil {
				t.Fatal("down did not remove operations")
			}
			execMigrationFileForDriver(t, db, base+".up.sql", driver)
			if _, err := db.Exec(insert); err != nil {
				t.Fatal("up after down failed", err)
			}
		})
	}
}

func TestDocumentPublicationSQLiteCurrentPathsMatchPublicationSchema(t *testing.T) {
	runner := &Runner{dir: "../migrations"}
	catalog, err := runner.loadCatalog()
	if err != nil {
		t.Fatal(err)
	}
	release := openRawSQLite(t, filepath.Join(t.TempDir(), "release.db"))
	dev := openRawSQLite(t, filepath.Join(t.TempDir(), "dev.db"))
	for i, mode := range catalog.Modes {
		if i == len(catalog.Modes)-1 {
			execMigrationFileForDriver(t, release, mode.Aggregate.UpPath, "sqlite")
			for _, migration := range mode.Dev {
				execMigrationFileForDriver(t, dev, migration.UpPath, "sqlite")
			}
			break
		}
		for _, db := range []*sql.DB{release, dev} {
			execMigrationFileForDriver(t, db, mode.Aggregate.UpPath, "sqlite")
		}
	}
	// The pre-existing v0.3 aggregate has unrelated SQLite drift (recorded in
	// the delivery log). Compare this change's complete tables/indexes after
	// executing both real paths, rather than equating DDL whitespace with schema.
	fingerprint := func(db *sql.DB) string {
		rows, err := db.Query(`SELECT type,name,sql FROM sqlite_master WHERE tbl_name IN ('document_publication_operations','document_publication_bindings') AND sql IS NOT NULL ORDER BY type,name`)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		var out string
		for rows.Next() {
			var kind, name, sql string
			if err := rows.Scan(&kind, &name, &sql); err != nil {
				t.Fatal(err)
			}
			out += kind + "|" + name + "|" + sql + "\n"
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		return out
	}
	if fingerprint(release) != fingerprint(dev) {
		t.Fatal("publication schema differs between SQLite aggregate and dev paths")
	}
}
