package chat

import (
	"strings"
	"testing"
)

func contractColumns(t *testing.T, table string) map[string]bool {
	t.Helper()
	database := newPromptTestDB(t)
	columnTypes, err := database.DB.Migrator().ColumnTypes(table)
	if err != nil {
		t.Fatalf("read %s schema: %v", table, err)
	}

	columns := map[string]bool{}
	for _, columnType := range columnTypes {
		columns[columnType.Name()] = true
	}
	return columns
}

func requireContractColumns(t *testing.T, table string, want ...string) {
	t.Helper()
	columns := contractColumns(t, table)
	missing := make([]string, 0)
	for _, name := range want {
		if !columns[name] {
			missing = append(missing, name)
		}
	}
	if len(missing) > 0 {
		t.Fatalf("%s missing contract columns: %s", table, strings.Join(missing, ", "))
	}
}

func TestLocalWorkspaceSchemaPersistsGrantWithoutFileContent(t *testing.T) {
	requireContractColumns(t, "local_workspaces",
		"id",
		"create_user_id",
		"display_name",
		"canonical_path",
		"directory_identity",
		"status",
		"version",
		"source",
		"read_policy",
		"write_policy",
		"authorized_at",
		"last_used_at",
		"revoked_at",
		"created_at",
		"updated_at",
	)

	columns := contractColumns(t, "local_workspaces")
	for _, forbidden := range []string{"file_content", "file_list", "content_hash", "secret"} {
		if columns[forbidden] {
			t.Fatalf("local_workspaces must not persist %s", forbidden)
		}
	}
}

func TestConversationWorkspaceBindingSchemaKeepsOneWorkspacePerTask(t *testing.T) {
	requireContractColumns(t, "conversation_workspace_bindings",
		"conversation_id",
		"workspace_id",
		"permission_mode",
		"permission_version",
		"updated_at",
		"created_at",
	)

	database := newPromptTestDB(t)
	indexes, err := database.DB.Migrator().GetIndexes("conversation_workspace_bindings")
	if err != nil {
		t.Fatalf("read binding indexes: %v", err)
	}
	unique := false
	columnTypes, err := database.DB.Migrator().ColumnTypes("conversation_workspace_bindings")
	if err != nil {
		t.Fatalf("read binding columns: %v", err)
	}
	// PostgreSQL GetIndexes excludes indexes backing PRIMARY KEY constraints.
	primaryColumns := []string{}
	for _, column := range columnTypes {
		if primary, ok := column.PrimaryKey(); ok && primary {
			primaryColumns = append(primaryColumns, column.Name())
		}
	}
	unique = len(primaryColumns) == 1 && primaryColumns[0] == "conversation_id"
	for _, index := range indexes {
		isUnique, ok := index.Unique()
		columns := index.Columns()
		if ok && isUnique && len(columns) == 1 && columns[0] == "conversation_id" {
			unique = true
		}
	}
	if !unique {
		t.Fatal("conversation_workspace_bindings needs a unique conversation binding constraint")
	}

	var cascadeCount int64
	switch database.DB.Dialector.Name() {
	case "postgres":
		if err := database.DB.Raw(`
SELECT COUNT(*)
FROM information_schema.referential_constraints rc
JOIN information_schema.table_constraints tc
  ON tc.constraint_catalog = rc.constraint_catalog
 AND tc.constraint_schema = rc.constraint_schema
 AND tc.constraint_name = rc.constraint_name
WHERE tc.table_schema = current_schema()
  AND tc.table_name = 'conversation_workspace_bindings'
  AND rc.delete_rule = 'CASCADE'`).Scan(&cascadeCount).Error; err != nil {
			t.Fatalf("read PostgreSQL binding foreign keys: %v", err)
		}
	default:
		foreignKeys, err := database.DB.Raw("PRAGMA foreign_key_list(conversation_workspace_bindings)").Rows()
		if err != nil {
			t.Fatalf("read SQLite binding foreign keys: %v", err)
		}
		defer foreignKeys.Close()
		for foreignKeys.Next() {
			var id, seq int
			var table, from, to, onUpdate, onDelete, match string
			if err := foreignKeys.Scan(&id, &seq, &table, &from, &to, &onUpdate, &onDelete, &match); err != nil {
				t.Fatalf("scan binding foreign key: %v", err)
			}
			if table == "conversations" && from == "conversation_id" && onDelete == "CASCADE" {
				cascadeCount++
			}
		}
	}
	if cascadeCount == 0 {
		t.Fatal("conversation deletion must cascade to its workspace binding")
	}
}
