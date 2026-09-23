package service

import (
	"context"
	"testing"

	"lazymind/core/skillv2/testutil"
)

func TestPrepareCloudSkillPackageReadsOwnedHead(t *testing.T) {
	db := testutil.NewTestDB(t)
	testutil.SeedSkillWithRevision(t, db, "skill1", "rev1")
	service := NewSkillService(SkillServiceDeps{DB: db.DB, BlobStore: NewBlobStore(db.DB, NewLocalObjectStore(t.TempDir()))})

	prepared, err := service.PrepareCloudSkillPackage(context.Background(), CloudSkillExportRequest{
		OwnerUserID: "user_001", SkillID: "skill1", DesktopVersion: "1.0.0",
	})
	if err != nil {
		t.Fatal(err)
	}
	if prepared.Manifest.ResourceType != "skill" || prepared.Manifest.Entrypoint != "SKILL.md" || prepared.Manifest.ClientResourceKey != "skill:skill1" {
		t.Fatalf("manifest = %+v", prepared.Manifest)
	}
	if _, exists := prepared.Files["SKILL.md"]; !exists {
		t.Fatal("SKILL.md missing from Cloud package")
	}
}

func TestPrepareCloudSkillPackageRejectsForeignOwner(t *testing.T) {
	db := testutil.NewTestDB(t)
	testutil.SeedSkillWithRevision(t, db, "skill1", "rev1")
	service := NewSkillService(SkillServiceDeps{DB: db.DB, BlobStore: NewBlobStore(db.DB, NewLocalObjectStore(t.TempDir()))})
	if _, err := service.PrepareCloudSkillPackage(context.Background(), CloudSkillExportRequest{
		OwnerUserID: "other-user", SkillID: "skill1", DesktopVersion: "1.0.0",
	}); err == nil {
		t.Fatal("foreign Skill export must fail")
	}
}

func TestImportCloudSkillPackageUsesExistingImporterAndRejectsDuplicate(t *testing.T) {
	db := testutil.NewTestDB(t)
	service := NewSkillService(SkillServiceDeps{DB: db.DB, BlobStore: NewBlobStore(db.DB, NewLocalObjectStore(t.TempDir()))})
	request := CloudSkillImportRequest{OwnerUserID: "user_001", OwnerUserName: "User", Files: map[string][]byte{
		"SKILL.md": []byte("---\nname: cloud-skill\ndescription: from cloud\n---\n# Cloud Skill\n"),
	}}
	result, err := service.ImportCloudSkillPackage(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if result.SkillID == "" || result.HeadRevisionID == "" {
		t.Fatalf("result = %+v", result)
	}
	if _, err := service.ImportCloudSkillPackage(context.Background(), request); err == nil {
		t.Fatal("repeated Cloud download must not create a duplicate local Skill")
	}
}
