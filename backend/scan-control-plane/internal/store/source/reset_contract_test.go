package source

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestInitMigrationDoesNotDropTables(t *testing.T) {
	t.Parallel()

	ddl := normalizeSQL(readInitDDL(t))
	if strings.Contains(ddl, "drop table") {
		t.Fatalf("normal init migration must not drop tables")
	}
}

func TestResetScriptDropsOnlyOwnedTablesAndPrintsList(t *testing.T) {
	t.Parallel()

	script := readResetScript(t)
	ownedTables := []string{
		"parse_task_dead_letters",
		"agent_commands",
		"agents",
		"data_source_create_operations",
		"source_sync_runs",
		"source_sync_checkpoints",
		"parse_tasks",
		"documents",
		"source_document_states",
		"source_object_index",
		"source_bindings",
		"sources",
	}
	for _, table := range ownedTables {
		assertContains(t, script, table)
		assertContains(t, script, "echo \"  public.${table}\"")
	}
	for _, forbidden := range []string{"auth", "core", "lazyllm", "cloud_source_bindings", "DROP SCHEMA"} {
		assertNotContains(t, script, forbidden)
	}
	assertContains(t, script, "LAZYMIND_SCAN_CONTROL_PLANE_RESET_CONFIRM")
	assertContains(t, script, "psql \"$DB_DSN\" -v ON_ERROR_STOP=1 -f \"$MIGRATION\"")
}

func readResetScript(t *testing.T) string {
	t.Helper()

	path := filepath.Join("..", "..", "..", "scripts", "reset_scan_control_plane_schema.sh")
	content, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read reset script: %v", err)
	}
	return string(content)
}
