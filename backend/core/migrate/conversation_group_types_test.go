package migrate

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"
)

func TestConversationGroupTypeMigration(t *testing.T) {
	for _, driver := range []string{"sqlite", "postgres"} {
		t.Run(driver, func(t *testing.T) {
			var db *sql.DB
			if driver == "sqlite" {
				db = openRawSQLite(t, t.TempDir()+"/types.db")
			} else {
				dsn := os.Getenv(migrationPostgresDSNEnv)
				if dsn == "" {
					t.Skip("PostgreSQL DSN not configured")
				}
				db = createTemporaryPostgresDatabase(t, dsn, "group_types")
			}
			exec := func(body string) {
				t.Helper()
				if _, err := db.Exec(body); err != nil {
					t.Fatal(err)
				}
			}
			// Seed the pre-isolation schema, including archived and soft-deleted task members.
			exec(`CREATE TABLE conversations(id VARCHAR(36) PRIMARY KEY,is_task_conv BOOLEAN NOT NULL,archived_at TIMESTAMP,deleted_at TIMESTAMP);
CREATE TABLE conversation_groups(id VARCHAR(36) PRIMARY KEY,user_id VARCHAR(255) NOT NULL,name VARCHAR(255) NOT NULL,normalized_name VARCHAR(255) NOT NULL,scope TEXT NOT NULL DEFAULT '',version BIGINT NOT NULL DEFAULT 1,created_by VARCHAR(16) NOT NULL DEFAULT 'user',created_run_id VARCHAR(64) NOT NULL DEFAULT '',created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,deleted_at TIMESTAMP,pinned BOOLEAN NOT NULL DEFAULT FALSE,sort_order BIGINT NOT NULL DEFAULT 0,kind VARCHAR(16) NOT NULL DEFAULT 'group',workspace_id VARCHAR(64),project_path TEXT);
CREATE UNIQUE INDEX uk_conversation_groups_user_name ON conversation_groups(user_id,normalized_name) WHERE kind='group';
CREATE TABLE conversation_group_members(conversation_id VARCHAR(36) PRIMARY KEY,group_id VARCHAR(36) NOT NULL,user_id VARCHAR(255) NOT NULL,revision BIGINT NOT NULL,source VARCHAR(16) NOT NULL,source_run_id VARCHAR(64) NOT NULL,created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP);
CREATE TABLE conversation_group_states(conversation_id VARCHAR(36) PRIMARY KEY,user_id VARCHAR(255) NOT NULL,group_id VARCHAR(36),revision BIGINT NOT NULL,source_run_id VARCHAR(64) NOT NULL,updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP);
INSERT INTO conversation_groups(id,user_id,name,normalized_name) VALUES ('empty','u','Empty','empty'),('normal','u','Normal','normal'),('task','u','Task','task'),('mixed','u','Mixed','mixed');
INSERT INTO conversation_groups(id,user_id,name,normalized_name,kind) VALUES ('project','u','Project','project','project');
INSERT INTO conversations(id,is_task_conv) VALUES ('n',FALSE),('t',TRUE),('mn',FALSE),('mt',TRUE),('pn',FALSE),('pt',TRUE);
UPDATE conversations SET archived_at=CURRENT_TIMESTAMP,deleted_at=CURRENT_TIMESTAMP WHERE id='mt';
INSERT INTO conversation_group_members(conversation_id,group_id,user_id,revision,source,source_run_id) VALUES ('n','normal','u',3,'organizer','run'),('t','task','u',3,'organizer','run'),('mn','mixed','u',3,'organizer','run'),('mt','mixed','u',3,'organizer','run'),('pn','project','u',3,'user',''),('pt','project','u',3,'user','');
INSERT INTO conversation_group_states(conversation_id,user_id,group_id,revision,source_run_id) VALUES ('mt','u','mixed',9,'run');`)
			paths, err := filepath.Glob("../migrations/dev_mode/v0_3/*_isolate_conversation_group_types.up.sql")
			if err != nil || len(paths) != 1 {
				t.Fatalf("migration: %v %v", paths, err)
			}
			execMigrationFileForDriver(t, db, paths[0], driver)
			scalar := func(query string, want int) {
				t.Helper()
				var got int
				if err := db.QueryRow(query).Scan(&got); err != nil {
					t.Fatal(err)
				}
				if got != want {
					t.Fatalf("%s: got %d want %d", query, got, want)
				}
			}
			scalar("SELECT COUNT(*) FROM conversation_groups", 6)
			scalar("SELECT COUNT(*) FROM conversation_group_members", 6)
			scalar("SELECT COUNT(*) FROM conversation_group_members m JOIN conversation_groups g ON g.id=m.group_id JOIN conversations c ON c.id=m.conversation_id WHERE g.kind='group' AND g.is_task_conv<>c.is_task_conv", 0)
			scalar("SELECT COUNT(*) FROM conversation_groups WHERE id='empty' AND is_task_conv=FALSE", 1)
			scalar("SELECT COUNT(*) FROM conversation_groups WHERE id='task' AND is_task_conv=TRUE AND version=2", 1)
			scalar("SELECT COUNT(*) FROM conversation_group_states s JOIN conversation_group_members m ON m.conversation_id=s.conversation_id WHERE s.conversation_id='mt' AND s.group_id=m.group_id AND s.group_id<>'mixed' AND s.revision=10 AND m.revision=4 AND s.source_run_id='' AND m.source_run_id=''", 1)
			scalar("SELECT COUNT(*) FROM conversation_group_members WHERE group_id='project' AND revision=3", 2)
			// Restoring visibility does not change membership type.
			exec("UPDATE conversations SET archived_at=NULL,deleted_at=NULL WHERE id='mt'")
			execMigrationFileForDriver(t, db, paths[0][:len(paths[0])-len("up.sql")]+"down.sql", driver)
			scalar("SELECT COUNT(*) FROM conversation_groups", 6)
			scalar("SELECT COUNT(*) FROM conversation_group_members", 6)
			scalar("SELECT COUNT(*) FROM conversation_groups WHERE normalized_name='mixed'", 1)
			execMigrationFileForDriver(t, db, paths[0], driver)
			scalar("SELECT COUNT(*) FROM conversation_groups", 6)
		})
	}
}
