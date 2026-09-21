package migrate

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestConversationResultReadMigrationRoundTrip(t *testing.T) {
	for _, driver := range []string{"sqlite", "postgres"} {
		t.Run(driver, func(t *testing.T) {
			var db *sql.DB
			if driver == "postgres" {
				dsn := os.Getenv(migrationPostgresDSNEnv)
				if dsn == "" {
					t.Skip("PostgreSQL integration requires MIGRATION_TEST_POSTGRES_DSN")
				}
				db = createTemporaryPostgresDatabase(t, dsn, "result_reads")
			} else {
				db = openRawSQLite(t, t.TempDir()+"/result-reads.db")
			}
			paths, err := filepath.Glob("../migrations/dev_mode/v0_3/*_conversation_result_reads.up.sql")
			if err != nil || len(paths) != 1 {
				t.Fatalf("expected exactly one conversation result read migration, got %v err=%v", paths, err)
			}
			up := filepath.Clean(paths[0])
			down := strings.TrimSuffix(up, ".up.sql") + ".down.sql"
			catalog, err := (&Runner{dir: "../migrations"}).loadCatalog()
			if err != nil {
				t.Fatal(err)
			}
			for _, mode := range catalog.Modes {
				if len(mode.Dev) == 0 {
					execMigrationFileForDriver(t, db, mode.Aggregate.UpPath, driver)
					continue
				}
				for _, migration := range mode.Dev {
					if filepath.Clean(migration.UpPath) == up {
						break
					}
					execMigrationFileForDriver(t, db, migration.UpPath, driver)
				}
			}
			_, err = db.Exec(`INSERT INTO conversations (id, create_user_id, create_user_name, created_at, updated_at) VALUES ('legacy-conv', 'test-user', 'Test', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP);
INSERT INTO chat_histories (id, conversation_id, seq, content, result, run_id, run_status, create_time, update_time) VALUES ('legacy-reply', 'legacy-conv', 1, 'question', 'preserved answer', 'old-run', 'completed', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP);`)
			if err != nil {
				t.Fatal(err)
			}
			execMigrationFileForDriver(t, db, up, driver)
			var count int
			if err := db.QueryRow(`SELECT COUNT(*) FROM conversation_result_reads`).Scan(&count); err != nil || count != 0 {
				t.Fatalf("new receipts: count=%d err=%v", count, err)
			}
			if err := db.QueryRow(`SELECT COUNT(*) FROM conversation_result_read_state WHERE initialized = true`).Scan(&count); err != nil || count != 0 {
				t.Fatalf("baseline prematurely initialized: count=%d err=%v", count, err)
			}
			if _, err := db.Exec(`INSERT INTO conversation_result_reads (user_id, conversation_id, terminal_version) VALUES ('test-user', 'legacy-conv', 'version-1')`); err != nil {
				t.Fatal(err)
			}
			if _, err := db.Exec(`INSERT INTO conversation_result_reads (user_id, conversation_id, terminal_version) VALUES ('test-user', 'legacy-conv', 'version-1')`); err == nil {
				t.Fatal("duplicate receipt accepted")
			}
			if _, err := db.Exec(`INSERT INTO conversation_result_reads (user_id, conversation_id, terminal_version) VALUES ('other-user', 'legacy-conv', 'version-1'), ('test-user', 'legacy-conv', 'version-2')`); err != nil {
				t.Fatal(err)
			}
			execMigrationFileForDriver(t, db, down, driver)
			for _, table := range []string{"conversation_result_reads", "conversation_result_read_state"} {
				if rows, err := db.Query("SELECT * FROM " + table); err == nil {
					rows.Close()
					t.Errorf("down retained %s", table)
				}
			}
			var answer, status string
			if err := db.QueryRow(`SELECT result, run_status FROM chat_histories WHERE id='legacy-reply'`).Scan(&answer, &status); err != nil || answer != "preserved answer" || status != "completed" {
				t.Fatalf("history changed: %q %q err=%v", answer, status, err)
			}
			if err := db.QueryRow(`SELECT COUNT(*) FROM conversations WHERE id='legacy-conv'`).Scan(&count); err != nil || count != 1 {
				t.Fatalf("conversation lost: count=%d err=%v", count, err)
			}
			execMigrationFileForDriver(t, db, up, driver)
			if err := db.QueryRow(`SELECT COUNT(*) FROM conversation_result_reads`).Scan(&count); err != nil || count != 0 {
				t.Fatalf("up after rollback: count=%d err=%v", count, err)
			}
		})
	}
}

