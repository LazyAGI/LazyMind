package migrate

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCloudModelSelectionMigrationSupportsBothDatabasesAndAggregate(t *testing.T) {
	devDir := filepath.Join("..", "migrations", "dev_mode", "v0_3")
	entries, err := os.ReadDir(devDir)
	if err != nil {
		t.Fatal(err)
	}
	var upPath, downPath string
	for _, entry := range entries {
		name := entry.Name()
		if strings.HasSuffix(name, "_add_user_selected_cloud_models.up.sql") {
			upPath = filepath.Join(devDir, name)
		}
		if strings.HasSuffix(name, "_add_user_selected_cloud_models.down.sql") {
			downPath = filepath.Join(devDir, name)
		}
	}
	if upPath == "" || downPath == "" {
		t.Fatalf("Cloud model selection migration pair missing: up=%q down=%q", upPath, downPath)
	}
	up := readCloudSelectionSQL(t, upPath)
	down := readCloudSelectionSQL(t, downPath)
	for _, marker := range []string{
		"dialect postgres", "dialect sqlite", "user_selected_cloud_models",
		"user_id", "model_type", "public_model_key", "display_name_snapshot",
		"catalog_revision_snapshot", "unique", "chat_model_source", "varchar(128)",
	} {
		if !strings.Contains(up, marker) {
			t.Errorf("Cloud selection up migration omitted %q", marker)
		}
	}
	for _, marker := range []string{"dialect postgres", "dialect sqlite", "drop table", "user_selected_cloud_models", "chat_model_source"} {
		if !strings.Contains(down, marker) {
			t.Errorf("Cloud selection down migration omitted %q", marker)
		}
	}

	aggregateDir := filepath.Join("..", "migrations", "version_mode", "v0_3")
	aggregateUp := readCloudSelectionSQL(t, filepath.Join(aggregateDir, "20260805000000_workflow_runtime_release.up.sql"))
	aggregateDown := readCloudSelectionSQL(t, filepath.Join(aggregateDir, "20260805000000_workflow_runtime_release.down.sql"))
	for _, marker := range []string{"user_selected_cloud_models", "chat_model_source", "varchar(128)"} {
		if !strings.Contains(aggregateUp, marker) {
			t.Errorf("v0_3 aggregate up omitted %q", marker)
		}
	}
	if !strings.Contains(aggregateDown, "user_selected_cloud_models") {
		t.Error("v0_3 aggregate down omitted Cloud selection rollback")
	}
}

func readCloudSelectionSQL(t *testing.T, path string) string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return strings.ToLower(string(raw))
}
