package workflow

import (
	"context"
	"testing"

	"lazymind/core/cloudbinding"
	"lazymind/core/cloudclient"
	"lazymind/core/cloudpackage"
	"lazymind/core/cloudresource"
	"lazymind/core/common/orm"
)

func TestCloudWorkflowAdapterImportsCompilesAndBindsAtomically(t *testing.T) {
	db := newTestDB(t)
	if err := db.AutoMigrate(&orm.WorkflowRevision{}, &orm.WorkflowRevisionEntry{}, &orm.WorkflowBlob{}, &orm.UserWorkflowSetting{}, &cloudbinding.Binding{}); err != nil {
		t.Fatal(err)
	}
	files := map[string]cloudpackage.File{}
	for path, body := range validCloudWorkflowFiles() {
		files[path] = cloudpackage.File{Data: body}
	}
	prepared, err := cloudpackage.Prepare(cloudpackage.PrepareInput{
		ResourceType: "workflow", ResourceName: "Fixture", ClientResourceKey: "workflow:cloud", DesktopVersion: "1.0.0", Files: files,
	})
	if err != nil {
		t.Fatal(err)
	}
	adapter := CloudAdapter{DB: db.DB, DesktopVersion: "1.0.0"}
	result, err := adapter.ImportAndBind(context.Background(), cloudresource.ImportRequest{
		OwnerUserID: "local-user", CloudIssuer: "https://cloud.example", CloudAccountID: "account-1",
		Resource: cloudclient.PrivateResource{
			ResourceID: "resource-1", ResourceType: "workflow", ClientResourceKey: "workflow:cloud",
			ResourceName: "Fixture", ContentHash: prepared.Manifest.ContentHash,
		},
		Files: files,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Exists || result.ResourceID == "" || result.ResourceRef == "" || result.RevisionID == "" {
		t.Fatalf("result = %+v", result)
	}
	bindings, err := cloudbinding.NewRepository(db.DB).FindByCloudIDs(context.Background(), "https://cloud.example", "account-1", "workflow", []string{"resource-1"})
	if err != nil || bindings["resource-1"].LocalResourceID != result.ResourceID {
		t.Fatalf("bindings=%+v err=%v", bindings, err)
	}
}

func TestCloudWorkflowAdapterRollsBackWhenBindingFails(t *testing.T) {
	db := newTestDB(t)
	if err := db.AutoMigrate(&orm.WorkflowRevision{}, &orm.WorkflowRevisionEntry{}, &orm.WorkflowBlob{}, &orm.UserWorkflowSetting{}, &cloudbinding.Binding{}); err != nil {
		t.Fatal(err)
	}
	files := map[string]cloudpackage.File{}
	for path, body := range validCloudWorkflowFiles() {
		files[path] = cloudpackage.File{Data: body}
	}
	prepared, err := cloudpackage.Prepare(cloudpackage.PrepareInput{
		ResourceType: "workflow", ResourceName: "Fixture", ClientResourceKey: "workflow:rollback", DesktopVersion: "1.0.0", Files: files,
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = (CloudAdapter{DB: db.DB, DesktopVersion: "1.0.0"}).ImportAndBind(context.Background(), cloudresource.ImportRequest{
		OwnerUserID: "local-user", CloudIssuer: "https://cloud.example",
		Resource: cloudclient.PrivateResource{ResourceID: "resource-1", ResourceType: "workflow", ClientResourceKey: "workflow:rollback", ResourceName: "Fixture", ContentHash: prepared.Manifest.ContentHash},
		Files:    files,
	})
	if err == nil {
		t.Fatal("binding failure must fail Workflow import")
	}
	var count int64
	if err := db.Model(&orm.WorkflowResource{}).Where("owner_user_id = ?", "local-user").Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("rolled-back Workflow rows = %d", count)
	}
}
