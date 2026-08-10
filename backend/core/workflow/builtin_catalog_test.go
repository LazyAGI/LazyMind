package workflow

import (
	"context"
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
