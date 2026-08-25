package workflow

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"lazymind/core/common/orm"
)

func TestReconcileBuiltinWorkflowCatalogArchivesOnlyRemovedBuiltins(t *testing.T) {
	db := newHandlerTestDB(t)
	now := time.Now().UTC()
	resources := []orm.WorkflowResource{
		{
			ID: "current", WorkflowRef: "builtin:image-workflow", WorkflowID: "image-workflow",
			OwnerScope: "builtin", SourceType: "builtin", RelativeRoot: "workflows/builtin/image-workflow",
			Name: "AI Image Generation", Status: "active", CreatedAt: now, UpdatedAt: now,
		},
		{
			ID: "legacy", WorkflowRef: "builtin:image-plugin", WorkflowID: "image-plugin",
			OwnerScope: "builtin", SourceType: "builtin", RelativeRoot: "workflows/builtin/image-plugin",
			Name: "AI Image Generation", Status: "active", CreatedAt: now, UpdatedAt: now,
		},
		{
			ID: "personal", WorkflowRef: "user:one:workflow", WorkflowID: "workflow",
			OwnerUserID: "one", OwnerScope: "user", SourceType: "user", RelativeRoot: "workflows/u_one/workflow",
			Name: "Personal", Status: "active", CreatedAt: now, UpdatedAt: now,
		},
	}
	if err := db.DB.Create(&resources).Error; err != nil {
		t.Fatal(err)
	}

	if err := reconcileBuiltinWorkflowCatalog(
		context.Background(), db.DB, []string{"builtin:image-workflow"},
	); err != nil {
		t.Fatal(err)
	}

	var current, legacy, personal orm.WorkflowResource
	if err := db.DB.Where("id = ?", "current").First(&current).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.DB.Where("id = ?", "legacy").First(&legacy).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.DB.Where("id = ?", "personal").First(&personal).Error; err != nil {
		t.Fatal(err)
	}
	if current.Status != "active" || legacy.Status != "archived" || personal.Status != "active" {
		t.Fatalf("statuses: current=%s legacy=%s personal=%s", current.Status, legacy.Status, personal.Status)
	}
}

func TestDisabledBuiltinWorkflowIDsIgnoresArchivedCatalogEntries(t *testing.T) {
	db := newHandlerTestDB(t)
	now := time.Now().UTC()
	resource := orm.WorkflowResource{
		ID: "legacy", WorkflowRef: "builtin:writer-plugin", WorkflowID: "writer-plugin",
		OwnerScope: "builtin", SourceType: "builtin", RelativeRoot: "workflows/builtin/writer-plugin",
		Name: "AI Writer", Status: "archived", CreatedAt: now, UpdatedAt: now,
	}
	setting := orm.UserWorkflowSetting{
		UserID: "user-1", WorkflowRef: resource.WorkflowRef, Enabled: false, UpdatedAt: now,
	}
	if err := db.DB.Create(&resource).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.DB.Create(&setting).Error; err != nil {
		t.Fatal(err)
	}

	ids, err := DisabledBuiltinWorkflowIDs(db.DB, "user-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 0 {
		t.Fatalf("archived builtin leaked into disabled catalog: %v", ids)
	}
}

func TestBuiltinPackageIgnoresPythonRuntimeCacheFiles(t *testing.T) {
	for _, name := range []string{"tools.pyc", "tools.pyo", ".DS_Store"} {
		if !ignoredBuiltinPackageFile(name) {
			t.Fatalf("runtime cache file %q was not ignored", name)
		}
	}
	if ignoredBuiltinPackageFile("tools.py") {
		t.Fatal("source file tools.py must remain in the immutable package")
	}
}

func TestBuiltinSeedPublishesNewRevisionWhenCompilerGraphChanges(t *testing.T) {
	db := newHandlerTestDB(t)
	root := t.TempDir()
	workflowYAML := `id: compiler-refresh
name: Compiler Refresh
slots:
  - {id: topic, type: text, external: true}
  - {id: result, type: text}
steps:
  - {id: run, label: Run}
`
	stateYAML := `transitions:
  __start__: [{to: run}]
  run: [{to: __end__}]
steps:
  run:
    inputs: [{material: topic, required: true}]
    outputs: [result]
`
	if err := os.MkdirAll(filepath.Join(root, "scenario"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "workflow.yaml"), []byte(workflowYAML), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "scenario", "state.yml"), []byte(stateYAML), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, err := seedBuiltinWorkflow(context.Background(), db.DB, root); err != nil {
		t.Fatal(err)
	}
	var first orm.WorkflowRevision
	if err := db.DB.First(&first).Error; err != nil {
		t.Fatal(err)
	}
	if err := db.DB.Model(&first).Update("graph_hash", "legacy-compiler-graph").Error; err != nil {
		t.Fatal(err)
	}
	if _, err := seedBuiltinWorkflow(context.Background(), db.DB, root); err != nil {
		t.Fatal(err)
	}
	if _, err := seedBuiltinWorkflow(context.Background(), db.DB, root); err != nil {
		t.Fatal(err)
	}
	var count int64
	if err := db.DB.Model(&orm.WorkflowRevision{}).Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 2 {
		t.Fatalf("revision count=%d, want exactly one compiler refresh", count)
	}
	var resource orm.WorkflowResource
	if err := db.DB.Where("plugin_ref = ?", "builtin:compiler-refresh").First(&resource).Error; err != nil {
		t.Fatal(err)
	}
	var head orm.WorkflowRevision
	if err := db.DB.Where("id = ?", resource.HeadRevisionID).First(&head).Error; err != nil {
		t.Fatal(err)
	}
	if head.GraphHash == "" || head.GraphHash == "legacy-compiler-graph" || head.RevisionNo != 2 {
		t.Fatalf("head=%#v", head)
	}
}
