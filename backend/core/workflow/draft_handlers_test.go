package workflow

import (
	"strings"
	"testing"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	"lazymind/core/common/orm"
)

func TestSetWorkflowYAMLUpdateUsesPhysicalPluginIDColumn(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{DryRun: true})
	if err != nil {
		t.Fatal(err)
	}

	updates := map[string]any{}
	setWorkflowYAMLUpdate(updates, "id: deep-research\n")
	if updates["plugin_id"] != "deep-research" || updates["plugin_yaml_content"] != "id: deep-research\n" || len(updates) != 2 {
		t.Fatalf("unexpected workflow id update: %#v", updates)
	}

	stmt := db.Model(&orm.WorkflowDraft{}).
		Where("id = ?", "draft-1").
		Updates(updates).Statement
	sql := stmt.SQL.String()
	if !strings.Contains(sql, "`plugin_id`") {
		t.Fatalf("workflow id update must target physical plugin_id column: %s", sql)
	}
	if strings.Contains(sql, "`workflow_id`") {
		t.Fatalf("workflow_id is not a physical plugin_drafts column: %s", sql)
	}
	if !strings.Contains(sql, "`plugin_yaml_content`") || strings.Contains(sql, "`workflow_yaml_content`") {
		t.Fatalf("workflow YAML update must target physical plugin_yaml_content column: %s", sql)
	}
}

func TestReplaceWorkflowYAMLIdentityKeepsDefinitionAndChangesIdentity(t *testing.T) {
	source := "id: image-workflow\nname: AI Image\ndescription: keep me\nsteps:\n  - id: generate\n"
	got := replaceWorkflowYAMLIdentity(source, "image-workflow-copy", "AI 图片生成 副本")
	if extractWorkflowID(got) != "image-workflow-copy" {
		t.Fatalf("copied workflow id = %q", extractWorkflowID(got))
	}
	for _, expected := range []string{`name: "AI 图片生成 副本"`, "description: keep me", "  - id: generate"} {
		if !strings.Contains(got, expected) {
			t.Fatalf("copied workflow lost %q:\n%s", expected, got)
		}
	}
}
