package revision

import (
	"context"
	"sync"
	"testing"

	"lazymind/core/skillv2/testutil"
)

func TestCommitDraft_CreatesOneRevisionForMultipleFiles(t *testing.T) {
	db := testutil.NewTestDB(t)
	testutil.SeedSkillWithRevision(t, db, "skill1", "rev1")
	testutil.SeedTextBlob(t, db, "h_skill_draft", "# 草稿\n")
	testutil.SeedTextBlob(t, db, "h_ref_draft", "# 草稿资料\n")
	testutil.SeedDraftEntry(t, db, "skill1", "SKILL.md", "upsert", "file", "h_skill_draft")
	testutil.SeedDraftEntry(t, db, "skill1", "references/a.md", "upsert", "file", "h_ref_draft")
	service := NewService(ServiceDeps{DB: db.DB, BlobStore: NewBlobStore(db.DB, NewLocalObjectStore(t.TempDir()))})

	resp, err := service.CommitDraft(context.Background(), CommitDraftRequest{SkillID: "skill1", UserID: "user_001", DraftVersion: 1})
	if err != nil {
		t.Fatalf("CommitDraft returned error: %v", err)
	}
	if resp.RevisionID == "" || resp.RevisionID == "rev1" {
		t.Fatalf("CommitDraft returned invalid revision id: %#v", resp)
	}
	testutil.AssertHeadRevision(t, db, "skill1", resp.RevisionID)
	testutil.AssertRevisionEntries(t, db, resp.RevisionID, []testutil.ExpectedEntry{
		{Path: "SKILL.md", EntryType: "file", FileType: "markdown", HasBlob: true},
		{Path: "references/a.md", EntryType: "file", FileType: "markdown", HasBlob: true},
	})
	testutil.AssertNoDraftEntries(t, db, "skill1")
	var draft testutil.SkillDraftRow
	if err := db.Where("skill_id = ?", "skill1").Take(&draft).Error; err != nil {
		t.Fatalf("query draft: %v", err)
	}
	if draft.BaseRevisionID == nil || *draft.BaseRevisionID != resp.RevisionID {
		t.Fatalf("base_revision_id = %v, want %q", draft.BaseRevisionID, resp.RevisionID)
	}
}

func TestCommitDraft_RejectsEmptyCommit(t *testing.T) {
	db := testutil.NewTestDB(t)
	testutil.SeedSkillWithRevision(t, db, "skill1", "rev1")
	service := NewService(ServiceDeps{DB: db.DB, BlobStore: NewBlobStore(db.DB, NewLocalObjectStore(t.TempDir()))})

	if _, err := service.CommitDraft(context.Background(), CommitDraftRequest{SkillID: "skill1", UserID: "user_001", DraftVersion: 1}); err == nil {
		t.Fatal("CommitDraft succeeded with no draft overlay")
	}
	testutil.AssertHeadRevision(t, db, "skill1", "rev1")
	if got := testutil.CountRows(t, db, "skill_revisions", "skill_id = ?", "skill1"); got != 1 {
		t.Fatalf("revision count = %d, want 1", got)
	}
}

func TestCommitDraft_RejectsStaleDraftVersion(t *testing.T) {
	db := testutil.NewTestDB(t)
	testutil.SeedSkillWithRevision(t, db, "skill1", "rev1")
	if err := db.Model(&testutil.SkillDraftRow{}).Where("skill_id = ?", "skill1").Update("version", 3).Error; err != nil {
		t.Fatalf("update draft version: %v", err)
	}
	testutil.SeedDraftEntry(t, db, "skill1", "SKILL.md", "upsert", "file", "h_draft")
	service := NewService(ServiceDeps{DB: db.DB, BlobStore: NewBlobStore(db.DB, NewLocalObjectStore(t.TempDir()))})

	if _, err := service.CommitDraft(context.Background(), CommitDraftRequest{SkillID: "skill1", UserID: "user_001", DraftVersion: 2}); err == nil {
		t.Fatal("CommitDraft succeeded with stale draft_version")
	}
	testutil.AssertHeadRevision(t, db, "skill1", "rev1")
	if got := testutil.CountRows(t, db, "skill_draft_entries", "skill_id = ?", "skill1"); got != 1 {
		t.Fatalf("draft entry count = %d, want 1", got)
	}
}

