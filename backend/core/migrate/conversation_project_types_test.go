package migrate

import (
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestConversationProjectTypeMigration(t *testing.T) {
	for _, driver := range []string{"sqlite", "postgres"} {
		t.Run(driver, func(t *testing.T) {
			var db *sql.DB
			if driver == "sqlite" {
				db = openRawSQLite(t, t.TempDir()+"/projects.db")
			} else {
				dsn := os.Getenv(migrationPostgresDSNEnv)
				if dsn == "" {
					t.Skip("PostgreSQL DSN not configured")
				}
				db = createTemporaryPostgresDatabase(t, dsn, "project_types")
			}
			exec := func(q string) {
				t.Helper()
				if _, err := db.Exec(q); err != nil {
					t.Fatal(err)
				}
			}
			exec(`CREATE TABLE conversations(id VARCHAR(36) PRIMARY KEY,is_task_conv BOOLEAN NOT NULL,parent_conversation_id VARCHAR(36),archived_at TIMESTAMP,deleted_at TIMESTAMP);
CREATE TABLE conversation_groups(id VARCHAR(36) PRIMARY KEY,user_id VARCHAR(255) NOT NULL,name VARCHAR(255) NOT NULL,normalized_name VARCHAR(255) NOT NULL,scope TEXT NOT NULL DEFAULT '',version BIGINT NOT NULL DEFAULT 1,created_by VARCHAR(16) NOT NULL DEFAULT 'user',created_run_id VARCHAR(64) NOT NULL DEFAULT '',created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,deleted_at TIMESTAMP,pinned BOOLEAN NOT NULL DEFAULT FALSE,sort_order BIGINT NOT NULL DEFAULT 0,kind VARCHAR(16) NOT NULL DEFAULT 'project',workspace_id VARCHAR(64),project_path TEXT,is_task_conv BOOLEAN NOT NULL DEFAULT FALSE);
CREATE UNIQUE INDEX uk_conversation_projects_user_path ON conversation_groups(user_id,project_path) WHERE kind='project' AND deleted_at IS NULL;
CREATE TABLE conversation_group_members(conversation_id VARCHAR(36) PRIMARY KEY,group_id VARCHAR(36) NOT NULL,user_id VARCHAR(255) NOT NULL,revision BIGINT NOT NULL,source VARCHAR(16) NOT NULL,source_run_id VARCHAR(64) NOT NULL,created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP);
CREATE TABLE conversation_group_states(conversation_id VARCHAR(36) PRIMARY KEY,user_id VARCHAR(255) NOT NULL,group_id VARCHAR(36),revision BIGINT NOT NULL,source_run_id VARCHAR(64) NOT NULL,updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP);
INSERT INTO conversation_groups(id,user_id,name,normalized_name,workspace_id,project_path) VALUES ('mixed','u','Mixed','mixed','grant','/mixed'),('task','u','Task','task','grant2','/task'),('empty','u','Empty','empty','grant3','/empty'),('deleted','u','Deleted','deleted','old-grant','/deleted');
UPDATE conversation_groups SET deleted_at=CURRENT_TIMESTAMP,project_path='/mixed' WHERE id='deleted';
INSERT INTO conversations(id,is_task_conv) VALUES ('normal',FALSE),('archived-task',TRUE),('deleted-task',TRUE),('task-only',TRUE),('sidechat',FALSE),('dn',FALSE),('dt',TRUE);
UPDATE conversations SET archived_at=CURRENT_TIMESTAMP WHERE id='archived-task';
UPDATE conversations SET parent_conversation_id='task-only' WHERE id='sidechat';
UPDATE conversations SET deleted_at=CURRENT_TIMESTAMP WHERE id IN ('deleted-task','dn','dt');
INSERT INTO conversation_group_members(conversation_id,group_id,user_id,revision,source,source_run_id) VALUES ('normal','mixed','u',2,'user',''),('archived-task','mixed','u',2,'user',''),('deleted-task','mixed','u',2,'user',''),('task-only','task','u',2,'user',''),('sidechat','task','u',2,'user',''),('dn','deleted','u',2,'user',''),('dt','deleted','u',2,'user','');
INSERT INTO conversation_group_states(conversation_id,user_id,group_id,revision,source_run_id) VALUES ('archived-task','u','mixed',7,'');`)
			paths, err := filepath.Glob("../migrations/dev_mode/v0_3/*_isolate_conversation_project_types.up.sql")
			if err != nil || len(paths) != 1 {
				t.Fatalf("migration: %v %v", paths, err)
			}
			execMigrationFileForDriver(t, db, paths[0], driver)
			count := func(q string, want int) {
				t.Helper()
				var got int
				if err := db.QueryRow(q).Scan(&got); err != nil {
					t.Fatal(err)
				}
				if got != want {
					t.Fatalf("%s: got %d want %d", q, got, want)
				}
			}
			count("SELECT COUNT(*) FROM conversation_groups", 6)
			count("SELECT COUNT(*) FROM conversation_group_members", 7)
			count("SELECT COUNT(*) FROM conversation_group_members m JOIN conversation_groups g ON g.id=m.group_id JOIN conversations c ON c.id=m.conversation_id WHERE c.is_task_conv<>g.is_task_conv", 0)
			count("SELECT COUNT(*) FROM conversation_groups WHERE project_path='/mixed' AND workspace_id='grant' AND deleted_at IS NULL", 2)
			count("SELECT COUNT(*) FROM conversation_groups WHERE project_path='/mixed' AND workspace_id='old-grant' AND deleted_at IS NOT NULL", 2)
			count("SELECT COUNT(*) FROM conversation_groups WHERE id='task' AND is_task_conv=TRUE", 1)
			count("SELECT COUNT(*) FROM conversation_groups WHERE id='empty' AND is_task_conv=FALSE", 1)
			count("SELECT COUNT(*) FROM conversation_group_states s JOIN conversation_group_members m ON m.conversation_id=s.conversation_id WHERE s.conversation_id='archived-task' AND s.group_id=m.group_id AND s.group_id<>'mixed' AND s.revision=8 AND m.revision=3", 1)
			if _, err := db.Exec("INSERT INTO conversation_groups(id,user_id,name,normalized_name,project_path,is_task_conv) VALUES('dup','u','Dup','dup','/mixed',TRUE)"); err == nil {
				t.Fatal("duplicate task directory accepted")
			}
			down := strings.TrimSuffix(paths[0], "up.sql") + "down.sql"
			body, err := os.ReadFile(down)
			if err != nil {
				t.Fatal(err)
			}
			tx, err := db.Begin()
			if err != nil {
				t.Fatal(err)
			}
			if _, err := tx.Exec(string(body)); err == nil {
				tx.Rollback()
				t.Fatal("unsafe downgrade accepted")
			}
			if err := tx.Rollback(); err != nil {
				t.Fatal(err)
			}
			count("SELECT COUNT(*) FROM conversation_groups", 6)
			// Removing the active-path conflict permits a non-destructive downgrade.
			exec("UPDATE conversation_groups SET deleted_at=CURRENT_TIMESTAMP WHERE project_path='/mixed' AND is_task_conv=TRUE")
			execMigrationFileForDriver(t, db, down, driver)
			count("SELECT COUNT(*) FROM conversation_group_members", 7)
			execMigrationFileForDriver(t, db, paths[0], driver)
			count("SELECT COUNT(*) FROM conversation_groups", 6)
			count("SELECT COUNT(*) FROM conversation_group_members m JOIN conversation_groups g ON g.id=m.group_id JOIN conversations c ON c.id=m.conversation_id WHERE c.is_task_conv<>g.is_task_conv", 0)
		})
	}
}
