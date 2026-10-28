package migrate

import (
	"database/sql"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

func TestKnowledgeSceneCleanupRoundTripAndFinalSchema(t *testing.T) {
	for _, driver := range []string{"sqlite", "postgres"} {
		t.Run(driver, func(t *testing.T) {
			db := paperLibraryMigrationDB(t, driver, "scene_cleanup")
			if _, err := db.Exec(`CREATE TABLE datasets(id VARCHAR(255) PRIMARY KEY, scene VARCHAR(64) NOT NULL DEFAULT 'general', scenario_key VARCHAR(64) NOT NULL DEFAULT 'general');INSERT INTO datasets(id,scene,scenario_key)VALUES('existing','legal','academic_papers')`); err != nil {
				t.Fatal(err)
			}
			prefix := "../migrations/dev_mode/v0_3/20261026051528_use_persisted_knowledge_scene"
			execMigrationFileForDriver(t, db, prefix+".up.sql", driver)
			var scene string
			if err := db.QueryRow("SELECT scene FROM datasets WHERE id='existing'").Scan(&scene); err != nil || scene != "legal" {
				t.Fatal(scene, err)
			}
			if _, err := db.Exec("SELECT scenario_key FROM datasets"); err == nil {
				t.Fatal("obsolete scenario remains")
			}
			execMigrationFileForDriver(t, db, prefix+".down.sql", driver)
			if err := db.QueryRow("SELECT scene FROM datasets WHERE id='existing'").Scan(&scene); err != nil || scene != "legal" {
				t.Fatal(scene, err)
			}
			catalog, err := (&Runner{dir: filepath.Join("..", "migrations")}).loadCatalog()
			if err != nil {
				t.Fatal(err)
			}
			aggregate, dev := paperLibraryMigrationDB(t, driver, "scene_aggregate"), paperLibraryMigrationDB(t, driver, "scene_dev")
			for i, mode := range catalog.Modes {
				execMigrationFileForDriver(t, aggregate, mode.Aggregate.UpPath, driver)
				if i < len(catalog.Modes)-1 {
					execMigrationFileForDriver(t, dev, mode.Aggregate.UpPath, driver)
				} else {
					for _, m := range mode.Dev {
						execMigrationFileForDriver(t, dev, m.UpPath, driver)
					}
				}
			}
			if driver == "sqlite" {
				if a, b := sqliteSceneSchema(t, aggregate), sqliteSceneSchema(t, dev); a != b {
					t.Fatalf("affected aggregate and dev schema differ:\n%s", sceneSchemaDiff(a, b))
				}
			} else {
				if a, b := strings.Join(postgresSchemaLines(t, aggregate), "\n"), strings.Join(postgresSchemaLines(t, dev), "\n"); a != b {
					t.Fatalf("affected aggregate and dev schema differ:\n%s", sceneSchemaDiff(a, b))
				}
			}
		})
	}
}

// Compare SQLite's effective schema: ALTER TABLE may retain different SQL formatting.
func sqliteSceneSchema(t *testing.T, db *sql.DB) string {
	t.Helper()
	rows, err := db.Query("SELECT name FROM sqlite_master WHERE type='table' AND (name IN ('datasets','documents','tasks','uploaded_files','paper_libraries','paper_import_batches','paper_import_items','resource_operation_receipts') OR name LIKE 'academic_%') ORDER BY name")
	if err != nil {
		t.Fatal(err)
	}
	var tables []string
	for rows.Next() {
		var name string
		if err = rows.Scan(&name); err != nil {
			t.Fatal(err)
		}
		tables = append(tables, name)
	}
	rows.Close()
	var lines []string
	for _, table := range tables {
		quoted := strings.ReplaceAll(table, "'", "''")
		for _, query := range []string{
			`SELECT name,upper(type),"notnull",COALESCE(dflt_value,''),pk FROM pragma_table_info('` + quoted + `') ORDER BY name`,
			`SELECT "table","from","to",on_update,on_delete FROM pragma_foreign_key_list('` + quoted + `') ORDER BY "from"`,
		} {
			r, e := db.Query(query)
			if e != nil {
				t.Fatal(e)
			}
			for r.Next() {
				var a, b, c, d, e string
				if err = r.Scan(&a, &b, &c, &d, &e); err != nil {
					t.Fatal(err)
				}
				lines = append(lines, fmt.Sprintf("%s|%s|%s|%s|%s|%s", table, a, b, c, d, e))
			}
			if err = r.Err(); err != nil {
				t.Fatal(err)
			}
			r.Close()
		}
	}
	r, e := db.Query("SELECT type,name,sql FROM sqlite_master WHERE type IN ('index','trigger') AND sql IS NOT NULL AND (tbl_name IN ('datasets','documents','tasks','uploaded_files','paper_libraries','paper_import_batches','paper_import_items','resource_operation_receipts') OR tbl_name LIKE 'academic_%') ORDER BY name")
	if e != nil {
		t.Fatal(e)
	}
	for r.Next() {
		var kind, name, ddl string
		if err = r.Scan(&kind, &name, &ddl); err != nil {
			t.Fatal(err)
		}
		ddl = strings.ReplaceAll(ddl, `"`, "")
		lines = append(lines, kind+"|"+name+"|"+strings.Join(strings.Fields(ddl), ""))
	}
	if err = r.Err(); err != nil {
		t.Fatal(err)
	}
	r.Close()
	sort.Strings(lines)
	return strings.Join(lines, "\n")
}
func sceneSchemaDiff(a, b string) string {
	left, right := map[string]bool{}, map[string]bool{}
	for _, l := range strings.Split(a, "\n") {
		left[l] = true
	}
	for _, l := range strings.Split(b, "\n") {
		right[l] = true
	}
	var lines []string
	for l := range left {
		if !right[l] {
			lines = append(lines, "aggregate: "+l)
		}
	}
	for l := range right {
		if !left[l] {
			lines = append(lines, "dev: "+l)
		}
	}
	sort.Strings(lines)
	return strings.Join(lines, "\n")
}
