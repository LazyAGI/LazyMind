package review

import (
	"context"
	"testing"

	skilldiff "lazymind/core/skillv2/diff"
	"lazymind/core/skillv2/service"
	"lazymind/core/skillv2/testutil"
)

func TestPrepareFile_ReturnsStableHunkIDForSameSnapshot(t *testing.T) {
	db := testutil.NewTestDB(t)
	seedDraftReviewFixture(t, db)
	svc := newTestService(t, db)

	first := prepareSkillFile(t, svc, db)
	second := prepareSkillFile(t, svc, db)

	firstHunk := first.DiffEntryLines[0]
	secondHunk := second.DiffEntryLines[0]
	if first.ReviewID == "" || first.ReviewID != second.ReviewID {
		t.Fatalf("review id is not stable: first=%q second=%q", first.ReviewID, second.ReviewID)
	}
	if firstHunk.HunkID == "" || firstHunk.HunkID != secondHunk.HunkID {
		t.Fatalf("hunk id is not stable: first=%q second=%q", firstHunk.HunkID, secondHunk.HunkID)
	}
	if firstHunk.Decision != decisionPending {
		t.Fatalf("decision = %q, want pending", firstHunk.Decision)
	}
}

func TestActionAndUndo_UpdateReviewStateByBatch(t *testing.T) {
	db := testutil.NewTestDB(t)
	seedDraftReviewFixture(t, db)
	svc := newTestService(t, db)
	file := prepareSkillFile(t, svc, db)
	hunkID := file.DiffEntryLines[0].HunkID

	action, err := svc.Action(context.Background(), ActionRequest{
		SkillID:               "skill1",
		UserID:                "user_001",
		ReviewID:              file.ReviewID,
		ExpectedReviewVersion: file.ReviewVersion,
		Items: []ActionItem{{
			Path:     "SKILL.md",
			HunkID:   hunkID,
			Decision: decisionAccepted,
		}},
	})
	if err != nil {
		t.Fatalf("Action returned error: %v", err)
	}
	if action.ReviewVersion != file.ReviewVersion+1 || !action.CanUndo {
		t.Fatalf("unexpected action response: %#v", action)
	}

	undo, err := svc.Undo(context.Background(), UndoRequest{
		SkillID:               "skill1",
		UserID:                "user_001",
		ReviewID:              file.ReviewID,
		ExpectedReviewVersion: action.ReviewVersion,
	})
	if err != nil {
		t.Fatalf("Undo returned error: %v", err)
	}
	if len(undo.Items) != 1 || undo.Items[0].Decision != decisionPending {
		t.Fatalf("unexpected undo items: %#v", undo.Items)
	}
	if undo.CanUndo {
		t.Fatalf("can_undo = true, want false")
	}
}

func TestCommit_RejectHunkCreatesFormalRevisionAndClearsDraft(t *testing.T) {
	db := testutil.NewTestDB(t)
	seedDraftReviewFixture(t, db)
	svc := newTestService(t, db)
	file := prepareSkillFile(t, svc, db)
	hunkID := file.DiffEntryLines[0].HunkID

	action, err := svc.Action(context.Background(), ActionRequest{
		SkillID:               "skill1",
		UserID:                "user_001",
		ReviewID:              file.ReviewID,
		ExpectedReviewVersion: file.ReviewVersion,
		Items: []ActionItem{{
			Path:     "SKILL.md",
			HunkID:   hunkID,
			Decision: decisionRejected,
		}},
	})
	if err != nil {
		t.Fatalf("Action returned error: %v", err)
	}
	commit, err := svc.Commit(context.Background(), CommitRequest{
		SkillID:               "skill1",
		UserID:                "user_001",
		ReviewID:              file.ReviewID,
		ExpectedReviewVersion: action.ReviewVersion,
	})
	if err != nil {
		t.Fatalf("Commit returned error: %v", err)
	}
	if commit.RevisionID == "" || commit.RevisionNo != 2 {
		t.Fatalf("unexpected commit response: %#v", commit)
	}
	testutil.AssertHeadRevision(t, db, "skill1", commit.RevisionID)
	testutil.AssertNoDraftEntries(t, db, "skill1")

	var entry testutil.SkillRevisionEntryRow
	if err := db.Where("revision_id = ? AND path = ?", commit.RevisionID, "SKILL.md").Take(&entry).Error; err != nil {
		t.Fatalf("query committed entry: %v", err)
	}
	var blob testutil.SkillBlobRow
	if err := db.Where("hash = ?", *entry.BlobHash).Take(&blob).Error; err != nil {
		t.Fatalf("query committed blob: %v", err)
	}
	if string(blob.Content) != "# 论文精读\n" {
		t.Fatalf("committed content = %q, want base content", string(blob.Content))
	}
	if got := testutil.CountRows(t, db, "skill_draft_review_action_items", "review_session_id = ?", file.ReviewID); got != 0 {
		t.Fatalf("review action items = %d, want 0", got)
	}
	if got := testutil.CountRows(t, db, "skill_draft_review_action_batches", "review_session_id = ?", file.ReviewID); got != 0 {
		t.Fatalf("review action batches = %d, want 0", got)
	}
	if got := testutil.CountRows(t, db, "skill_draft_review_sessions", "id = ?", file.ReviewID); got != 0 {
		t.Fatalf("review sessions = %d, want 0", got)
	}
}

func newTestService(t *testing.T, db *testutil.TestDB) *Service {
	t.Helper()
	return NewService(ServiceDeps{
		DB:        db.DB,
		BlobStore: service.NewBlobStore(db.DB, service.NewLocalObjectStore(t.TempDir())),
	})
}

func seedDraftReviewFixture(t *testing.T, db *testutil.TestDB) {
	t.Helper()
	testutil.SeedSkillWithRevision(t, db, "skill1", "rev1")
	testutil.SeedTextBlob(t, db, "h_skill_draft", "# 专业写作助手\n\n帮用户改写文章。\n")
	testutil.SeedDraftEntry(t, db, "skill1", "SKILL.md", "upsert", "file", "h_skill_draft")
}

func prepareSkillFile(t *testing.T, svc *Service, db *testutil.TestDB) skilldiff.DiffFile {
	t.Helper()
	session, err := svc.ensureSession(context.Background(), db.DB, "skill1", "user_001")
	if err != nil {
		t.Fatalf("ensureSession returned error: %v", err)
	}
	file, err := svc.diffFileForPath(context.Background(), db.DB, session, "SKILL.md")
	if err != nil {
		t.Fatalf("diffFileForPath returned error: %v", err)
	}
	if len(file.DiffEntryLines) == 0 || file.DiffEntryLines[0].HunkID == "" {
		t.Fatalf("missing hunk metadata in %#v", file.DiffEntryLines)
	}
	return file
}
