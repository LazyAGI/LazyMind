package migrate

import (
	"database/sql"
	"os"
	"strings"
	"testing"
)

func githubStarMigrationPostgresDSN(t *testing.T) string {
	t.Helper()
	dsn := strings.TrimSpace(os.Getenv(migrationPostgresDSNEnv))
	if dsn == "" {
		dsn = strings.TrimSpace(os.Getenv("TEST_DB_DSN"))
	}
	if dsn == "" {
		if os.Getenv("TEST_DB_DRIVER") == "postgres" {
			t.Fatal("PostgreSQL CI requires MIGRATION_TEST_POSTGRES_DSN or TEST_DB_DSN")
		}
		t.Skip("set MIGRATION_TEST_POSTGRES_DSN or TEST_DB_DSN to an isolated PostgreSQL test server")
	}
	return dsn
}

func TestGitHubStarMergedAggregateRoundTrip(t *testing.T) {
	for _, driver := range []string{"sqlite", "postgres"} {
		t.Run(driver, func(t *testing.T) {
			var db *sql.DB
			if driver == "postgres" {
				db = createTemporaryPostgresDatabase(t, githubStarMigrationPostgresDSN(t), "star_aggregate")
			} else {
				db = openRawSQLite(t, t.TempDir()+"/aggregate.db")
			}
			catalog, err := (&Runner{dir: "../migrations"}).loadCatalog()
			if err != nil {
				t.Fatal(err)
			}
			for _, mode := range catalog.Modes[:len(catalog.Modes)-1] {
				execMigrationFileForDriver(t, db, mode.Aggregate.UpPath, driver)
			}
			if _, err := db.Exec("CREATE TABLE star_legacy_fixture (id TEXT PRIMARY KEY, value TEXT NOT NULL); INSERT INTO star_legacy_fixture VALUES ('existing-user','keep-existing-preferences')"); err != nil {
				t.Fatal(err)
			}
			current := catalog.Modes[len(catalog.Modes)-1]
			execMigrationFileForDriver(t, db, current.Aggregate.UpPath, driver)
			for _, query := range []string{
				"SELECT user_id,prompted_task_ids,last_prompt_at,supported,created_at,updated_at FROM user_github_star_prompts LIMIT 0",
				"SELECT scope,active_model_id,phase,generation,target_signature FROM embedding_index_states LIMIT 0",
			} {
				rows, err := db.Query(query)
				if err != nil {
					t.Fatal(err)
				}
				rows.Close()
			}
			execMigrationFileForDriver(t, db, current.Aggregate.DownPath, driver)
			for _, table := range []string{"user_github_star_prompts", "embedding_index_states"} {
				if rows, err := db.Query("SELECT * FROM " + table); err == nil {
					rows.Close()
					t.Fatalf("aggregate down retained %s", table)
				}
			}
			var legacy string
			if err := db.QueryRow("SELECT value FROM star_legacy_fixture WHERE id='existing-user'").Scan(&legacy); err != nil || legacy != "keep-existing-preferences" {
				t.Fatalf("legacy data changed: %q %v", legacy, err)
			}
		})
	}
}

