package migrations_test

import (
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

func TestReadingPolicyMigrationAndAggregate(t *testing.T) {
	for _, dialect := range []string{"sqlite", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			for _, aggregate := range []bool{false, true} {
				db := openDatabase(t, dialect)
				for _, version := range []string{"v0_1", "v0_2"} {
					applySQL(t, db, migrationPath(t, "version_mode/"+version+"/*.up.sql"), dialect)
				}
				if _, err := db.Exec("CREATE TABLE reading_policy_existing (id INTEGER PRIMARY KEY, value TEXT); INSERT INTO reading_policy_existing VALUES (1,'preserved')"); err != nil {
					t.Fatal(err)
				}
				up := migrationPath(t, "dev_mode/v0_3/*_create_reading_policies.up.sql")
				if aggregate {
					up = migrationPath(t, "version_mode/v0_3/*.up.sql")
					applySQL(t, db, up, dialect)
				} else {
					paths, _ := filepath.Glob("dev_mode/v0_3/*.up.sql")
					sort.Strings(paths)
					for _, p := range paths {
						applySQL(t, db, p, dialect)
					}
				}
				if _, err := db.Exec("INSERT INTO user_reading_policies(user_id,policy_json,updated_at) VALUES ('u','{}',CURRENT_TIMESTAMP)"); err != nil {
					t.Fatal(err)
				}
				var value string
				if err := db.QueryRow("SELECT policy_json FROM user_reading_policies WHERE user_id='u'").Scan(&value); err != nil || value != "{}" {
					t.Fatalf("schema/data: %s %v", value, err)
				}
				applySQL(t, db, strings.Replace(up, ".up.sql", ".down.sql", 1), dialect)
				if err := db.QueryRow("SELECT value FROM reading_policy_existing WHERE id=1").Scan(&value); err != nil || value != "preserved" {
					t.Fatalf("existing data lost: %s %v", value, err)
				}
			}
		})
	}
}
