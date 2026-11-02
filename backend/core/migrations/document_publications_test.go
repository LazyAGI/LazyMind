package migrations_test

import (
	"database/sql"
	"fmt"
	"reflect"
	"sort"
	"strings"
	"testing"
)

func publicationColumns(t *testing.T, db *sql.DB, dialect string) []string {
	t.Helper()
	var result []string
	for _, table := range []string{"document_publication_bindings", "document_publication_operations"} {
		query := "SELECT column_name FROM information_schema.columns WHERE table_schema='public' AND table_name='" + table + "'"
		if dialect == "sqlite" {
			query = "SELECT name FROM pragma_table_info('" + table + "')"
		}
		rows, err := db.Query(query)
		if err != nil {
			t.Fatal(err)
		}
		for rows.Next() {
			var column string
			if err := rows.Scan(&column); err != nil {
				t.Fatal(err)
			}
			result = append(result, table+":"+column)
		}
		rows.Close()
	}
	sort.Strings(result)
	return result
}

func TestIndependentPublicationMigrationPreservesLegacyAndMatchesAggregate(t *testing.T) {
	for _, dialect := range []string{"sqlite", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			dev, aggregate := openDatabase(t, dialect), openDatabase(t, dialect)
			applySQL(t, dev, migrationPath(t, "dev_mode/v0_3/20260912125857_document_publications.up.sql"), dialect)
			before := publicationColumns(t, dev, dialect)
			if _, err := dev.Exec("INSERT INTO document_publication_bindings(id,session_id,slot_id,item_index,owner_user_id,provider) VALUES ('old','s','draft',-1,'owner','github')"); err != nil {
				t.Fatal(err)
			}
			up := migrationPath(t, "dev_mode/v0_3/*_independent_document_publications.up.sql")
			applySQL(t, dev, up, dialect)
			for _, version := range []string{"v0_1", "v0_2", "v0_3"} {
				applySQL(t, aggregate, migrationPath(t, "version_mode/"+version+"/*.up.sql"), dialect)
			}
			if !reflect.DeepEqual(publicationColumns(t, dev, dialect), publicationColumns(t, aggregate, dialect)) {
				t.Fatal("aggregate schema differs from upgrade")
			}
			for _, provider := range []string{"github", "notion"} {
				query := fmt.Sprintf("INSERT INTO document_publication_bindings(id,session_id,slot_id,item_index,owner_user_id,provider,binding_scope) VALUES ('%s','s','draft',-1,'owner','%s','%s')", provider, provider, provider)
				if _, err := dev.Exec(query); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := dev.Exec("INSERT INTO document_publication_bindings(id,session_id,slot_id,item_index,owner_user_id,binding_scope) VALUES ('duplicate','s','draft',-1,'owner','github')"); err == nil {
				t.Fatal("duplicate target accepted")
			}
			applySQL(t, dev, strings.Replace(up, ".up.sql", ".down.sql", 1), dialect)
			if !reflect.DeepEqual(before, publicationColumns(t, dev, dialect)) {
				t.Fatal("rollback did not restore original schema")
			}
			var provider string
			if err := dev.QueryRow("SELECT provider FROM document_publication_bindings WHERE id='old'").Scan(&provider); err != nil || provider != "github" {
				t.Fatalf("legacy binding lost: %v", err)
			}
		})
	}
}
