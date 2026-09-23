package share

import (
	"context"
	"testing"

	"lazymind/core/skillv2/testutil"
)

func TestShareAccept_CopiesSourceHeadRevision(t *testing.T) {
	db := testutil.NewTestDB(t)
	testutil.SeedSkillWithRevision(t, db, "source_skill", "source_rev1")
	if err := db.Table("skills").Where("id = ?", "source_skill").Update("original_revision_id", "source_rev1").Error; err != nil {
		t.Fatal(err)
	}
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
	if target.OriginalRevisionID == nil || target.HeadRevisionID == nil || *target.OriginalRevisionID != *target.HeadRevisionID || *target.OriginalRevisionID == "source_rev1" {
		t.Fatalf("copied original pointer not independently owned: %v", target)
	}
	if target.OwnerUserID != "user_002" || target.HeadRevisionID == nil {
		t.Fatalf("target skill invalid: %#v", target)
	}
	if got := testutil.CountRows(t, db, "skill_revision_entries", "revision_id = ?", *target.HeadRevisionID); got == 0 {
		t.Fatal("target skill revision has no entries")
	}
}

func TestShareAccept_PreservesManualCallMode(t *testing.T) {
	db := testutil.NewTestDB(t)
	testutil.SeedSkillWithRevision(t, db, "source_skill", "source_rev1")
	if err := db.Exec("UPDATE skills SET is_enabled = ?, call_mode = ? WHERE id = ?", false, "manual", "source_skill").Error; err != nil {
		t.Fatalf("set source call mode: %v", err)
	}
	var source testutil.SkillRow
	if err := db.Where("id = ?", "source_skill").Take(&source).Error; err != nil {
		t.Fatalf("query source skill: %v", err)
	}
	if source.IsEnabled || source.CallMode != "manual" {
		t.Fatalf("source call mode = enabled:%v mode:%q, want disabled manual", source.IsEnabled, source.CallMode)
	}
	shareID := seedShareItem(t, db, "share_manual", "source_skill", "user_002", "pending")
	service := NewService(ServiceDeps{DB: db.DB, BlobStore: NewBlobStore(db.DB, NewLocalObjectStore(t.TempDir()))})

	resp, err := service.Accept(context.Background(), AcceptRequest{ShareItemID: shareID, UserID: "user_002", UserName: "李四"})
	if err != nil {
		t.Fatalf("Accept returned error: %v", err)
	}
	var target testutil.SkillRow
	if err := db.Where("id = ?", resp.TargetSkillID).Take(&target).Error; err != nil {
		t.Fatalf("query target skill: %v", err)
	}
	if target.IsEnabled || target.CallMode != "manual" {
		t.Fatalf("copied call mode = enabled:%v mode:%q, want disabled manual", target.IsEnabled, target.CallMode)
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

func TestShareAccept_RejectsDuplicateName(t *testing.T) {
	db := testutil.NewTestDB(t)
	testutil.SeedSkillWithRevision(t, db, "source_skill", "source_rev1")
	testutil.SeedSkillWithRevision(t, db, "user_skill", "user_rev1")
	if err := db.Model(&testutil.SkillRow{}).Where("id = ?", "user_skill").Updates(map[string]any{
		"owner_user_id":    "user_002",
		"owner_user_name":  "李四",
		"create_user_id":   "user_002",
		"create_user_name": "李四",
		"category":         "research",
		"skill_name":       "论文精读-source_skill",
		"relative_root":    "research/论文精读-source_skill",
	}).Error; err != nil {
		t.Fatalf("seed conflicting target skill: %v", err)
	}
	shareID := seedShareItem(t, db, "share_conflict", "source_skill", "user_002", "pending")
	service := NewService(ServiceDeps{DB: db.DB, BlobStore: NewBlobStore(db.DB, NewLocalObjectStore(t.TempDir()))})

	if _, err := service.Accept(context.Background(), AcceptRequest{ShareItemID: shareID, UserID: "user_002", UserName: "李四"}); err == nil || err.Error() != "skill already exists" {
		t.Fatalf("Accept error = %v, want skill already exists", err)
	}
	if got := testutil.CountRows(t, db, "skills", "owner_user_id = ?", "user_002"); got != 1 {
		t.Fatalf("target skill count = %d, want 1", got)
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
