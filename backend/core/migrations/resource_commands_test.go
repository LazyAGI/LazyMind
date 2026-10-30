package migrations_test

import (
	"database/sql"
	"fmt"
	"sort"
	"strings"
	"testing"
)

func TestResourceCommandsMigrationPreservesRows(t *testing.T) {
	for _, dialect := range []string{"sqlite", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			db := openDatabase(t, dialect)
			if _, err := db.Exec("CREATE TABLE datasets (id VARCHAR(255) PRIMARY KEY, display_name TEXT); CREATE TABLE documents(id VARCHAR(128) PRIMARY KEY, dataset_id VARCHAR(255),p_id TEXT, display_name TEXT,document_type TEXT,tags JSON,file_id TEXT,ext JSON,deleted_at TIMESTAMP); INSERT INTO datasets VALUES ('kb','preserved'); INSERT INTO documents(id,dataset_id,display_name) VALUES ('doc','kb','preserved')"); err != nil {
				t.Fatal(err)
			}
			up := migrationPath(t, "dev_mode/v0_3/*_knowledge_resource_commands.up.sql")
			applySQL(t, db, up, dialect)
			var scene string
			var version int64
			if err := db.QueryRow("SELECT scene,resource_version FROM datasets WHERE id='kb'").Scan(&scene, &version); err != nil || scene != "general" || version != 1 {
				t.Fatalf("defaults %s/%d: %v", scene, version, err)
			}
			if dialect == "sqlite" {
				if _, err := db.Exec("PRAGMA recursive_triggers=ON"); err != nil {
					t.Fatal(err)
				}
			}
			for i, statement := range []string{"UPDATE documents SET display_name='renamed' WHERE id='doc'", "UPDATE documents SET display_name='renamed' WHERE id='doc'", "UPDATE documents SET resource_version=resource_version+1 WHERE id='doc'"} {
				if _, err := db.Exec(statement); err != nil {
					t.Fatal(err)
				}
				var dv, kv int64
				if err := db.QueryRow("SELECT resource_version FROM documents WHERE id='doc'").Scan(&dv); err != nil {
					t.Fatal(err)
				}
				if err := db.QueryRow("SELECT resource_version FROM datasets WHERE id='kb'").Scan(&kv); err != nil {
					t.Fatal(err)
				}
				want := int64(2)
				if i == 2 {
					want = 3
				}
				if dv != want || kv != want {
					t.Fatalf("trigger versions after %d: document=%d dataset=%d want %d", i, dv, kv, want)
				}
			}
			if _, err := db.Exec("INSERT INTO documents(id,dataset_id,display_name) VALUES ('legacy','kb','child'); INSERT INTO datasets(id,display_name) VALUES ('other','Other'); UPDATE documents SET dataset_id='other' WHERE id='legacy'; DELETE FROM documents WHERE id='legacy'"); err != nil {
				t.Fatal(err)
			}
			var source, target int64
			_ = db.QueryRow("SELECT resource_version FROM datasets WHERE id='kb'").Scan(&source)
			_ = db.QueryRow("SELECT resource_version FROM datasets WHERE id='other'").Scan(&target)
			if source != 5 || target != 3 {
				t.Fatalf("legacy membership fencing differs: source=%d target=%d", source, target)
			}
			if _, err := db.Exec("INSERT INTO resource_operation_receipts VALUES ('default','u','key','create','hash','{}',CURRENT_TIMESTAMP)"); err != nil {
				t.Fatal(err)
			}
			if _, err := db.Exec("INSERT INTO resource_operation_receipts VALUES ('default','u','key','create','hash','{}',CURRENT_TIMESTAMP)"); err == nil {
				t.Fatal("duplicate receipt accepted")
			}
			applySQL(t, db, strings.Replace(up, ".up.sql", ".down.sql", 1), dialect)
			var name string
			if err := db.QueryRow("SELECT display_name FROM documents WHERE id='doc'").Scan(&name); err != nil || name != "renamed" {
				t.Fatalf("row lost: %s %v", name, err)
			}
		})
	}
}

func resourceSchema(t *testing.T, db *sql.DB, dialect string) string {
	t.Helper()
	var lines []string
	for _, table := range []string{"datasets", "documents", "resource_operation_receipts", "document_ownership_backfills", "rejected_document_publications"} {
		if dialect == "sqlite" {
			rows, err := db.Query("PRAGMA table_info(" + table + ")")
			if err != nil {
				t.Fatal(err)
			}
			for rows.Next() {
				var cid, required, pk int
				var name, kind string
				var def sql.NullString
				if err = rows.Scan(&cid, &name, &kind, &required, &def, &pk); err != nil {
					t.Fatal(err)
				}
				lines = append(lines, fmt.Sprintf("%s|%s|%s|%d|%s|%d", table, name, kind, required, def.String, pk))
			}
			rows.Close()
		} else {
			rows, err := db.Query("SELECT column_name,data_type,is_nullable,COALESCE(column_default,'') FROM information_schema.columns WHERE table_schema='public' AND table_name=$1", table)
			if err != nil {
				t.Fatal(err)
			}
			for rows.Next() {
				var name, kind, nullable, def string
				if err = rows.Scan(&name, &kind, &nullable, &def); err != nil {
					t.Fatal(err)
				}
				lines = append(lines, fmt.Sprintf("%s|%s|%s|%s|%s", table, name, kind, nullable, def))
			}
			rows.Close()
		}
	}
	sort.Strings(lines)
	return strings.Join(lines, "\n")
}
