package migrations_test

import (
	"database/sql"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

func TestAggregateMatchesCompleteDevPathAndRollsBack(t *testing.T) {
	type schemaCheck struct {
		name   string
		schema func(*testing.T, *sql.DB, string) string
	}
	for _, dialect := range []string{"sqlite", "postgres"} {
		t.Run(dialect, func(t *testing.T) {
			checks := []schemaCheck{
				{"ordinary_display", ordinaryTableSchema},
				{"resource_commands", resourceSchema},
			}
			if dialect == "sqlite" {
				checks = append(checks, schemaCheck{"skill_discovery", func(t *testing.T, db *sql.DB, _ string) string {
					return sqliteSkillSchema(t, db)
				}})
			}

			// These domains inspect the same unseeded migration paths. Keep their
			// assertions read-only so one isolated pair of databases covers them all.
			aggregateDB, devDB := openDatabase(t, dialect), openDatabase(t, dialect)
			for _, db := range []*sql.DB{aggregateDB, devDB} {
				for _, version := range []string{"v0_1", "v0_2"} {
					applySQL(t, db, migrationPath(t, "version_mode/"+version+"/*.up.sql"), dialect)
				}
			}
			baseline := make([]string, len(checks))
			for i, check := range checks {
				baseline[i] = check.schema(t, aggregateDB, dialect)
			}

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
			t.Run("aggregate_matches_dev", func(t *testing.T) {
				for _, check := range checks {
					t.Run(check.name, func(t *testing.T) {
						if a, b := check.schema(t, aggregateDB, dialect), check.schema(t, devDB, dialect); a != b {
							t.Fatalf("aggregate/dev schema diverged\n%s\n---\n%s", a, b)
						}
					})
				}
			})

			applySQL(t, aggregateDB, strings.Replace(aggregate, ".up.sql", ".down.sql", 1), dialect)
			t.Run("aggregate_rollback", func(t *testing.T) {
				for i, check := range checks {
					t.Run(check.name, func(t *testing.T) {
						if got := check.schema(t, aggregateDB, dialect); got != baseline[i] {
							t.Fatalf("aggregate rollback diverged\n%s\n---\n%s", got, baseline[i])
						}
					})
				}
			})
		})
	}
}
