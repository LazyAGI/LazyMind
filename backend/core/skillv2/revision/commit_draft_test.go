package revision

import (
	"context"
	"strings"
	"sync"
	"testing"

	"lazymind/core/skillv2/testutil"
)

func TestCommitDraft_CreatesInitialRevisionWithoutHead(t *testing.T) {
	db := testutil.NewTestDB(t)
	seedCreateDraftSkill(t, db, "skill-new")
	testutil.SeedTextBlob(t, db, "h_skill_new", testutil.SkillMD("new-skill", "New skill"))
	testutil.SeedDraftEntry(t, db, "skill-new", "SKILL.md", "upsert", "file", "h_skill_new")
	service := NewService(ServiceDeps{DB: db.DB, BlobStore: NewBlobStore(db.DB, NewLocalObjectStore(t.TempDir()))})

	resp, err := service.CommitDraft(context.Background(), CommitDraftRequest{SkillID: "skill-new", UserID: "user_001", DraftVersion: 1})
	if err != nil {
		t.Fatalf("CommitDraft returned error: %v", err)
	}
	if resp.RevisionNo != 1 {
		t.Fatalf("revision_no = %d, want 1", resp.RevisionNo)
	}
	testutil.AssertHeadRevision(t, db, "skill-new", resp.RevisionID)
	if got := testutil.CountRows(t, db, "skill_revisions", "skill_id = ? AND parent_revision_id IS NULL", "skill-new"); got != 1 {
		t.Fatalf("initial revision count = %d, want 1", got)
	}
	testutil.AssertNoDraftEntries(t, db, "skill-new")
}

func TestCommitDraft_RejectsInitialRevisionWithoutSkillMD(t *testing.T) {
	db := testutil.NewTestDB(t)
	seedCreateDraftSkill(t, db, "skill-new")
	testutil.SeedTextBlob(t, db, "h_readme", "# Readme\n")
	testutil.SeedDraftEntry(t, db, "skill-new", "README.md", "upsert", "file", "h_readme")
	service := NewService(ServiceDeps{DB: db.DB, BlobStore: NewBlobStore(db.DB, NewLocalObjectStore(t.TempDir()))})

	_, err := service.CommitDraft(context.Background(), CommitDraftRequest{SkillID: "skill-new", UserID: "user_001", DraftVersion: 1})
	if err == nil || !strings.Contains(err.Error(), "SKILL.md") {
		t.Fatalf("CommitDraft error = %v, want missing SKILL.md", err)
	}
	if got := testutil.CountRows(t, db, "skill_revisions", "skill_id = ?", "skill-new"); got != 0 {
		t.Fatalf("revision count = %d, want 0", got)
	}
	if got := testutil.CountRows(t, db, "skill_draft_entries", "skill_id = ?", "skill-new"); got != 1 {
		t.Fatalf("draft entry count = %d, want 1", got)
	}
}

func seedCreateDraftSkill(t *testing.T, db *testutil.TestDB, skillID string) {
	t.Helper()
	now := testutil.TimeFixture()
	testutil.MustCreate(t, db, &testutil.SkillRow{
		ID:                 skillID,
		OwnerUserID:        "user_001",
		CreateUserID:       "user_001",
		Category:           "research",
		SkillName:          "new-skill",
		Tags:               []byte("[]"),
		RelativeRoot:       "research/new-skill",
		SkillMDPath:        "SKILL.md",
		AutoEvoApplyStatus: "idle",
		IsEnabled:          false,
		UpdateStatus:       "up_to_date",
		CreatedAt:          now,
		UpdatedAt:          now,
	})
	testutil.MustCreate(t, db, &testutil.SkillDraftRow{
		SkillID:     skillID,
		DraftStatus: "pending_confirm",
		TaskID:      "task1",
		Version:     1,
		CreatedAt:   now,
		UpdatedAt:   now,
	})
}

