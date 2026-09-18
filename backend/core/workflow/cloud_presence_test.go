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

func TestDesktopImportedWorkflowIdentityDoesNotDependOnAnAuthoringDraft(t *testing.T) {
	db := newTestDB(t)
	if err := db.AutoMigrate(&orm.WorkflowRevision{}, &orm.WorkflowRevisionEntry{}, &orm.WorkflowBlob{}, &orm.UserWorkflowSetting{}, &orm.WorkflowDraft{}, &cloudbinding.Binding{}); err != nil {
		t.Fatal(err)
	}
	files := map[string]cloudpackage.File{}
	for path, body := range validCloudWorkflowFiles() {
		files[path] = cloudpackage.File{Data: body}
	}
	prepared, err := cloudpackage.Prepare(cloudpackage.PrepareInput{ResourceType: "workflow", ResourceName: "Fixture", ClientResourceKey: "workflow:fixture", DesktopVersion: "1.0.0", Files: files})
	if err != nil {
		t.Fatal(err)
	}
	adapter := CloudAdapter{DB: db.DB, DesktopVersion: "1.0.0"}
	imported, err := adapter.ImportAndBind(context.Background(), cloudresource.ImportRequest{OwnerUserID: "local-user", CloudIssuer: "https://cloud.example", CloudAccountID: "account-a", Resource: cloudclient.PrivateResource{ResourceID: "cloud-fixture", ResourceType: "workflow", ResourceName: "Fixture", ClientResourceKey: "workflow:fixture", ContentHash: prepared.Manifest.ContentHash}, Files: files})
	if err != nil {
		t.Fatal(err)
	}
	var count int64
	if err := db.Model(&orm.WorkflowDraft{}).Count(&count).Error; err != nil || count != 0 {
		t.Fatalf("fixture must have no authoring draft: count=%d err=%v", count, err)
	}
	for _, id := range []string{imported.ResourceID, imported.ResourceRef} {
		probe, err := adapter.Probe(context.Background(), "local-user", id)
		if err != nil || !probe.Exists || probe.ResourceID != imported.ResourceID || probe.ResourceRef != imported.ResourceRef {
			t.Fatalf("published identity cannot be resolved without a draft: %+v err=%v", probe, err)
		}
	}
	other, err := adapter.Probe(context.Background(), "another-local-user", imported.ResourceID)
	if err != nil || other.Exists {
		t.Fatalf("other owner's imported workflow appears local: %+v err=%v", other, err)
	}
	if err := db.Model(&orm.WorkflowResource{}).Where("id = ?", imported.ResourceID).Update("status", "archived").Error; err != nil {
		t.Fatal(err)
	}
	archived, err := adapter.Probe(context.Background(), "local-user", imported.ResourceID)
	if err != nil || archived.Exists {
		t.Fatalf("archived workflow must not suppress a Cloud-only row: %+v err=%v", archived, err)
	}
}
