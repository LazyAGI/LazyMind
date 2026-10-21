package migrations_test

import (
	"os"
	"strings"
	"testing"
)

func TestTaskTimingMigrationRoundTrip(t *testing.T) {
	for _, dialect := range []string{"sqlite", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			db := openDatabase(t, dialect)
			if _, err := db.Exec("CREATE TABLE preserved_history(id VARCHAR(36) PRIMARY KEY, content TEXT); INSERT INTO preserved_history VALUES ('history','preserved')"); err != nil {
				t.Fatal(err)
			}
			up := migrationPath(t, "dev_mode/v0_3/*_task_execution_timing.up.sql")
			applySQL(t, db, up, dialect)
			if _, err := db.Exec("INSERT INTO task_execution_timings(id,run_id,conversation_id,owner_user_id,history_id,status,requested_at) VALUES ('run','history','conv','owner','history','running',CURRENT_TIMESTAMP)"); err != nil {
				t.Fatal(err)
			}
			applySQL(t, db, strings.Replace(up, ".up.sql", ".down.sql", 1), dialect)
			var content string
			if err := db.QueryRow("SELECT content FROM preserved_history WHERE id='history'").Scan(&content); err != nil || content != "preserved" {
				t.Fatalf("history lost: %s %v", content, err)
			}
			applySQL(t, db, up, dialect)
			var count int
			if err := db.QueryRow("SELECT COUNT(*) FROM task_execution_timings").Scan(&count); err != nil || count != 0 {
				t.Fatalf("fabricated legacy timing: %d %v", count, err)
			}
		})
	}
}
func TestTaskTimingAggregateIncludesFinalSchema(t *testing.T) {
	for _, direction := range []string{"up", "down"} {
		dev := migrationPath(t, "dev_mode/v0_3/*_task_execution_timing."+direction+".sql")
		aggregate := migrationPath(t, "version_mode/v0_3/*."+direction+".sql")
		a, err := os.ReadFile(aggregate)
		if err != nil {
			t.Fatal(err)
		}
		b, err := os.ReadFile(dev)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(a), string(b)) {
			t.Fatalf("aggregate missing final timing %s schema", direction)
		}
	}
}
