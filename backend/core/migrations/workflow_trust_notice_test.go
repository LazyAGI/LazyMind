package migrations_test

import (
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

func TestWorkflowTrustNoticeMigrationPreservesPreferences(t *testing.T) {
	for _, dialect := range []string{"sqlite", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			for _, aggregate := range []bool{false, true} {
				db := openDatabase(t, dialect)
				for _, version := range []string{"v0_1", "v0_2"} {
					applySQL(t, db, migrationPath(t, "version_mode/"+version+"/*.up.sql"), dialect)
				}
				if _, err := db.Exec("INSERT INTO user_ui_preferences(user_id, chat_preference_notice_dismissed, created_at, updated_at) VALUES ('existing', TRUE, CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)"); err != nil {
					t.Fatal(err)
				}
				up := migrationPath(t, "dev_mode/v0_3/*_add_workflow_trust_notice_preference.up.sql")
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
				var dismissed, preserved bool
				if err := db.QueryRow("SELECT workflow_trust_notice_dismissed, chat_preference_notice_dismissed FROM user_ui_preferences WHERE user_id='existing'").Scan(&dismissed, &preserved); err != nil || dismissed || !preserved {
					t.Fatalf("upgrade changed preferences: %v %v %v", dismissed, preserved, err)
				}
				if _, err := db.Exec("UPDATE user_ui_preferences SET workflow_trust_notice_dismissed=TRUE WHERE user_id='existing'"); err != nil {
					t.Fatal(err)
				}
				applySQL(t, db, strings.Replace(up, ".up.sql", ".down.sql", 1), dialect)
				if err := db.QueryRow("SELECT chat_preference_notice_dismissed FROM user_ui_preferences WHERE user_id='existing'").Scan(&preserved); err != nil || !preserved {
					t.Fatalf("rollback lost preferences: %v", err)
				}
			}
		})
	}
}
