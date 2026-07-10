package revision

import (
	"context"
	"testing"

	"lazymind/core/skillv2/testutil"
)

func TestRollback_CreatesNewHeadRevision(t *testing.T) {
	db := testutil.NewTestDB(t)
	testutil.SeedSkillWithRevision(t, db, "skill1", "rev1")
	seedSecondRevision(t, db, "skill1", "rev1", "rev2")
	service := NewService(ServiceDeps{DB: db.DB, BlobStore: NewBlobStore(db.DB, NewLocalObjectStore(t.TempDir()))})

	resp, err := service.Rollback(context.Background(), RollbackRequest{SkillID: "skill1", UserID: "user_001", TargetRevisionID: "rev1"})
	if err != nil {
		t.Fatalf("Rollback returned error: %v", err)
	}
	if resp.NewHeadRevisionID == "" || resp.NewHeadRevisionID == "rev1" || resp.NewHeadRevisionID == "rev2" {
		t.Fatalf("rollback did not create a new head revision: %#v", resp)
	}
	testutil.AssertHeadRevision(t, db, "skill1", resp.NewHeadRevisionID)
	if got := testutil.CountRows(t, db, "skill_revisions", "id IN ?", []string{"rev1", "rev2"}); got != 2 {
		t.Fatalf("history revision count = %d, want 2", got)
	}
}

func TestRollback_RejectsWhenDraftExists(t *testing.T) {
	db := testutil.NewTestDB(t)
	testutil.SeedSkillWithRevision(t, db, "skill1", "rev1")
	seedSecondRevision(t, db, "skill1", "rev1", "rev2")
	testutil.SeedDraftEntry(t, db, "skill1", "SKILL.md", "upsert", "file", "h_draft")
	service := NewService(ServiceDeps{DB: db.DB, BlobStore: NewBlobStore(db.DB, NewLocalObjectStore(t.TempDir()))})

	if _, err := service.Rollback(context.Background(), RollbackRequest{SkillID: "skill1", UserID: "user_001", TargetRevisionID: "rev1"}); err == nil {
		t.Fatal("Rollback succeeded while draft overlay exists")
	}
	testutil.AssertHeadRevision(t, db, "skill1", "rev2")
}

func TestRollbackPreview_ReturnsDiffWithoutCreatingRevision(t *testing.T) {
	db := testutil.NewTestDB(t)
	testutil.SeedSkillWithRevision(t, db, "skill1", "rev1")
	seedSecondRevision(t, db, "skill1", "rev1", "rev2")
	service := NewService(ServiceDeps{DB: db.DB, BlobStore: NewBlobStore(db.DB, NewLocalObjectStore(t.TempDir()))})

	preview, err := service.RollbackPreview(context.Background(), RollbackPreviewRequest{SkillID: "skill1", UserID: "user_001", TargetRevisionID: "rev1"})
	if err != nil {
		t.Fatalf("RollbackPreview returned error: %v", err)
	}
	if len(preview.TreeDiff.Files) == 0 {
		t.Fatalf("RollbackPreview returned empty tree diff: %#v", preview)
	}
	testutil.AssertHeadRevision(t, db, "skill1", "rev2")
	if got := testutil.CountRows(t, db, "skill_revisions", "skill_id = ?", "skill1"); got != 2 {
		t.Fatalf("revision count = %d, want 2", got)
	}
}

func TestRollbackPreview_WithDraftReturnsWarningAndDiff(t *testing.T) {
	db := testutil.NewTestDB(t)
	testutil.SeedSkillWithRevision(t, db, "skill1", "rev1")
	seedSecondRevision(t, db, "skill1", "rev1", "rev2")
	testutil.SeedDraftEntry(t, db, "skill1", "SKILL.md", "upsert", "file", "h_draft")
	service := NewService(ServiceDeps{DB: db.DB, BlobStore: NewBlobStore(db.DB, NewLocalObjectStore(t.TempDir()))})

	preview, err := service.RollbackPreview(context.Background(), RollbackPreviewRequest{SkillID: "skill1", UserID: "user_001", TargetRevisionID: "rev1"})
	if err != nil {
		t.Fatalf("RollbackPreview returned error: %v", err)
	}
	if len(preview.TreeDiff.Files) == 0 || len(preview.Warnings) == 0 || preview.Warnings[0].Code != "draft_conflict" {
		t.Fatalf("expected diff and draft_conflict warning, got %#v", preview)
	}
	testutil.AssertHeadRevision(t, db, "skill1", "rev2")
	if got := testutil.CountRows(t, db, "skill_draft_entries", "skill_id = ?", "skill1"); got != 1 {
		t.Fatalf("draft entry count = %d, want 1", got)
	}
}

func seedSecondRevision(t *testing.T, db *testutil.TestDB, skillID, parentRevisionID, revisionID string) {
	t.Helper()
	parent := parentRevisionID
	testutil.MustCreate(t, db, &testutil.SkillRevisionRow{
		ID:               revisionID,
		SkillID:          skillID,
		ParentRevisionID: &parent,
		RevisionNo:       2,
		TreeHash:         "tree_" + revisionID,
		ChangeSource:     "draft_commit",
		CreatedAt:        testutil.TimeFixture(),
	})
	hash := "h_skill_" + revisionID
	testutil.SeedTextBlob(t, db, hash, "# v2\n")
	testutil.SeedRevisionEntry(t, db, revisionID, "SKILL.md", "file", hash, "markdown")
	if err := db.Model(&testutil.SkillRow{}).Where("id = ?", skillID).Update("head_revision_id", revisionID).Error; err != nil {
		t.Fatalf("update head revision: %v", err)
	}
	if err := db.Model(&testutil.SkillDraftRow{}).Where("skill_id = ?", skillID).Update("base_revision_id", revisionID).Error; err != nil {
		t.Fatalf("update draft base: %v", err)
	}
}
