package migrations_test

import (
	"database/sql"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

func TestOrdinaryDisplayMigrationPreservesHistoryAndRollsBack(t *testing.T) {
	for _, dialect := range []string{"sqlite", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			db := openDatabase(t, dialect)
			_, err := db.Exec(`CREATE TABLE sub_agent_tasks(id VARCHAR(36) PRIMARY KEY, title TEXT NOT NULL);
CREATE TABLE sub_agent_steps(id VARCHAR(36) PRIMARY KEY, task_id VARCHAR(36) NOT NULL, role VARCHAR(16) NOT NULL, seq INTEGER NOT NULL, content TEXT);
CREATE TABLE sub_agent_artifacts(id VARCHAR(36) PRIMARY KEY, task_id VARCHAR(36) NOT NULL, value TEXT);
INSERT INTO sub_agent_tasks VALUES ('history','Preserved task');
INSERT INTO sub_agent_steps VALUES ('step','history','think',1,'Preserved private history');
INSERT INTO sub_agent_artifacts VALUES ('artifact','history','Preserved output');`)
			if err != nil {
				t.Fatal(err)
			}
			up := migrationPath(t, "dev_mode/v0_3/*_ordinary_task_display.up.sql")
			applySQL(t, db, up, dialect)
			var execution string
			var revision int64
			var started, finished sql.NullTime
			if err := db.QueryRow("SELECT execution_id,display_revision,started_at,finished_at FROM sub_agent_tasks WHERE id='history'").Scan(&execution, &revision, &started, &finished); err != nil {
				t.Fatal(err)
			}
			if execution != "" || revision != 0 || started.Valid || finished.Valid {
				t.Fatal("migration fabricated execution/timing for historical rows")
			}
			if _, err := db.Exec("UPDATE sub_agent_tasks SET execution_id='new',display_revision=2 WHERE id='history'"); err != nil {
				t.Fatal(err)
			}
			applySQL(t, db, strings.Replace(up, ".up.sql", ".down.sql", 1), dialect)
			for _, check := range []struct{ query, want string }{{"SELECT title FROM sub_agent_tasks WHERE id='history'", "Preserved task"}, {"SELECT content FROM sub_agent_steps WHERE id='step'", "Preserved private history"}, {"SELECT value FROM sub_agent_artifacts WHERE id='artifact'", "Preserved output"}} {
				var got string
				if err := db.QueryRow(check.query).Scan(&got); err != nil || got != check.want {
					t.Fatalf("historical row lost: %q %v", got, err)
				}
			}
		})
	}
}

func ordinaryTableSchema(t *testing.T, db *sql.DB, dialect string) string {
	t.Helper()
	lines := []string{}
	for _, table := range []string{"sub_agent_tasks", "sub_agent_steps", "sub_agent_artifacts"} {
		if dialect == "sqlite" {
			rows, err := db.Query("PRAGMA table_info(" + table + ")")
			if err != nil {
				t.Fatal(err)
			}
			for rows.Next() {
				var cid, notnull, pk int
				var name, kind string
				var def sql.NullString
				if err := rows.Scan(&cid, &name, &kind, &notnull, &def, &pk); err != nil {
					t.Fatal(err)
				}
				lines = append(lines, fmt.Sprintf("%s|%s|%s|%d|%s|%d", table, name, strings.ToLower(kind), notnull, def.String, pk))
			}
			rows.Close()
		} else {
			rows, err := db.Query("SELECT column_name,data_type,is_nullable,COALESCE(column_default,'') FROM information_schema.columns WHERE table_schema='public' AND table_name=$1", table)
			if err != nil {
				t.Fatal(err)
			}
			for rows.Next() {
				var name, kind, nullable, def string
				if err := rows.Scan(&name, &kind, &nullable, &def); err != nil {
					t.Fatal(err)
				}
				lines = append(lines, fmt.Sprintf("%s|%s|%s|%s|%s", table, name, kind, nullable, def))
			}
			rows.Close()
		}
		var indexRows *sql.Rows
		var indexErr error
		if dialect == "sqlite" {
			indexRows, indexErr = db.Query("SELECT name,COALESCE(sql,'') FROM sqlite_master WHERE type='index' AND tbl_name=?", table)
		} else {
			indexRows, indexErr = db.Query("SELECT indexname,indexdef FROM pg_indexes WHERE schemaname='public' AND tablename=$1", table)
		}
		if indexErr != nil {
			t.Fatal(indexErr)
		}
		for indexRows.Next() {
			var name, definition string
			if err := indexRows.Scan(&name, &definition); err != nil {
				t.Fatal(err)
			}
			lines = append(lines, "index|"+name+"|"+strings.Join(strings.Fields(definition), " "))
		}
		indexRows.Close()
	}
	sort.Strings(lines)
	return strings.Join(lines, "\n")
}

func TestOrdinaryDisplayAggregateMatchesCompleteDevPath(t *testing.T) {
	for _, dialect := range []string{"sqlite", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			aggregateDB, devDB := openDatabase(t, dialect), openDatabase(t, dialect)
			for _, db := range []*sql.DB{aggregateDB, devDB} {
				for _, version := range []string{"v0_1", "v0_2"} {
					applySQL(t, db, migrationPath(t, "version_mode/"+version+"/*.up.sql"), dialect)
				}
			}
			baseline := ordinaryTableSchema(t, aggregateDB, dialect)
			aggregate := migrationPath(t, "version_mode/v0_3/*.up.sql")
			applySQL(t, aggregateDB, aggregate, dialect)
			paths, err := filepath.Glob("dev_mode/v0_3/*.up.sql")
			if err != nil {
				t.Fatal(err)
			}
			sort.Strings(paths)
			for _, path := range paths {
				applySQL(t, devDB, path, dialect)
			}
			if a, b := ordinaryTableSchema(t, aggregateDB, dialect), ordinaryTableSchema(t, devDB, dialect); a != b {
				t.Fatalf("aggregate/dev diverged\n%s\n---\n%s", a, b)
			}
			applySQL(t, aggregateDB, strings.Replace(aggregate, ".up.sql", ".down.sql", 1), dialect)
			if got := ordinaryTableSchema(t, aggregateDB, dialect); got != baseline {
				t.Fatalf("aggregate rollback diverged\n%s\n---\n%s", got, baseline)
			}
		})
	}
}
