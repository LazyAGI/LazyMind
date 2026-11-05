package migrations_test

import (
	"database/sql"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

func readingIndexes(t *testing.T, db *sql.DB, dialect string) string {
	t.Helper()
	query := `SELECT name || '|' || sql FROM sqlite_master WHERE type='index' AND name LIKE 'idx_reading_%' ORDER BY name`
	if dialect == "postgres" {
		query = `SELECT indexname || '|' || indexdef FROM pg_indexes WHERE schemaname=current_schema() AND indexname LIKE 'idx_reading_%' ORDER BY indexname`
	}
	rows, err := db.Query(query)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var definitions []string
	for rows.Next() {
		var line string
		if err := rows.Scan(&line); err != nil {
			t.Fatal(err)
		}
		definitions = append(definitions, strings.Join(strings.Fields(line), " "))
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return strings.Join(definitions, "\n")
}

func TestReadingPreparationIndexesMigrationParityAndRollback(t *testing.T) {
	for _, dialect := range []string{"sqlite", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			dev, aggregate := openDatabase(t, dialect), openDatabase(t, dialect)
			for _, db := range []*sql.DB{dev, aggregate} {
				for _, version := range []string{"v0_1", "v0_2"} {
					applySQL(t, db, migrationPath(t, "version_mode/"+version+"/*.up.sql"), dialect)
				}
			}
			up := migrationPath(t, "dev_mode/v0_3/*_index_reading_preparation.up.sql")
			paths, _ := filepath.Glob("dev_mode/v0_3/*.up.sql")
			sort.Strings(paths)
			for _, path := range paths {
				if path != up {
					applySQL(t, dev, path, dialect)
				}
			}
			// Existing queue data must survive indexing and its rollback.
			if _, err := dev.Exec(`INSERT INTO async_jobs(id,job_type,status,payload_json,next_run_at,created_at,updated_at) VALUES ('preserved','document_reading_preparation','failed','{"dataset_id":"ds","document_id":"doc","source_sha256":"hash"}',CURRENT_TIMESTAMP,CURRENT_TIMESTAMP,CURRENT_TIMESTAMP)`); err != nil {
				t.Fatal(err)
			}
			applySQL(t, dev, up, dialect)
			aggregateUp := migrationPath(t, "version_mode/v0_3/*.up.sql")
			applySQL(t, aggregate, aggregateUp, dialect)
			if a, b := readingIndexes(t, dev, dialect), readingIndexes(t, aggregate, dialect); a != b || strings.Count(a, "\n") != 3 {
				t.Fatalf("index parity:\n%s\n%s", a, b)
			}
			var id string
			if err := dev.QueryRow(`SELECT id FROM async_jobs WHERE payload_json ->> 'dataset_id'='ds' AND payload_json ->> 'document_id'='doc' AND payload_json ->> 'source_sha256'='hash'`).Scan(&id); err != nil || id != "preserved" {
				t.Fatalf("JSON query: %s %v", id, err)
			}
			applySQL(t, dev, strings.Replace(up, ".up.sql", ".down.sql", 1), dialect)
			if readingIndexes(t, dev, dialect) != "" {
				t.Fatal("rollback retained indexes")
			}
			if err := dev.QueryRow(`SELECT id FROM async_jobs WHERE id='preserved'`).Scan(&id); err != nil {
				t.Fatal("rollback lost job", err)
			}
			applySQL(t, aggregate, strings.Replace(aggregateUp, ".up.sql", ".down.sql", 1), dialect)
			if readingIndexes(t, aggregate, dialect) != "" {
				t.Fatal("aggregate rollback retained indexes")
			}
		})
	}
}
