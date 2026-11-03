package migrations_test

import (
	"database/sql"
	"fmt"
	"os"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"testing"
)

func importTableColumns(t *testing.T, db *sql.DB, table, dialect string) []string {
	t.Helper()
	result := []string{}
	if dialect == "postgres" {
		rows, err := db.Query("SELECT column_name,data_type FROM information_schema.columns WHERE table_schema=current_schema() AND table_name=$1", table)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		for rows.Next() {
			var name, kind string
			if err := rows.Scan(&name, &kind); err != nil {
				t.Fatal(err)
			}
			result = append(result, name+":"+kind)
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
	} else {
		rows, err := db.Query("PRAGMA table_info(" + table + ")")
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		for rows.Next() {
			var cid, required, primary int
			var name, kind string
			var defaultValue any
			if err := rows.Scan(&cid, &name, &kind, &required, &defaultValue, &primary); err != nil {
				t.Fatal(err)
			}
			result = append(result, name+":"+kind)
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
	}
	sort.Strings(result)
	return result
}

func TestPaperImportOperationsMigrationPreservesHistoryAndMatchesAggregate(t *testing.T) {
	for _, dialect := range []string{"sqlite", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			db := openDatabase(t, dialect)
			applySQL(t, db, migrationPath(t, "dev_mode/v0_3/*_add_academic_reference_import.up.sql"), dialect)
			if _, err := db.Exec(`INSERT INTO paper_import_batches(id,entry_type,target_dataset_id,source_document_ids_json,policy_snapshot_json,status,created_by,created_at,updated_at) VALUES ('existing','single_reference','papers','[]','{}','succeeded','user',CURRENT_TIMESTAMP,CURRENT_TIMESTAMP);
  INSERT INTO paper_import_items(id,batch_id,academic_work_id,reference_ids_json,presence_snapshot_json,selected_candidate_json,stage,status,error_details_json,created_at,updated_at) VALUES ('item','existing','work','[]','{}','{}','done','succeeded','{}',CURRENT_TIMESTAMP,CURRENT_TIMESTAMP)`); err != nil {
				t.Fatal(err)
			}
			up := migrationPath(t, "dev_mode/v0_3/*_paper_import_operations.up.sql")
			applySQL(t, db, up, dialect)
			var source, title, paper, result string
			if err := db.QueryRow("SELECT source_context_json FROM paper_import_batches WHERE id='existing'").Scan(&source); err != nil || source != "{}" {
				t.Fatalf("legacy source: %s %v", source, err)
			}
			if err := db.QueryRow("SELECT title_snapshot,library_paper_id,result_kind FROM paper_import_items WHERE id='item'").Scan(&title, &paper, &result); err != nil || title != "" || paper != "" || result != "" {
				t.Fatalf("legacy item changed: %v", err)
			}
			dev := map[string][]string{}
			for _, table := range []string{"paper_import_batches", "paper_import_items"} {
				dev[table] = importTableColumns(t, db, table, dialect)
			}
			applySQL(t, db, strings.Replace(up, ".up.sql", ".down.sql", 1), dialect)
			var count int
			if err := db.QueryRow("SELECT COUNT(*) FROM paper_import_items WHERE id='item'").Scan(&count); err != nil || count != 1 {
				t.Fatalf("rollback lost history: %d %v", count, err)
			}
			if _, err := db.Exec("DROP TABLE paper_import_items; DROP TABLE paper_import_batches"); err != nil {
				t.Fatal(err)
			}
			aggregate, err := os.ReadFile(migrationPath(t, "version_mode/v0_3/*.up.sql"))
			if err != nil {
				t.Fatal(err)
			}
			for _, table := range []string{"paper_import_batches", "paper_import_items"} {
				create := regexp.MustCompile(`(?s)CREATE TABLE IF NOT EXISTS ` + table + `\s*\(.*?\);`).Find(aggregate)
				if len(create) == 0 {
					t.Fatal("missing aggregate table", table)
				}
				if _, err := db.Exec(string(create)); err != nil {
					t.Fatal(err)
				}
				if got := importTableColumns(t, db, table, dialect); !reflect.DeepEqual(got, dev[table]) {
					t.Fatal(fmt.Sprintf("aggregate differs for %s: %v != %v", table, got, dev[table]))
				}
			}
		})
	}
}
