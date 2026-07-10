package market

import (
	"context"
	"testing"

	"lazymind/core/skillv2/testutil"
)

func TestMarketInstall_CopiesSkillTreeForUser(t *testing.T) {
	db := testutil.NewTestDB(t)
	testutil.SeedSkillWithRevision(t, db, "market_skill", "market_rev1")
	testutil.MustCreate(t, db, &testutil.SkillMarketItemRow{
		ID:            "market_item1",
		SourceSkillID: "market_skill",
		Status:        "published",
		CreatedAt:     testutil.TimeFixture(),
		UpdatedAt:     testutil.TimeFixture(),
	})
	service := NewService(ServiceDeps{DB: db.DB, BlobStore: NewBlobStore(db.DB, NewLocalObjectStore(t.TempDir()))})

	resp, err := service.Install(context.Background(), InstallRequest{MarketItemID: "market_item1", UserID: "user_002", UserName: "李四"})
	if err != nil {
		t.Fatalf("Install returned error: %v", err)
	}
	if resp.SkillID == "" || resp.SkillID == "market_skill" {
		t.Fatalf("Install did not create user-owned skill copy: %#v", resp)
	}
	var copied testutil.SkillRow
	if err := db.Where("id = ?", resp.SkillID).Take(&copied).Error; err != nil {
		t.Fatalf("query installed skill: %v", err)
	}
	if copied.OwnerUserID != "user_002" || copied.HeadRevisionID == nil {
		t.Fatalf("installed skill owner/head invalid: %#v", copied)
	}
	if got := testutil.CountRows(t, db, "skill_revision_entries", "revision_id = ?", *copied.HeadRevisionID); got == 0 {
		t.Fatal("installed skill revision has no entries")
	}
}

func TestMarketInstall_DoesNotReferenceMarketAsTruth(t *testing.T) {
	db := testutil.NewTestDB(t)
	testutil.SeedSkillWithRevision(t, db, "market_skill", "market_rev1")
	testutil.MustCreate(t, db, &testutil.SkillMarketItemRow{ID: "market_item1", SourceSkillID: "market_skill", Status: "published", CreatedAt: testutil.TimeFixture(), UpdatedAt: testutil.TimeFixture()})
	service := NewService(ServiceDeps{DB: db.DB, BlobStore: NewBlobStore(db.DB, NewLocalObjectStore(t.TempDir()))})

	resp, err := service.Install(context.Background(), InstallRequest{MarketItemID: "market_item1", UserID: "user_002", UserName: "李四"})
	if err != nil {
		t.Fatalf("Install returned error: %v", err)
	}
	if err := db.Model(&testutil.SkillRevisionEntryRow{}).Where("revision_id = ? AND path = ?", "market_rev1", "SKILL.md").Update("path", "changed.md").Error; err != nil {
		t.Fatalf("mutate market source fixture: %v", err)
	}
	tree, err := service.GetInstalledTree(context.Background(), GetInstalledTreeRequest{SkillID: resp.SkillID, UserID: "user_002"})
	if err != nil {
		t.Fatalf("GetInstalledTree returned error: %v", err)
	}
	if !tree.HasPath("SKILL.md") || tree.HasPath("changed.md") {
		t.Fatalf("installed tree still references market truth: %#v", tree)
	}
}

func TestMarketAdminPublishEditUnpublish(t *testing.T) {
	db := testutil.NewTestDB(t)
	service := NewAdminService(AdminServiceDeps{DB: db.DB, BlobStore: NewBlobStore(db.DB, NewLocalObjectStore(t.TempDir()))})

	published, err := service.Publish(context.Background(), PublishRequest{
		AdminUserID: "admin_001",
		Name:        "论文精读",
		Category:    "research",
		Source: SourceInput{
			Type:     "uploaded_zip",
			UploadID: "upload_market_zip",
		},
	})
	if err != nil {
		t.Fatalf("Publish returned error: %v", err)
	}
	if published.MarketItemID == "" || published.SourceSkillID == "" {
		t.Fatalf("Publish returned incomplete response: %#v", published)
	}
	if got := testutil.CountRows(t, db, "skill_market_items", "id = ? AND status = ?", published.MarketItemID, "published"); got != 1 {
		t.Fatalf("published market item count = %d, want 1", got)
	}

	if _, err := service.Edit(context.Background(), EditRequest{AdminUserID: "admin_001", MarketItemID: published.MarketItemID, VersionNote: "v2"}); err != nil {
		t.Fatalf("Edit returned error: %v", err)
	}
	if _, err := service.Unpublish(context.Background(), UnpublishRequest{AdminUserID: "admin_001", MarketItemID: published.MarketItemID}); err != nil {
		t.Fatalf("Unpublish returned error: %v", err)
	}
	if got := testutil.CountRows(t, db, "skill_market_items", "id = ? AND status = ?", published.MarketItemID, "unpublished"); got != 1 {
		t.Fatalf("unpublished market item count = %d, want 1", got)
	}
}

func TestMarketInstall_NameConflict(t *testing.T) {
	db := testutil.NewTestDB(t)
	testutil.SeedSkillWithRevision(t, db, "user_skill", "user_rev1")
	testutil.SeedSkillWithRevision(t, db, "market_skill", "market_rev1")
	testutil.MustCreate(t, db, &testutil.SkillMarketItemRow{ID: "market_item1", SourceSkillID: "market_skill", Status: "published", CreatedAt: testutil.TimeFixture(), UpdatedAt: testutil.TimeFixture()})
	service := NewService(ServiceDeps{DB: db.DB, BlobStore: NewBlobStore(db.DB, NewLocalObjectStore(t.TempDir()))})

	if _, err := service.Install(context.Background(), InstallRequest{MarketItemID: "market_item1", UserID: "user_001", UserName: "张三"}); err == nil {
		t.Fatal("Install succeeded despite same category/name conflict")
	}
	if got := testutil.CountRows(t, db, "skills", "owner_user_id = ?", "user_001"); got != 2 {
		t.Fatalf("user skill count = %d, want 2", got)
	}
}