func resultReadSQLiteStructure(t *testing.T, db *sql.DB, table string) []string {
	t.Helper()
	rows, err := db.Query("SELECT name, type, \"notnull\", COALESCE(dflt_value, ''), pk FROM pragma_table_info(?) ORDER BY cid", table)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var result []string
	for rows.Next() {
		var name, typ, def string
		var notNull, primaryKey int
		if err := rows.Scan(&name, &typ, &notNull, &def, &primaryKey); err != nil {
			t.Fatal(err)
		}
		result = append(result, fmt.Sprintf("column:%s:%s:%d:%s:%d", name, strings.ToLower(typ), notNull, def, primaryKey))
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if len(result) == 0 {
		t.Fatalf("missing table %s", table)
	}
	indexes, err := db.Query(`SELECT il.name, il."unique", il.origin, il.partial, ii.seqno, ii.name FROM pragma_index_list(?) il JOIN pragma_index_info(il.name) ii ORDER BY il.name, ii.seqno`, table)
	if err != nil {
		t.Fatal(err)
	}
	defer indexes.Close()
	for indexes.Next() {
		var name, origin, column string
		var unique, partial, seq int
		if err := indexes.Scan(&name, &unique, &origin, &partial, &seq, &column); err != nil {
			t.Fatal(err)
		}
		result = append(result, fmt.Sprintf("index:%s:%d:%s:%d:%d:%s", name, unique, origin, partial, seq, column))
	}
	if err := indexes.Err(); err != nil {
		t.Fatal(err)
	}
	return result
}

func TestConversationResultReadSQLiteAggregateEquivalence(t *testing.T) {
	catalog, err := (&Runner{dir: "../migrations"}).loadCatalog()
	if err != nil {
		t.Fatal(err)
	}
	if len(catalog.Modes) != 3 || catalog.Modes[2].Name != "v0_3" || catalog.Modes[2].Aggregate == nil {
		t.Fatal("expected the existing v0_3 aggregate")
	}
	current := catalog.Modes[2]
	release, dev := openRawSQLite(t, t.TempDir()+"/release.db"), openRawSQLite(t, t.TempDir()+"/dev.db")
	for _, db := range []*sql.DB{release, dev} {
		for _, mode := range catalog.Modes[:2] {
			execMigrationFileForDriver(t, db, mode.Aggregate.UpPath, "sqlite")
		}
		if _, err := db.Exec(`INSERT INTO conversations (id, create_user_id, create_user_name, created_at, updated_at) VALUES ('old-conv', 'test-user', 'Test', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP);
INSERT INTO chat_histories (id, conversation_id, seq, content, result, create_time, update_time) VALUES ('old-reply', 'old-conv', 1, 'question', 'preserved answer', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP);`); err != nil {
			t.Fatal(err)
		}
	}
	execMigrationFileForDriver(t, release, current.Aggregate.UpPath, "sqlite")
	for _, migration := range current.Dev {
		execMigrationFileForDriver(t, dev, migration.UpPath, "sqlite")
	}
	for _, table := range []string{"conversation_result_reads", "conversation_result_read_state"} {
		if a, b := resultReadSQLiteStructure(t, release, table), resultReadSQLiteStructure(t, dev, table); !reflect.DeepEqual(a, b) {
			t.Fatalf("%s aggregate/dev structure mismatch: %v != %v", table, a, b)
		}
		var releaseCount, devCount int
		if err := release.QueryRow("SELECT COUNT(*) FROM " + table).Scan(&releaseCount); err != nil {
			t.Fatal(err)
		}
		if err := dev.QueryRow("SELECT COUNT(*) FROM " + table).Scan(&devCount); err != nil {
			t.Fatal(err)
		}
		if releaseCount != 0 || devCount != 0 {
			t.Fatalf("%s should start without runtime state: aggregate=%d dev=%d", table, releaseCount, devCount)
		}
	}
	for _, db := range []*sql.DB{release, dev} {
		if _, err := db.Exec(`INSERT INTO conversation_result_reads (user_id, conversation_id, terminal_version) VALUES ('test-user', 'old-conv', 'version-1')`); err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(`INSERT INTO conversation_result_reads (user_id, conversation_id, terminal_version) VALUES ('test-user', 'old-conv', 'version-1')`); err == nil {
			t.Fatal("path permits duplicate result receipts")
		}
		var answer string
		if err := db.QueryRow(`SELECT result FROM chat_histories WHERE id='old-reply'`).Scan(&answer); err != nil || answer != "preserved answer" {
			t.Fatalf("upgrade lost old history: %q err=%v", answer, err)
		}
	}
	execMigrationFileForDriver(t, release, current.Aggregate.DownPath, "sqlite")
	for _, table := range []string{"conversation_result_reads", "conversation_result_read_state"} {
		if rows, err := release.Query("SELECT * FROM " + table); err == nil {
			rows.Close()
			t.Errorf("aggregate down retained %s", table)
		}
	}
	var answer string
	if err := release.QueryRow(`SELECT result FROM chat_histories WHERE id='old-reply'`).Scan(&answer); err != nil || answer != "preserved answer" {
		t.Fatalf("aggregate rollback lost old history: %q err=%v", answer, err)
	}
}