func TestGitHubStarPromptMigrationPathsAndRollback(t *testing.T) {
	for _, driver := range []string{"sqlite", "postgres"} {
		t.Run(driver, func(t *testing.T) {
			runner := &Runner{dir: "../migrations"}
			catalog, err := runner.loadCatalog()
			if err != nil {
				t.Fatal(err)
			}
			current := catalog.Modes[len(catalog.Modes)-1]
			if current.Name != "v0_3" || current.Aggregate == nil {
				t.Fatal("expected the maintained v0_3 aggregate")
			}
			open := func(label string) *sql.DB {
				if driver == "postgres" {
					dsn := githubStarMigrationPostgresDSN(t)
					return createTemporaryPostgresDatabase(t, dsn, "star_"+label)
				}
				return openRawSQLite(t, t.TempDir()+"/"+label+".db")
			}
			devDB, releaseDB := open("dev"), open("release")
			for _, db := range []*sql.DB{devDB, releaseDB} {
				for _, mode := range catalog.Modes[:len(catalog.Modes)-1] {
					execMigrationFileForDriver(t, db, mode.Aggregate.UpPath, driver)
				}
				if _, err := db.Exec("CREATE TABLE star_legacy_fixture (id TEXT PRIMARY KEY, value TEXT NOT NULL); INSERT INTO star_legacy_fixture VALUES ('existing-user','keep-existing-preferences')"); err != nil {
					t.Fatal(err)
				}
			}
			execMigrationFileForDriver(t, releaseDB, current.Aggregate.UpPath, driver)
			t.Run("aggregate_storage", func(t *testing.T) {
				rows, err := releaseDB.Query("SELECT user_id,prompted_task_ids,last_prompt_at,supported,created_at,updated_at FROM user_github_star_prompts LIMIT 0")
				if err != nil {
					t.Fatalf("maintained aggregate does not yet provide Star storage: %v", err)
				}
				rows.Close()
				rows, err = releaseDB.Query("SELECT scope,active_model_id,phase,generation,target_signature FROM embedding_index_states LIMIT 0")
				if err != nil {
					t.Fatalf("maintained aggregate lost embedding index storage: %v", err)
				}
				rows.Close()
			})
			var starUp, starDown string
			for _, migration := range current.Dev {
				execMigrationFileForDriver(t, devDB, migration.UpPath, driver)
				if migration.FileVersion > current.Aggregate.Version {
					execMigrationFileForDriver(t, releaseDB, migration.UpPath, driver)
				}
				if strings.HasSuffix(migration.UpPath, "_github_star_prompts.up.sql") {
					starUp, starDown = migration.UpPath, migration.DownPath
				}
			}
			for _, db := range []*sql.DB{devDB, releaseDB} {
				// Execute the real construction paths first. The baseline red result
				// must be an absent feature table, not an invented migration fixture.
				if _, err := db.Exec(`INSERT INTO user_github_star_prompts (user_id,created_at,updated_at)
VALUES ('owner',CURRENT_TIMESTAMP,CURRENT_TIMESTAMP)`); err != nil {
					t.Fatalf("real %s construction path lacks usable Star prompt storage: %v", driver, err)
				}
				var supported bool
				var prompted string
				var last sql.NullTime
				if err := db.QueryRow("SELECT supported,prompted_task_ids,last_prompt_at FROM user_github_star_prompts WHERE user_id='owner'").Scan(&supported, &prompted, &last); err != nil {
					t.Fatal(err)
				}
				if supported || prompted != "[]" || last.Valid {
					t.Fatalf("unsafe defaults supported=%v prompted=%q last=%v", supported, prompted, last)
				}
				if _, err := db.Exec("INSERT INTO user_github_star_prompts (user_id,created_at,updated_at) VALUES ('owner',CURRENT_TIMESTAMP,CURRENT_TIMESTAMP)"); err == nil {
					t.Fatal("one user's state permits duplicate rows")
				}
				if _, err := db.Exec(`UPDATE user_github_star_prompts SET prompted_task_ids='["root-3"]',supported=TRUE,last_prompt_at=CURRENT_TIMESTAMP WHERE user_id='owner'`); err != nil {
					t.Fatal(err)
				}
				if err := db.QueryRow("SELECT supported,prompted_task_ids FROM user_github_star_prompts WHERE user_id='owner'").Scan(&supported, &prompted); err != nil || !supported || prompted != `["root-3"]` {
					t.Fatalf("state did not persist: %v %q %v", supported, prompted, err)
				}
			}
			if starUp == "" || starDown == "" {
				t.Fatal("Star persistence requires its own new dev up/down pair")
			}
			if driver == "sqlite" {
				if sqliteSchemaFingerprint(t, devDB) != sqliteSchemaFingerprint(t, releaseDB) {
					t.Fatal("SQLite aggregate and dev paths differ")
				}
			} else if postgresSchemaFingerprint(t, devDB) != postgresSchemaFingerprint(t, releaseDB) {
				t.Fatal("PostgreSQL aggregate and dev paths differ")
			}
			execMigrationFileForDriver(t, devDB, starDown, driver)
			if rows, err := devDB.Query("SELECT user_id FROM user_github_star_prompts"); err == nil {
				rows.Close()
				t.Fatal("dev down retained the feature table")
			}
			execMigrationFileForDriver(t, devDB, starUp, driver)
			if _, err := devDB.Exec("SELECT user_id FROM user_github_star_prompts"); err != nil {
				t.Fatalf("dev re-up failed: %v", err)
			}
			execMigrationFileForDriver(t, releaseDB, current.Aggregate.DownPath, driver)
			for _, table := range []string{"user_github_star_prompts", "embedding_index_states"} {
				if rows, err := releaseDB.Query("SELECT * FROM " + table); err == nil {
					rows.Close()
					t.Fatalf("aggregate down retained %s", table)
				}
			}
			for _, db := range []*sql.DB{devDB, releaseDB} {
				var value string
				if err := db.QueryRow("SELECT value FROM star_legacy_fixture WHERE id='existing-user'").Scan(&value); err != nil || value != "keep-existing-preferences" {
					t.Fatalf("legacy data lost during migration/rollback: %q %v", value, err)
				}
			}
		})
	}
}

