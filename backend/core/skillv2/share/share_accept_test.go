package share

import (
	"context"
	"testing"

	"lazymind/core/skillv2/testutil"
)

func TestShareAccept_CopiesSourceHeadRevision(t *testing.T) {
	db := testutil.NewTestDB(t)
	testutil.SeedSkillWithRevision(t, db, "source_skill", "source_rev1")
	shareID := seedShareItem(t, db, "share1", "source_skill", "user_002", "pending")
	service := NewService(ServiceDeps{DB: db.DB, BlobStore: NewBlobStore(db.DB, NewLocalObjectStore(t.TempDir()))})

	resp, err := service.Accept(context.Background(), AcceptRequest{ShareItemID: shareID, UserID: "user_002", UserName: "李四"})
	if err != nil {
		t.Fatalf("Accept returned error: %v", err)
	}
	if resp.TargetSkillID == "" || resp.TargetSkillID == "source_skill" {
		t.Fatalf("Accept did not create target skill copy: %#v", resp)
	}
	var target testutil.SkillRow
	if err := db.Where("id = ?", resp.TargetSkillID).Take(&target).Error; err != nil {
		t.Fatalf("query target skill: %v", err)
	}
	if target.OwnerUserID != "user_002" || target.HeadRevisionID == nil {
		t.Fatalf("target skill invalid: %#v", target)
	}
	if got := testutil.CountRows(t, db, "skill_revision_entries", "revision_id = ?", *target.HeadRevisionID); got == 0 {
		t.Fatal("target skill revision has no entries")
	}
}

func TestShareAccept_SourceMissingOrForbidden(t *testing.T) {
	db := testutil.NewTestDB(t)
	missingShareID := seedShareItem(t, db, "share_missing", "missing_skill", "user_002", "pending")
	testutil.SeedSkillWithRevision(t, db, "source_skill", "source_rev1")
	forbiddenShareID := seedShareItem(t, db, "share_forbidden", "source_skill", "user_003", "pending")
	service := NewService(ServiceDeps{DB: db.DB, BlobStore: NewBlobStore(db.DB, NewLocalObjectStore(t.TempDir()))})

	if _, err := service.Accept(context.Background(), AcceptRequest{ShareItemID: missingShareID, UserID: "user_002", UserName: "李四"}); err == nil {
		t.Fatal("Accept succeeded with missing source skill")
	}
	if _, err := service.Accept(context.Background(), AcceptRequest{ShareItemID: forbiddenShareID, UserID: "user_002", UserName: "李四"}); err == nil {
		t.Fatal("Accept succeeded for user that is not share target")
	}
	if got := testutil.CountRows(t, db, "skills", "owner_user_id = ?", "user_002"); got != 0 {
		t.Fatalf("target skill count = %d, want 0", got)
	}
}

func seedShareItem(t *testing.T, db *testutil.TestDB, id, sourceSkillID, targetUserID, status string) string {
	t.Helper()
	item := map[string]any{
		"id":              id,
		"source_skill_id": sourceSkillID,
		"target_user_id":  targetUserID,
		"status":          status,
		"created_at":      testutil.TimeFixture(),
		"updated_at":      testutil.TimeFixture(),
	}
	if err := db.Table("skill_share_items").Create(item).Error; err != nil {
		t.Fatalf("seed share item: %v", err)
	}
	return id
}
