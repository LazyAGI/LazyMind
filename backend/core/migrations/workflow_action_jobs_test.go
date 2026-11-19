package migrations_test

import (
	"path/filepath"
	"sort"
	"testing"
)

func TestWorkflowActionJobsMigrationAndAggregate(t *testing.T) {
	for _, dialect := range []string{"sqlite", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			for _, aggregate := range []bool{false, true} {
				db := openDatabase(t, dialect)
				for _, version := range []string{"v0_1", "v0_2"} {
					applySQL(t, db, migrationPath(t, "version_mode/"+version+"/*.up.sql"), dialect)
				}
				if aggregate {
					applySQL(t, db, migrationPath(t, "version_mode/v0_3/*.up.sql"), dialect)
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
				_, err := db.Exec("INSERT INTO workflow_action_jobs(id,session_id,slot_id,list_index,base_revision,status,version,request_json,result_json,created_at,updated_at) VALUES ('job','session','preview_html',0,1,'waiting_agent',1,'{}','{}',CURRENT_TIMESTAMP,CURRENT_TIMESTAMP)")
				if err != nil {
					t.Fatal(err)
				}
				down := migrationPath(t, "dev_mode/v0_3/*_workflow_action_jobs.down.sql")
				if aggregate {
					down = migrationPath(t, "version_mode/v0_3/*.down.sql")
				}
				applySQL(t, db, down, dialect)
				if _, err := db.Exec("SELECT id FROM workflow_action_jobs"); err == nil {
					t.Fatal("down migration left artifact action table behind")
				}
			}
		})
	}
}