// Keep the new migration independently executable even when a pre-existing
// full-history migration fails first. The full-path test above stays required.
func TestGitHubStarPromptIncrementalRoundTrip(t *testing.T) {
	for _, driver := range []string{"sqlite", "postgres"} {
		t.Run(driver, func(t *testing.T) {
			var db *sql.DB
			if driver == "postgres" {
				dsn := githubStarMigrationPostgresDSN(t)
				db = createTemporaryPostgresDatabase(t, dsn, "star_incremental")
			} else {
				db = openRawSQLite(t, t.TempDir()+"/star-incremental.db")
			}
			catalog, err := (&Runner{dir: "../migrations"}).loadCatalog()
			if err != nil {
				t.Fatal(err)
			}
			var feature *migrationFile
			for _, migration := range catalog.Modes[len(catalog.Modes)-1].Dev {
				if strings.HasSuffix(migration.UpPath, "_github_star_prompts.up.sql") {
					if feature != nil {
						t.Fatal("duplicate Star migration pairs")
					}
					copy := migration
					feature = &copy
				}
			}
			if feature == nil {
				t.Fatal("Star feature has no incremental migration yet")
			}
			if _, err := db.Exec("CREATE TABLE star_legacy_fixture (id TEXT PRIMARY KEY, value TEXT NOT NULL); INSERT INTO star_legacy_fixture VALUES ('existing-user','keep-existing-preferences')"); err != nil {
				t.Fatal(err)
			}
			for round := 0; round < 2; round++ {
				execMigrationFileForDriver(t, db, feature.UpPath, driver)
				if _, err := db.Exec("INSERT INTO user_github_star_prompts (user_id,created_at,updated_at) VALUES ('owner',CURRENT_TIMESTAMP,CURRENT_TIMESTAMP)"); err != nil {
					t.Fatal(err)
				}
				var supported bool
				var prompted string
				var last sql.NullTime
				if err := db.QueryRow("SELECT supported,prompted_task_ids,last_prompt_at FROM user_github_star_prompts WHERE user_id='owner'").Scan(&supported, &prompted, &last); err != nil || supported || prompted != "[]" || last.Valid {
					t.Fatalf("defaults=%v/%q/%v err=%v", supported, prompted, last, err)
				}
				if _, err := db.Exec("INSERT INTO user_github_star_prompts (user_id,created_at,updated_at) VALUES ('owner',CURRENT_TIMESTAMP,CURRENT_TIMESTAMP)"); err == nil {
					t.Fatal("missing user uniqueness")
				}
				execMigrationFileForDriver(t, db, feature.DownPath, driver)
				if rows, err := db.Query("SELECT user_id FROM user_github_star_prompts"); err == nil {
					rows.Close()
					t.Fatal("incremental down retained the Star table")
				}
				var legacy string
				if err := db.QueryRow("SELECT value FROM star_legacy_fixture WHERE id='existing-user'").Scan(&legacy); err != nil || legacy != "keep-existing-preferences" {
					t.Fatalf("legacy data changed: %q %v", legacy, err)
				}
			}
		})
	}
}
