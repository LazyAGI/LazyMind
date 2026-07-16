package plugin

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestPluginGenerationAnalysisMigrationContract(t *testing.T) {
	_, file, _, _ := runtime.Caller(0)
	root := filepath.Join(filepath.Dir(file), "..", "migrations")
	up, err := os.ReadFile(filepath.Join(root, "20260710180000_plugin_generation_analysis.up.sql"))
	if err != nil {
		t.Fatal(err)
	}
	down, err := os.ReadFile(filepath.Join(root, "20260710180000_plugin_generation_analysis.down.sql"))
	if err != nil {
		t.Fatal(err)
	}
	for _, token := range []string{"source_skill_revision_id", "source_skill_tree_hash", "plugin_generation_analyses", "plugin_repair_runs", "source_package_json", "VARCHAR(32)"} {
		if !strings.Contains(string(up), token) {
			t.Fatalf("up migration missing %s", token)
		}
	}
	for _, token := range []string{"DROP TABLE IF EXISTS plugin_repair_runs", "DROP TABLE IF EXISTS plugin_generation_analyses", "DROP COLUMN IF EXISTS source_skill_revision_id", "VARCHAR(16)"} {
		if !strings.Contains(string(down), token) {
			t.Fatalf("down migration missing %s", token)
		}
	}
}
