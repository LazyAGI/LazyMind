package migrate

import (
	"os"
	"path/filepath"
	"testing"
)

var workspaceMigrationNames = []string{
	"20260901061506_create_local_workspaces",
	"20260902120000_allow_local_workspace_writes",
	"20260903023152_add_workspace_permission_mode",
}

func TestLocalWorkspaceMigrationPairsExist(t *testing.T) {
	for _, name := range workspaceMigrationNames {
		for _, direction := range []string{"up", "down"} {
			t.Run(name+"/"+direction, func(t *testing.T) {
				p := filepath.Join("..", "migrations", "dev_mode", "v0_3", name+"."+direction+".sql")
				if _, err := os.ReadFile(p); err != nil {
					t.Fatalf("required shared migration missing: %v", err)
				}
			})
		}
	}
}

func TestLocalWorkspaceSQLiteUpgradePreservesBindingAndDown(t *testing.T) {
	db := openRawSQLite(t, filepath.Join(t.TempDir(), "workspace.db"))
	if _, err := db.Exec(`PRAGMA foreign_keys=ON; CREATE TABLE conversations(id TEXT PRIMARY KEY); INSERT INTO conversations VALUES ('task');`); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join("..", "migrations", "dev_mode", "v0_3")
	execMigrationFileForDriver(t, db, filepath.Join(dir, workspaceMigrationNames[0]+".up.sql"), "sqlite")
	if _, err := db.Exec(`INSERT INTO local_workspaces(id,create_user_id,display_name,canonical_path,directory_identity,status,source,authorized_at,last_used_at,created_at,updated_at)
 VALUES ('grant','owner','project','/fixture','identity','active','local',CURRENT_TIMESTAMP,CURRENT_TIMESTAMP,CURRENT_TIMESTAMP,CURRENT_TIMESTAMP);
 INSERT INTO conversation_workspace_bindings(conversation_id,workspace_id,created_at) VALUES ('task','grant',CURRENT_TIMESTAMP);`); err != nil {
		t.Fatal(err)
	}
	for _, name := range workspaceMigrationNames[1:] {
		execMigrationFileForDriver(t, db, filepath.Join(dir, name+".up.sql"), "sqlite")
	}
	var mode, workspace string
	var version int64
	if err := db.QueryRow(`SELECT workspace_id,permission_mode,permission_version FROM conversation_workspace_bindings WHERE conversation_id='task'`).Scan(&workspace, &mode, &version); err != nil {
		t.Fatal(err)
	}
	if workspace != "grant" || mode != "ask_as_needed" || version != 1 {
		t.Fatalf("upgrade lost binding/defaults: %s %s %d", workspace, mode, version)
	}
	if _, err := db.Exec(`UPDATE conversation_workspace_bindings SET permission_mode='invalid'`); err == nil {
		t.Fatal("invalid permission accepted")
	}
	if _, err := db.Exec(`INSERT INTO conversation_workspace_bindings SELECT * FROM conversation_workspace_bindings`); err == nil {
		t.Fatal("duplicate task binding accepted")
	}
	for i := len(workspaceMigrationNames) - 1; i >= 0; i-- {
		execMigrationFileForDriver(t, db, filepath.Join(dir, workspaceMigrationNames[i]+".down.sql"), "sqlite")
	}
	assertTableExists(t, db, "local_workspaces", false)
	assertTableExists(t, db, "conversation_workspace_bindings", false)
	var count int
	if err := db.QueryRow(`SELECT COUNT(*) FROM conversations WHERE id='task'`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("down lost conversation: count=%d err=%v", count, err)
	}
}
