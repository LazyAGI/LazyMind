package migrate

import (
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestExternalAgentWorkflowMigrationPaths(t *testing.T) {
	catalog, err := (&Runner{dir: "../migrations"}).loadCatalog()
	if err != nil {
		t.Fatal(err)
	}
	current := catalog.Modes[len(catalog.Modes)-1]
	var target migrationFile
	for _, migration := range current.Dev {
		if migration.FileVersion == 20260914200000 {
			target = migration
		}
	}
	if target.UpPath == "" || current.Aggregate == nil {
		t.Fatal("external task migration and aggregate required")
	}
	for _, driver := range []string{"sqlite", "postgres"} {
		t.Run(driver, func(t *testing.T) {
			fingerprints := map[string]string{}
			for _, path := range []string{"dev", "aggregate"} {
				t.Run(path, func(t *testing.T) {
					var db *sql.DB
					if driver == "sqlite" {
						db = openRawSQLite(t, filepath.Join(t.TempDir(), "external.db"))
					} else {
						dsn := os.Getenv(migrationPostgresDSNEnv)
						if dsn == "" {
							t.Skip("PostgreSQL integration DSN required")
						}
						db = createTemporaryPostgresDatabase(t, dsn, "external_task")
					}
					for _, mode := range catalog.Modes[:len(catalog.Modes)-1] {
						execMigrationFileForDriver(t, db, mode.Aggregate.UpPath, driver)
					}
					if _, err := db.Exec(`INSERT INTO plugin_sessions(id,conversation_id,plugin_id,status,created_at,updated_at) VALUES ('kept','conv','writer','waiting',CURRENT_TIMESTAMP,CURRENT_TIMESTAMP)`); err != nil {
						t.Fatal(err)
					}
					down := target.DownPath
					if path == "aggregate" {
						execMigrationFileForDriver(t, db, current.Aggregate.UpPath, driver)
						down = current.Aggregate.DownPath
					} else {
						for _, migration := range current.Dev {
							execMigrationFileForDriver(t, db, migration.UpPath, driver)
						}
					}
					if driver == "sqlite" {
						rows, err := db.Query(`SELECT sql FROM sqlite_master WHERE tbl_name IN ('external_agent_workflow_tasks','external_agent_skill_sources') AND sql IS NOT NULL ORDER BY name`)
						if err != nil {
							t.Fatal(err)
						}
						var definitions []string
						for rows.Next() {
							var definition string
							if err := rows.Scan(&definition); err != nil {
								t.Fatal(err)
							}
							definitions = append(definitions, strings.Join(strings.Fields(definition), " "))
						}
						if err := rows.Err(); err != nil {
							t.Fatal(err)
						}
						rows.Close()
						fingerprints[path] = strings.Join(definitions, "\n")
					}
					statements := []string{
						`INSERT INTO external_agent_workflow_tasks(id,owner_user_id,idempotency_key,agent_type,skill_id,created_at,updated_at) VALUES ('task','owner','request','codex','skill',CURRENT_TIMESTAMP,CURRENT_TIMESTAMP)`,
						`INSERT INTO external_agent_skill_sources(id,owner_user_id,source_type,source_key,created_at,updated_at) VALUES ('source','owner','url','hash',CURRENT_TIMESTAMP,CURRENT_TIMESTAMP)`,
					}
					for _, statement := range statements {
						if _, err := db.Exec(statement); err != nil {
							t.Fatal(err)
						}
					}
					if _, err := db.Exec(`INSERT INTO external_agent_workflow_tasks(id,owner_user_id,idempotency_key,agent_type,skill_id,created_at,updated_at) VALUES ('duplicate','owner','request','codex','skill',CURRENT_TIMESTAMP,CURRENT_TIMESTAMP)`); err == nil {
						t.Fatal("missing idempotency uniqueness")
					}
					execMigrationFileForDriver(t, db, down, driver)
					for _, table := range []string{"external_agent_workflow_tasks", "external_agent_skill_sources"} {
						if rows, err := db.Query("SELECT id FROM " + table); err == nil {
							rows.Close()
							t.Fatalf("down left table %s", table)
						}
					}
					var plugin string
					if err := db.QueryRow(`SELECT plugin_id FROM plugin_sessions WHERE id='kept'`).Scan(&plugin); err != nil || plugin != "writer" {
						t.Fatalf("existing session lost: %q %v", plugin, err)
					}
				})
			}
			if driver == "sqlite" && fingerprints["dev"] != fingerprints["aggregate"] {
				t.Fatalf("external schemas differ:\ndev=%s\naggregate=%s", fingerprints["dev"], fingerprints["aggregate"])
			}
		})
	}
}