func TestCommitDraft_ConcurrentCommitOnlyOneSucceeds(t *testing.T) {
	db := testutil.NewTestDB(t)
	testutil.SeedSkillWithRevision(t, db, "skill1", "rev1")
	if err := db.Model(&testutil.SkillDraftRow{}).Where("skill_id = ?", "skill1").Update("version", 3).Error; err != nil {
		t.Fatalf("update draft version: %v", err)
	}
	testutil.SeedTextBlob(t, db, "h_draft", "# 并发提交\n")
	testutil.SeedDraftEntry(t, db, "skill1", "SKILL.md", "upsert", "file", "h_draft")
	service := NewService(ServiceDeps{DB: db.DB, BlobStore: NewBlobStore(db.DB, NewLocalObjectStore(t.TempDir()))})

	var wg sync.WaitGroup
	results := make(chan error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := service.CommitDraft(context.Background(), CommitDraftRequest{SkillID: "skill1", UserID: "user_001", DraftVersion: 3})
			results <- err
		}()
	}
	wg.Wait()
	close(results)

	successes := 0
	failures := 0
	for err := range results {
		if err == nil {
			successes++
		} else {
			failures++
		}
	}
	if successes != 1 || failures != 1 {
		t.Fatalf("concurrent commit successes=%d failures=%d, want 1/1", successes, failures)
	}
	if got := testutil.CountRows(t, db, "skill_revisions", "skill_id = ?", "skill1"); got != 2 {
		t.Fatalf("revision count = %d, want 2", got)
	}
	testutil.AssertNoDraftEntries(t, db, "skill1")
}

func TestCommitDraft_BlobWriteFailureRollsBack(t *testing.T) {
	db := testutil.NewTestDB(t)
	testutil.SeedSkillWithRevision(t, db, "skill1", "rev1")
	testutil.SeedDraftEntry(t, db, "skill1", "SKILL.md", "upsert", "file", "h_missing")
	blobStore := NewFailingBlobStore("write failed")
	service := NewService(ServiceDeps{DB: db.DB, BlobStore: blobStore})

	if _, err := service.CommitDraft(context.Background(), CommitDraftRequest{SkillID: "skill1", UserID: "user_001", DraftVersion: 1}); err == nil {
		t.Fatal("CommitDraft succeeded despite blob write failure")
	}
	testutil.AssertHeadRevision(t, db, "skill1", "rev1")
	if got := testutil.CountRows(t, db, "skill_revisions", "skill_id = ?", "skill1"); got != 1 {
		t.Fatalf("revision count = %d, want 1", got)
	}
	if got := testutil.CountRows(t, db, "skill_draft_entries", "skill_id = ?", "skill1"); got != 1 {
		t.Fatalf("draft entry count = %d, want 1", got)
	}
}

func TestCommitDraft_ReplacedBlobCleanupRespectsReferences(t *testing.T) {
	db := testutil.NewTestDB(t)
	testutil.SeedSkillWithRevision(t, db, "skill1", "rev1")
	testutil.SeedTextBlob(t, db, "h_new", "# 新内容\n")
	testutil.SeedDraftEntry(t, db, "skill1", "SKILL.md", "upsert", "file", "h_new")
	service := NewService(ServiceDeps{DB: db.DB, BlobStore: NewBlobStore(db.DB, NewLocalObjectStore(t.TempDir()))})

	if _, err := service.CommitDraft(context.Background(), CommitDraftRequest{SkillID: "skill1", UserID: "user_001", DraftVersion: 1}); err != nil {
		t.Fatalf("CommitDraft returned error: %v", err)
	}
	if got := testutil.CountRows(t, db, "skill_blobs", "hash = ?", "h_skill_rev1"); got != 1 {
		t.Fatalf("historical revision blob count = %d, want 1", got)
	}

	testutil.SeedSkillWithRevision(t, db, "skill2", "rev_other")
	testutil.SeedRevisionEntry(t, db, "rev_other", "copy.md", "file", "h_new", "markdown")
	testutil.SeedDraftEntry(t, db, "skill1", "SKILL.md", "upsert", "file", "h_skill_rev1")
	if _, err := service.CommitDraft(context.Background(), CommitDraftRequest{SkillID: "skill1", UserID: "user_001", DraftVersion: 2}); err != nil {
		t.Fatalf("second CommitDraft returned error: %v", err)
	}
	if got := testutil.CountRows(t, db, "skill_blobs", "hash = ?", "h_new"); got != 1 {
		t.Fatalf("shared old blob count = %d, want 1", got)
	}
}