func TestCommitDraft_CreatesOneRevisionForMultipleFiles(t *testing.T) {
	db := testutil.NewTestDB(t)
	testutil.SeedSkillWithRevision(t, db, "skill1", "rev1")
	testutil.SeedTextBlob(t, db, "h_skill_draft", testutil.SkillMD("论文精读草稿", "草稿描述"))
	testutil.SeedTextBlob(t, db, "h_ref_draft", "# 草稿资料\n")
	testutil.SeedDraftEntry(t, db, "skill1", "SKILL.md", "upsert", "file", "h_skill_draft")
	testutil.SeedDraftEntry(t, db, "skill1", "references/a.md", "upsert", "file", "h_ref_draft")
	var beforePublish testutil.SkillRow
	if err := db.Where("id = ?", "skill1").Take(&beforePublish).Error; err != nil {
		t.Fatalf("query skill before publish: %v", err)
	}
	if beforePublish.SkillName != "论文精读" {
		t.Fatalf("draft metadata changed published skill name to %q", beforePublish.SkillName)
	}
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
	var skill testutil.SkillRow
	if err := db.Where("id = ?", "skill1").Take(&skill).Error; err != nil {
		t.Fatalf("query skill: %v", err)
	}
	if skill.SkillName != "论文精读草稿" || skill.Description != "草稿描述" || skill.RelativeRoot != "research/论文精读草稿" {
		t.Fatalf("published metadata not synchronized: %#v", skill)
	}
	var draft testutil.SkillDraftRow
	if err := db.Where("skill_id = ?", "skill1").Take(&draft).Error; err != nil {
		t.Fatalf("query draft: %v", err)
	}
	if draft.BaseRevisionID == nil || *draft.BaseRevisionID != resp.RevisionID {
		t.Fatalf("base_revision_id = %v, want %q", draft.BaseRevisionID, resp.RevisionID)
	}
}

func TestCommitDraft_NameConflictRollsBackAndKeepsDraft(t *testing.T) {
	db := testutil.NewTestDB(t)
	testutil.SeedSkillWithRevision(t, db, "skill1", "rev1")
	testutil.SeedSkillWithRevision(t, db, "skill2", "rev2")
	testutil.SeedTextBlob(t, db, "h_conflict", testutil.SkillMD("论文精读-skill2", "冲突描述"))
	testutil.SeedDraftEntry(t, db, "skill1", "SKILL.md", "upsert", "file", "h_conflict")
	service := NewService(ServiceDeps{DB: db.DB, BlobStore: NewBlobStore(db.DB, NewLocalObjectStore(t.TempDir()))})

	if _, err := service.CommitDraft(context.Background(), CommitDraftRequest{SkillID: "skill1", UserID: "user_001", DraftVersion: 1}); err == nil || !strings.Contains(err.Error(), "skill name conflict") {
		t.Fatalf("CommitDraft error = %v, want skill name conflict", err)
	}
	testutil.AssertHeadRevision(t, db, "skill1", "rev1")
	if got := testutil.CountRows(t, db, "skill_draft_entries", "skill_id = ?", "skill1"); got != 1 {
		t.Fatalf("draft entry count = %d, want 1", got)
	}
}

func TestCommitDraft_InvalidFrontmatterRollsBackAndKeepsDraft(t *testing.T) {
	db := testutil.NewTestDB(t)
	testutil.SeedSkillWithRevision(t, db, "skill1", "rev1")
	testutil.SeedTextBlob(t, db, "h_invalid_frontmatter", "# Missing frontmatter\n")
	testutil.SeedDraftEntry(t, db, "skill1", "SKILL.md", "upsert", "file", "h_invalid_frontmatter")
	service := NewService(ServiceDeps{DB: db.DB, BlobStore: NewBlobStore(db.DB, NewLocalObjectStore(t.TempDir()))})

	if _, err := service.CommitDraft(context.Background(), CommitDraftRequest{SkillID: "skill1", UserID: "user_001", DraftVersion: 1}); err == nil || !strings.Contains(err.Error(), "frontmatter") {
		t.Fatalf("CommitDraft error = %v, want invalid frontmatter", err)
	}
	testutil.AssertHeadRevision(t, db, "skill1", "rev1")
	if got := testutil.CountRows(t, db, "skill_revisions", "skill_id = ?", "skill1"); got != 1 {
		t.Fatalf("revision count = %d, want 1", got)
	}
	if got := testutil.CountRows(t, db, "skill_draft_entries", "skill_id = ?", "skill1"); got != 1 {
		t.Fatalf("draft entry count = %d, want 1", got)
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
	testutil.SeedTextBlob(t, db, "h_draft", testutil.SkillMD("并发提交", "并发提交描述"))
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
	testutil.SeedTextBlob(t, db, "h_new", testutil.SkillMD("新内容", "新内容描述"))
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
