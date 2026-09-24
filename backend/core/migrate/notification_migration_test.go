package migrate

import (
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Both dialects run the repository SQL, never AutoMigrate.
func TestNotificationMigrationPaths(t *testing.T) {
	driver := strings.TrimSpace(os.Getenv("TEST_DB_DRIVER"))
	if driver == "" {
		driver = "sqlite"
	}
	if driver != "sqlite" && driver != "postgres" {
		t.Fatalf("unsupported test driver %q", driver)
	}
	catalog, err := (&Runner{dir: "../migrations"}).loadCatalog()
	if err != nil {
		t.Fatal(err)
	}
	current := catalog.Modes[len(catalog.Modes)-1]
	var feature *migrationFile
	for i := range current.Dev {
		if strings.HasSuffix(current.Dev[i].Name, "/add_task_notifications") {
			feature = &current.Dev[i]
		}
	}
	open := func(t *testing.T, label string) *sql.DB {
		t.Helper()
		if driver == "postgres" {
			dsn := strings.TrimSpace(os.Getenv(migrationPostgresDSNEnv))
			if dsn == "" {
				dsn = strings.TrimSpace(os.Getenv("TEST_DB_DSN"))
			}
			if dsn == "" {
				t.Fatal("PostgreSQL migration tests require MIGRATION_TEST_POSTGRES_DSN or TEST_DB_DSN")
			}
			return createTemporaryPostgresDatabase(t, dsn, "notifications_"+label)
		}
		return openRawSQLite(t, filepath.Join(t.TempDir(), label+".db"))
	}
	for _, path := range []string{"upgrade_and_down", "aggregate", "dev"} {
		t.Run(path, func(t *testing.T) {
			db := open(t, path)
			if feature == nil || feature.UpPath == "" || feature.DownPath == "" {
				t.Fatal("missing new add_task_notifications dev migration up/down pair")
			}
			for _, mode := range catalog.Modes[:len(catalog.Modes)-1] {
				if mode.Aggregate == nil {
					t.Fatalf("missing previous aggregate: %s", mode.Name)
				}
				execMigrationFileForDriver(t, db, mode.Aggregate.UpPath, driver)
			}
			if path == "aggregate" {
				if current.Aggregate == nil {
					t.Fatal("expected existing v0_3 aggregate")
				}
				execMigrationFileForDriver(t, db, current.Aggregate.UpPath, driver)
			} else {
				for _, migration := range current.Dev {
					if migration.UpPath == feature.UpPath && path == "upgrade_and_down" {
						seedNotificationLegacySchedule(t, db)
					}
					execMigrationFileForDriver(t, db, migration.UpPath, driver)
				}
			}
			for _, table := range []string{"user_notification_preferences", "task_notifications", "desktop_notification_receipts"} {
				rows, err := db.Query("SELECT * FROM " + table + " WHERE 1 = 0")
				if err != nil {
					t.Fatalf("%s path missing %s: %v", path, table, err)
				}
				_ = rows.Close()
			}
			for _, table := range []string{"user_schedules", "task_center_tasks"} {
				rows, err := db.Query("SELECT notification_config, notification_revision FROM " + table + " WHERE 1 = 0")
				if err != nil {
					t.Fatalf("%s path missing notification configuration columns: %v", path, err)
				}
				_ = rows.Close()
			}
			if path == "upgrade_and_down" {
				var config sql.NullString
				var revision int
				if err := db.QueryRow("SELECT notification_config, notification_revision FROM user_schedules WHERE id = 'legacy-notification'").Scan(&config, &revision); err != nil {
					t.Fatal(err)
				}
				if config.Valid || revision != 0 {
					t.Fatal("migration enabled notifications for an unconfigured legacy schedule")
				}
				execMigrationFileForDriver(t, db, feature.DownPath, driver)
				var name string
				if err := db.QueryRow("SELECT name FROM user_schedules WHERE id = 'legacy-notification'").Scan(&name); err != nil || name != "旧任务" {
					t.Fatalf("rollback damaged existing schedule: %v", err)
				}
				execMigrationFileForDriver(t, db, feature.UpPath, driver)
				if err := db.QueryRow("SELECT notification_config FROM user_schedules WHERE id = 'legacy-notification'").Scan(&config); err != nil || config.Valid {
					t.Fatalf("up/down/up must preserve the legacy unconfigured state: %v", err)
				}
			}
		})
	}
}

func seedNotificationLegacySchedule(t *testing.T, db *sql.DB) {
	t.Helper()
	if _, err := db.Exec(`INSERT INTO user_schedules
(id, user_id, name, cron_expr, prompt_template, next_run_at, created_at)
VALUES ('legacy-notification', 'legacy-owner', '旧任务', '0 9 * * *', '整理日报', CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`); err != nil {
		t.Fatal(err)
	}
}
