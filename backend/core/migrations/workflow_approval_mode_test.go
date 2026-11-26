package migrations_test

import (
	"encoding/json"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

func TestWorkflowApprovalModeMigration(t *testing.T) {
	for _, dialect := range []string{"sqlite", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			for _, aggregate := range []bool{false, true} {
				db := openDatabase(t, dialect)
				for _, version := range []string{"v0_1", "v0_2"} {
					applySQL(t, db, migrationPath(t, "version_mode/"+version+"/*.up.sql"), dialect)
				}
				if _, err := db.Exec("INSERT INTO plugin_sessions(id,conversation_id,plugin_id,created_at,updated_at) VALUES ('existing','chat','workflow',CURRENT_TIMESTAMP,CURRENT_TIMESTAMP)"); err != nil {
					t.Fatal(err)
				}
				up := migrationPath(t, "dev_mode/v0_3/*_workflow_session_approval_mode.up.sql")
				if aggregate {
					up = migrationPath(t, "version_mode/v0_3/*.up.sql")
					applySQL(t, db, up, dialect)
				} else {
					paths, err := filepath.Glob("dev_mode/v0_3/*.up.sql")
					if err != nil {
						t.Fatal(err)
					}
					sort.Strings(paths)
					for _, path := range paths {
						applySQL(t, db, path, dialect)
					}
				}
				var mode, workflow, snapshot string
				if err := db.QueryRow("SELECT approval_mode,plugin_id,approval_preferences FROM plugin_sessions WHERE id='existing'").Scan(&mode, &workflow, &snapshot); err != nil || mode != "" || workflow != "workflow" || snapshot != "{}" {
					t.Fatalf("data not preserved: %q %q %v", mode, workflow, err)
				}
				if _, err := db.Exec("UPDATE plugin_sessions SET approval_mode='auto' WHERE id='existing'"); err != nil {
					t.Fatal(err)
				}
				if !aggregate {
					applySQL(t, db, migrationPath(t, "dev_mode/v0_3/*_workflow_approval_snapshot.down.sql"), dialect)
				}
				applySQL(t, db, strings.Replace(up, ".up.sql", ".down.sql", 1), dialect)
				for _, column := range importTableColumns(t, db, "plugin_sessions", dialect) {
					if strings.HasPrefix(column, "approval_mode:") || strings.HasPrefix(column, "approval_preferences:") {
						t.Fatalf("rollback retained approval column (aggregate=%v): %s", aggregate, column)
					}
				}
				if err := db.QueryRow("SELECT plugin_id FROM plugin_sessions WHERE id='existing'").Scan(&workflow); err != nil || workflow != "workflow" {
					t.Fatalf("rollback lost data: %v", err)
				}
			}
		})
	}
}

func TestWorkflowApprovalSnapshotBackfill(t *testing.T) {
	for _, dialect := range []string{"sqlite", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			db := openDatabase(t, dialect)
			_, err := db.Exec(`CREATE TABLE plugin_sessions (id TEXT PRIMARY KEY, create_user_id TEXT, plugin_id TEXT);
CREATE TABLE workflow_approval_preferences (user_id TEXT, workflow_id TEXT, step_id TEXT, approval_required BOOLEAN);
INSERT INTO plugin_sessions VALUES ('existing','owner','workflow'), ('other','someone','workflow'), ('no-pref','owner','another-workflow');
INSERT INTO workflow_approval_preferences VALUES ('owner','workflow','*',FALSE), ('owner','workflow','review',TRUE), ('someone','workflow','*',TRUE);`)
			if err != nil {
				t.Fatal(err)
			}
			up := migrationPath(t, "dev_mode/v0_3/*_workflow_approval_snapshot.up.sql")
			applySQL(t, db, up, dialect)
			if _, err := db.Exec("DELETE FROM workflow_approval_preferences"); err != nil {
				t.Fatal(err)
			}
			for id, want := range map[string]map[string]bool{"existing": {"*": false, "review": true}, "other": {"*": true}, "no-pref": {}} {
				var raw string
				if err := db.QueryRow("SELECT approval_preferences FROM plugin_sessions WHERE id='" + id + "'").Scan(&raw); err != nil {
					t.Fatal(err)
				}
				var got map[string]bool
				if err := json.Unmarshal([]byte(raw), &got); err != nil {
					t.Fatal(err)
				}
				if len(got) != len(want) {
					t.Fatalf("snapshot %s: %s", id, raw)
				}
				for step, required := range want {
					if value, ok := got[step]; !ok || value != required {
						t.Fatalf("snapshot %s: %s", id, raw)
					}
				}
			}
			applySQL(t, db, strings.Replace(up, ".up.sql", ".down.sql", 1), dialect)
			var count int
			if err := db.QueryRow("SELECT COUNT(*) FROM plugin_sessions").Scan(&count); err != nil || count != 3 {
				t.Fatalf("rollback lost rows: %d %v", count, err)
			}
		})
	}
}
