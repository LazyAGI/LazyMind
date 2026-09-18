package service

import (
	"context"
	"testing"

	"lazymind/core/cloudbinding"
	"lazymind/core/cloudclient"
	"lazymind/core/cloudpackage"
	"lazymind/core/cloudresource"
	"lazymind/core/skillv2/testutil"
)

func TestCloudSkillAdapterImportsAndBindsInOneTransaction(t *testing.T) {
	db := testutil.NewTestDB(t)
	if err := db.AutoMigrate(&cloudbinding.Binding{}); err != nil {
		t.Fatal(err)
	}
	service := NewSkillService(SkillServiceDeps{DB: db.DB, BlobStore: NewBlobStore(db.DB, NewLocalObjectStore(t.TempDir()))})
	files := map[string]cloudpackage.File{"SKILL.md": {Data: []byte("---\nname: cloud-skill\ndescription: from cloud\n---\n# Cloud Skill\n")}}
	prepared, err := cloudpackage.Prepare(cloudpackage.PrepareInput{
		ResourceType: "skill", ResourceName: "cloud-skill", ClientResourceKey: "skill:cloud", DesktopVersion: "1.0.0", Files: files,
	})
	if err != nil {
		t.Fatal(err)
	}
	adapter := CloudAdapter{Service: service, DesktopVersion: "1.0.0"}
	result, err := adapter.ImportAndBind(context.Background(), cloudresource.ImportRequest{
		OwnerUserID: "user-1", CloudIssuer: "https://cloud.example", CloudAccountID: "account-1",
		Resource: cloudclient.PrivateResource{
			ResourceID: "resource-1", ResourceType: "skill", ClientResourceKey: "skill:cloud",
			ResourceName: "cloud-skill", ContentHash: prepared.Manifest.ContentHash,
		},
		Files: files,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Exists || result.ResourceID == "" || result.RevisionID == "" {
		t.Fatalf("result = %+v", result)
	}
	bindings, err := cloudbinding.NewRepository(db.DB).FindByCloudIDs(context.Background(), "https://cloud.example", "account-1", "skill", []string{"resource-1"})
	if err != nil || bindings["resource-1"].LocalResourceID != result.ResourceID {
		t.Fatalf("bindings=%+v err=%v", bindings, err)
	}
}

func TestCloudSkillAdapterRollsBackImportWhenBindingFails(t *testing.T) {
	db := testutil.NewTestDB(t)
	if err := db.AutoMigrate(&cloudbinding.Binding{}); err != nil {
		t.Fatal(err)
	}
	service := NewSkillService(SkillServiceDeps{DB: db.DB, BlobStore: NewBlobStore(db.DB, NewLocalObjectStore(t.TempDir()))})
	files := map[string]cloudpackage.File{"SKILL.md": {Data: []byte("---\nname: rollback-skill\ndescription: from cloud\n---\n# Cloud Skill\n")}}
	prepared, err := cloudpackage.Prepare(cloudpackage.PrepareInput{
		ResourceType: "skill", ResourceName: "rollback-skill", ClientResourceKey: "skill:rollback", DesktopVersion: "1.0.0", Files: files,
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = (CloudAdapter{Service: service, DesktopVersion: "1.0.0"}).ImportAndBind(context.Background(), cloudresource.ImportRequest{
		OwnerUserID: "user-1", CloudIssuer: "https://cloud.example",
		Resource: cloudclient.PrivateResource{ResourceID: "resource-1", ResourceType: "skill", ClientResourceKey: "skill:rollback", ResourceName: "rollback-skill", ContentHash: prepared.Manifest.ContentHash},
		Files:    files,
	})
	if err == nil {
		t.Fatal("binding failure must fail the import")
	}
	if count := testutil.CountRows(t, db, "skills", "skill_name = ?", "rollback-skill"); count != 0 {
		t.Fatalf("rolled-back Skill rows = %d", count)
	}
}
