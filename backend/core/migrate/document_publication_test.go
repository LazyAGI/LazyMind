package migrate

import (
	"database/sql"
	"os"
	"path/filepath"
	"strings"
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
	// ALTER TABLE appends columns while the aggregate declares the final table
	// directly. Compare publication schema semantics, not column order or DDL text.
	for _, table := range []string{"document_publication_operations", "document_publication_bindings"} {
		if a, b := publicationSQLiteTableFingerprint(t, release, table), publicationSQLiteTableFingerprint(t, dev, table); a != b {
			t.Fatalf("publication schema differs for %s: aggregate=%s dev=%s", table, a, b)
		}
	}
}

func publicationSQLiteTableFingerprint(t *testing.T, db *sql.DB, table string) string {
	t.Helper()
	var out strings.Builder
	for _, query := range []string{
		`SELECT json_array('column',name,type,"notnull",dflt_value,pk,hidden) FROM pragma_table_xinfo(?) ORDER BY name`,
		`SELECT json_array('index',il.name,il."unique",il.origin,il.partial,ix.seqno,ix.name,ix."desc",ix.coll,ix.key)
		 FROM pragma_index_list(?) il JOIN pragma_index_xinfo(il.name) ix ORDER BY il.name,ix.seqno`,
		`SELECT json_array('foreign_key',id,seq,"table","from","to",on_update,on_delete,match)
		 FROM pragma_foreign_key_list(?) ORDER BY id,seq`,
	} {
		rows, err := db.Query(query, table)
		if err != nil {
			t.Fatal(err)
		}
		for rows.Next() {
			var entry string
			if err := rows.Scan(&entry); err != nil {
				rows.Close()
				t.Fatal(err)
			}
			out.WriteString(entry + "\n")
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
	}
	if out.Len() == 0 {
		t.Fatalf("publication table %s is missing", table)
	}
	return out.String()
}

func TestPublicationSQLiteFingerprintDetectsSchemaChanges(t *testing.T) {
	baseline := openRawSQLite(t, filepath.Join(t.TempDir(), "baseline.db"))
	const original = `CREATE TABLE publication(id TEXT PRIMARY KEY, scope TEXT NOT NULL DEFAULT '');
		CREATE UNIQUE INDEX publication_scope ON publication(scope,id);`
	if _, err := baseline.Exec(original); err != nil {
		t.Fatal(err)
	}
	want := publicationSQLiteTableFingerprint(t, baseline, "publication")
	for _, sample := range []struct {
		name  string
		sql   string
		equal bool
	}{
		{"column order", strings.Replace(original, "id TEXT PRIMARY KEY, scope TEXT NOT NULL DEFAULT ''", "scope TEXT NOT NULL DEFAULT '', id TEXT PRIMARY KEY", 1), true},
		{"column type", strings.Replace(original, "scope TEXT", "scope BLOB", 1), false},
		{"default", strings.Replace(original, "DEFAULT ''", "DEFAULT 'changed'", 1), false},
		{"not null", strings.Replace(original, "NOT NULL", "", 1), false},
		{"primary key", strings.Replace(original, "PRIMARY KEY", "", 1), false},
		{"index uniqueness", strings.Replace(original, "UNIQUE INDEX", "INDEX", 1), false},
		{"index column order", strings.Replace(original, "publication(scope,id)", "publication(id,scope)", 1), false},
		{"index sort order", strings.Replace(original, "publication(scope,id)", "publication(scope DESC,id)", 1), false},
		{"foreign key", strings.Replace(original, "DEFAULT ''", "DEFAULT '' REFERENCES parent(id)", 1), false},
	} {
		t.Run(sample.name, func(t *testing.T) {
			db := openRawSQLite(t, filepath.Join(t.TempDir(), "changed.db"))
			if _, err := db.Exec(sample.sql); err != nil {
				t.Fatal(err)
			}
			if got := publicationSQLiteTableFingerprint(t, db, "publication"); (got == want) != sample.equal {
				t.Fatalf("schema equality = %t, want %t: original=%s changed=%s", got == want, sample.equal, want, got)
			}
		})
	}
}
