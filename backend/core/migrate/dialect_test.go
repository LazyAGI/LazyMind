package migrate

import (
	"errors"
	"os"
	"strings"
	"testing"
)

func TestMigrationSQLForDriver(t *testing.T) {
	body := `-- +migrate Up
-- common comment
-- +migrate Dialect postgres
ALTER TABLE public.items ADD COLUMN payload jsonb;
-- +migrate Dialect sqlite
ALTER TABLE items ADD COLUMN payload text;
`
	postgres, err := migrationSQLForDriver(body, "postgres")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(postgres, "jsonb") || strings.Contains(postgres, "payload text") {
		t.Fatalf("unexpected PostgreSQL SQL: %s", postgres)
	}
	sqlite, err := migrationSQLForDriver(body, "sqlite")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(sqlite, "payload text") || strings.Contains(sqlite, "jsonb") {
		t.Fatalf("unexpected SQLite SQL: %s", sqlite)
	}
	if _, err := migrationSQLForDriver(body, "mysql"); err == nil {
		t.Fatal("missing dialect should fail")
	}
}

func TestRepositoryMigrationsSupportPostgresAndSQLite(t *testing.T) {
	runner := &Runner{dir: "../migrations"}
	catalog, err := runner.loadCatalog()
	if err != nil {
		t.Fatalf("load migration catalog: %v", err)
	}
	for _, migration := range catalog.All {
		for _, path := range []string{migration.UpPath, migration.DownPath} {
			if path == "" {
				continue
			}
			body, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read migration %s: %v", path, err)
			}
			for _, driver := range []string{"postgres", "sqlite"} {
				if _, err := migrationSQLForDriver(string(body), driver); err != nil {
					t.Errorf("migration %s does not support %s: %v", path, driver, err)
				}
			}
		}
	}
}

func TestMigrationSQLWithoutDialectIsPortable(t *testing.T) {
	body := "ALTER TABLE items ADD COLUMN name text;"
	for _, driver := range []string{"postgres", "sqlite"} {
		got, err := migrationSQLForDriver(body, driver)
		if err != nil || got != body {
			t.Fatalf("driver=%s SQL=%q err=%v", driver, got, err)
		}
	}
}

func TestBenignSQLiteColumnChangeError(t *testing.T) {
	addSQL := "-- comment\nALTER TABLE user_ui_preferences ADD COLUMN accepted_user_agreement_version varchar(64) NOT NULL DEFAULT '';"
	dropSQL := "ALTER TABLE user_ui_preferences DROP COLUMN accepted_user_agreement_version;"
	if !isBenignSQLiteColumnChangeError(addSQL, errors.New("duplicate column name: accepted_user_agreement_version")) {
		t.Fatal("expected duplicate ADD COLUMN error to be benign")
	}
	if !isBenignSQLiteColumnChangeError(dropSQL, errors.New("no such column: accepted_user_agreement_version")) {
		t.Fatal("expected missing DROP COLUMN error to be benign")
	}
	if isBenignSQLiteColumnChangeError(addSQL, errors.New("no such table: user_ui_preferences")) {
		t.Fatal("table missing errors must not be ignored")
	}
}
