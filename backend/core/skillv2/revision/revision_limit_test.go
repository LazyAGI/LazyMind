package revision

import (
	"context"
	"fmt"
	"testing"

	"lazymind/core/skillv2/testutil"
)

func TestRevisionLimit_DeletesOldestWhenCreating51stRevision(t *testing.T) {
	db := testutil.NewTestDB(t)
	seedFiftyRevisions(t, db, "skill1", "rev50", "rev50")
	testutil.SeedDraftEntry(t, db, "skill1", "SKILL.md", "upsert", "file", "h_skill_rev50")
	service := NewService(ServiceDeps{DB: db.DB, BlobStore: NewBlobStore(db.DB, NewLocalObjectStore(t.TempDir())), MaxRevisions: 50})

	resp, err := service.CommitDraft(context.Background(), CommitDraftRequest{SkillID: "skill1", UserID: "user_001", DraftVersion: 1})
	if err != nil {
		t.Fatalf("CommitDraft returned error: %v", err)
	}
	if resp.RevisionNo != 51 {
		t.Fatalf("RevisionNo = %d, want 51", resp.RevisionNo)
	}
	if got := testutil.CountRows(t, db, "skill_revisions", "skill_id = ?", "skill1"); got != 50 {
		t.Fatalf("revision count = %d, want 50", got)
	}
	if got := testutil.CountRows(t, db, "skill_revisions", "id = ?", "rev1"); got != 0 {
		t.Fatalf("rev1 count = %d, want 0", got)
	}
	testutil.AssertHeadRevision(t, db, "skill1", resp.RevisionID)
}

func TestRevisionLimit_PreservesRolledBackHeadAsNewRevisionParent(t *testing.T) {
	db := testutil.NewTestDB(t)
	seedFiftyRevisions(t, db, "skill1", "rev1", "rev1")
	testutil.SeedDraftEntry(t, db, "skill1", "SKILL.md", "upsert", "file", "h_skill_rev50")
	service := NewService(ServiceDeps{DB: db.DB, BlobStore: NewBlobStore(db.DB, NewLocalObjectStore(t.TempDir())), MaxRevisions: 50})

	resp, err := service.CommitDraft(context.Background(), CommitDraftRequest{SkillID: "skill1", UserID: "user_001", DraftVersion: 1})
	if err != nil {
		t.Fatalf("CommitDraft returned error: %v", err)
	}
	if got := testutil.CountRows(t, db, "skill_revisions", "skill_id = ?", "skill1"); got != 50 {
		t.Fatalf("revision count = %d, want 50", got)
	}
	if got := testutil.CountRows(t, db, "skill_revisions", "id = ?", "rev1"); got != 1 {
		t.Fatalf("draft base rev1 count = %d, want 1", got)
	}
	if got := testutil.CountRows(t, db, "skill_revisions", "id = ?", "rev2"); got != 0 {
		t.Fatalf("rev2 count = %d, want 0", got)
	}
	testutil.AssertHeadRevision(t, db, "skill1", resp.RevisionID)
}

func seedFiftyRevisions(t *testing.T, db *testutil.TestDB, skillID, headRevisionID, draftBaseRevisionID string) {
	t.Helper()
	testutil.SeedSkillWithRevision(t, db, skillID, "rev1")
	for i := 2; i <= 50; i++ {
		revisionID := fmt.Sprintf("rev%d", i)
		parent := fmt.Sprintf("rev%d", i-1)
		testutil.MustCreate(t, db, &testutil.SkillRevisionRow{
			ID:               revisionID,
			SkillID:          skillID,
			ParentRevisionID: &parent,
			RevisionNo:       int64(i),
			TreeHash:         "tree_" + revisionID,
			ChangeSource:     "draft_commit",
			CreatedAt:        testutil.TimeFixture(),
		})
		hash := "h_skill_" + revisionID
		testutil.SeedTextBlob(t, db, hash, "# "+revisionID+"\n")
		testutil.SeedRevisionEntry(t, db, revisionID, "SKILL.md", "file", hash, "markdown")
	}
	if err := db.Model(&testutil.SkillRow{}).Where("id = ?", skillID).Update("head_revision_id", headRevisionID).Error; err != nil {
		t.Fatalf("update head revision: %v", err)
	}
	if err := db.Model(&testutil.SkillDraftRow{}).Where("skill_id = ?", skillID).Updates(map[string]any{
		"base_revision_id": draftBaseRevisionID,
		"version":          1,
	}).Error; err != nil {
		t.Fatalf("update draft base: %v", err)
	}
}
