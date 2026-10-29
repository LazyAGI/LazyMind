package migrations_test

import (
	"database/sql"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

func vocabularyCloudSchema(t *testing.T, db *sql.DB, dialect string) string {
	t.Helper()
	query := `SELECT type || '|' || name || '|' || COALESCE(sql,'') FROM sqlite_master WHERE tbl_name IN ('vocabulary_cloud_connections','vocabulary_cloud_operations') ORDER BY type,name`
	if dialect == "postgres" {
		query = `SELECT table_name || '|' || column_name || '|' || data_type || '|' || COALESCE(character_maximum_length::text,'') || '|' || is_nullable || '|' || COALESCE(column_default,'') FROM information_schema.columns WHERE table_schema=current_schema() AND table_name IN ('vocabulary_cloud_connections','vocabulary_cloud_operations') UNION ALL SELECT tablename || '|' || indexname || '|' || indexdef FROM pg_indexes WHERE schemaname=current_schema() AND tablename IN ('vocabulary_cloud_connections','vocabulary_cloud_operations')`
	}
	rows, err := db.Query(query)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var entries []string
	for rows.Next() {
		var entry string
		if err := rows.Scan(&entry); err != nil {
			t.Fatal(err)
		}
		entries = append(entries, strings.Join(strings.Fields(entry), " "))
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	sort.Strings(entries)
	return strings.Join(entries, "\n")
}

func assertCloudConstraints(t *testing.T, db *sql.DB) {
	t.Helper()
	if _, err := db.Exec(`INSERT INTO vocabulary_cloud_connections(id,owner_id,provider,token_cipher,created_at,updated_at) VALUES ('connection','owner','eudic','cipher',CURRENT_TIMESTAMP,CURRENT_TIMESTAMP)`); err != nil {
		t.Fatal(err)
	}
	var enabled, study bool
	var target, name, accountKey string
	if err := db.QueryRow(`SELECT enabled,add_to_study,target_id,target_name,account_key FROM vocabulary_cloud_connections WHERE id='connection'`).Scan(&enabled, &study, &target, &name, &accountKey); err != nil || enabled || study || target != "" || name != "" || accountKey != "" {
		t.Fatalf("connection defaults: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO vocabulary_cloud_connections(id,owner_id,provider,token_cipher,created_at,updated_at) VALUES ('duplicate','owner','eudic','cipher',CURRENT_TIMESTAMP,CURRENT_TIMESTAMP)`); err == nil {
		t.Fatal("duplicate owner/provider accepted")
	}
	insert := `INSERT INTO vocabulary_cloud_operations(id,owner_id,connection_id,provider,word_id,term,target_id,created_at,updated_at) VALUES ('%s','owner','connection','eudic','word','example','book',CURRENT_TIMESTAMP,CURRENT_TIMESTAMP)`
	if _, err := db.Exec(fmt.Sprintf(insert, "operation")); err != nil {
		t.Fatal(err)
	}
	var status, lastError, job string
	if err := db.QueryRow(`SELECT add_to_study,status,last_error,job_id FROM vocabulary_cloud_operations WHERE id='operation'`).Scan(&study, &status, &lastError, &job); err != nil || study || status != "pending" || lastError != "" || job != "" {
		t.Fatalf("operation defaults: %v", err)
	}
	if _, err := db.Exec(fmt.Sprintf(insert, "duplicate")); err == nil {
		t.Fatal("duplicate operation accepted")
	}
	if _, err := db.Exec(`INSERT INTO vocabulary_cloud_operations(id,owner_id,connection_id,provider,word_id,term,target_id,add_to_study,created_at,updated_at) VALUES ('study','owner','connection','eudic','word','example','book',TRUE,CURRENT_TIMESTAMP,CURRENT_TIMESTAMP)`); err != nil {
		t.Fatal("study opt-in must have distinct identity", err)
	}
	if _, err := db.Exec(`DELETE FROM vocabulary_cloud_connections WHERE id='connection'`); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM vocabulary_cloud_operations`).Scan(&count); err != nil || count != 2 {
		t.Fatal("disconnect must preserve pending operations", count, err)
	}
}

func TestVocabularyCloudMigrationParityAndRollback(t *testing.T) {
	for _, dialect := range []string{"sqlite", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			dev, aggregate := openDatabase(t, dialect), openDatabase(t, dialect)
			for _, db := range []*sql.DB{dev, aggregate} {
				for _, version := range []string{"v0_1", "v0_2"} {
					applySQL(t, db, migrationPath(t, "version_mode/"+version+"/*.up.sql"), dialect)
				}
			}
			up := migrationPath(t, "dev_mode/v0_3/*_vocabulary_cloud_sync.up.sql")
			paths, err := filepath.Glob("dev_mode/v0_3/*.up.sql")
			if err != nil {
				t.Fatal(err)
			}
			sort.Strings(paths)
			for _, path := range paths {
				if path != up {
					applySQL(t, dev, path, dialect)
				}
			}
			if _, err := dev.Exec(`INSERT INTO vocabulary_words(id,owner_id,provider,normalized_term,term) VALUES ('history','owner','local','example','Example')`); err != nil {
				t.Fatal(err)
			}
			applySQL(t, dev, up, dialect)
			aggregateUp := migrationPath(t, "version_mode/v0_3/*.up.sql")
			applySQL(t, aggregate, aggregateUp, dialect)
			if got, want := vocabularyCloudSchema(t, dev, dialect), vocabularyCloudSchema(t, aggregate, dialect); got != want || got == "" {
				t.Fatalf("cloud aggregate/dev schema differs:\n%s\n%s", got, want)
			}
			for _, db := range []*sql.DB{dev, aggregate} {
				if !strings.Contains(vocabularyCloudSchema(t, db, dialect), "idx_vocabulary_cloud_operations_owner_updated") {
					t.Fatal("missing owner/update index")
				}
				assertCloudConstraints(t, db)
			}
			applySQL(t, dev, strings.Replace(up, ".up.sql", ".down.sql", 1), dialect)
			var term string
			if err := dev.QueryRow(`SELECT term FROM vocabulary_words WHERE id='history'`).Scan(&term); err != nil || term != "Example" {
				t.Fatal("rollback lost vocabulary history", term, err)
			}
			applySQL(t, aggregate, strings.Replace(aggregateUp, ".up.sql", ".down.sql", 1), dialect)
			for _, db := range []*sql.DB{dev, aggregate} {
				if schema := vocabularyCloudSchema(t, db, dialect); schema != "" {
					t.Fatal("rollback left cloud schema", schema)
				}
			}
		})
	}
}
