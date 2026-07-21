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

func TestPrepareFile_RendersResolvedHunksAsContextAfterDecisions(t *testing.T) {
	db := testutil.NewTestDB(t)
	seedTwoHunkDraftReviewFixture(t, db)
	svc := newTestService(t, db)
	file := prepareSkillFile(t, svc, db)
	if file.HunkCount != 2 {
		t.Fatalf("hunk count = %d, want 2; lines = %#v", file.HunkCount, file.DiffEntryLines)
	}
	hunkIDs := collectHunkIDs(file.DiffEntryLines)
	if len(hunkIDs) != 2 {
		t.Fatalf("hunk IDs = %#v, want 2; lines = %#v", hunkIDs, file.DiffEntryLines)
	}
	firstHunkID := hunkIDs[0]
	secondHunkID := hunkIDs[1]

	action, err := svc.Action(context.Background(), ActionRequest{
		SkillID:               "skill1",
		UserID:                "user_001",
		ReviewID:              file.ReviewID,
		ExpectedReviewVersion: file.ReviewVersion,
		Items: []ActionItem{{
			Path:     "SKILL.md",
			HunkID:   firstHunkID,
			Decision: decisionAccepted,
		}},
	})
	if err != nil {
		t.Fatalf("Action returned error: %v", err)
	}

	remaining, err := svc.PrepareFile(context.Background(), PrepareFileRequest{
		SkillID: "skill1",
		UserID:  "user_001",
		File:    file,
	})
	if err != nil {
		t.Fatalf("PrepareFile returned error: %v", err)
	}
	if remaining.HunkCount != 2 || remaining.AcceptedCount != 1 || remaining.PendingCount != 1 || remaining.RejectedCount != 0 {
		t.Fatalf("unexpected counts after accept: hunk=%d accepted=%d pending=%d rejected=%d", remaining.HunkCount, remaining.AcceptedCount, remaining.PendingCount, remaining.RejectedCount)
	}
	firstResolved := hunkBlock(remaining.DiffEntryLines, firstHunkID)
	if len(firstResolved) == 0 {
		t.Fatalf("accepted hunk %q should still be returned as resolved context: %#v", firstHunkID, remaining.DiffEntryLines)
	}
	if hasChangedLine(firstResolved) {
		t.Fatalf("accepted hunk %q should not contain diff lines: %#v", firstHunkID, firstResolved)
	}
	secondPending := hunkBlock(remaining.DiffEntryLines, secondHunkID)
	if len(secondPending) == 0 {
		t.Fatalf("pending hunk %q missing from diff lines: %#v", secondHunkID, remaining.DiffEntryLines)
	}
	if !hasChangedLine(secondPending) {
		t.Fatalf("pending hunk %q should keep diff lines: %#v", secondHunkID, secondPending)
	}

	action, err = svc.Action(context.Background(), ActionRequest{
		SkillID:               "skill1",
		UserID:                "user_001",
		ReviewID:              file.ReviewID,
		ExpectedReviewVersion: action.ReviewVersion,
		Items: []ActionItem{{
			Path:     "SKILL.md",
			HunkID:   secondHunkID,
			Decision: decisionRejected,
		}},
	})
	if err != nil {
		t.Fatalf("second Action returned error: %v", err)
	}

	remaining, err = svc.PrepareFile(context.Background(), PrepareFileRequest{
		SkillID: "skill1",
		UserID:  "user_001",
		File:    file,
	})
	if err != nil {
		t.Fatalf("PrepareFile after reject returned error: %v", err)
	}
	if remaining.HunkCount != 2 || remaining.AcceptedCount != 1 || remaining.PendingCount != 0 || remaining.RejectedCount != 1 {
		t.Fatalf("unexpected counts after resolving all: hunk=%d accepted=%d pending=%d rejected=%d", remaining.HunkCount, remaining.AcceptedCount, remaining.PendingCount, remaining.RejectedCount)
	}
	firstResolved = hunkBlock(remaining.DiffEntryLines, firstHunkID)
	secondResolved := hunkBlock(remaining.DiffEntryLines, secondHunkID)
	if len(firstResolved) == 0 || len(secondResolved) == 0 {
		t.Fatalf("resolved hunks should remain visible as context, got %#v", remaining.DiffEntryLines)
	}
	if hasChangedLine(firstResolved) || hasChangedLine(secondResolved) {
		t.Fatalf("resolved hunks should not contain diff lines, first=%#v second=%#v", firstResolved, secondResolved)
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
	if string(blob.Content) != testutil.SkillMD("论文精读", "用于阅读和总结论文的技能") {
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

func TestCommit_AcceptedFrontmatterSynchronizesPublishedMetadata(t *testing.T) {
	db := testutil.NewTestDB(t)
	testutil.SeedSkillWithRevision(t, db, "skill1", "rev1")
	testutil.SeedTextBlob(t, db, "h_renamed_skill", testutil.SkillMD("论文精读 Pro", "专业论文阅读技能"))
	testutil.SeedDraftEntry(t, db, "skill1", "SKILL.md", "upsert", "file", "h_renamed_skill")
	svc := newTestService(t, db)
	file := prepareSkillFile(t, svc, db)
	hunkIDs := collectHunkIDs(file.DiffEntryLines)
	items := make([]ActionItem, 0, len(hunkIDs))
	for _, hunkID := range hunkIDs {
		items = append(items, ActionItem{Path: "SKILL.md", HunkID: hunkID, Decision: decisionAccepted})
	}
	action, err := svc.Action(context.Background(), ActionRequest{
		SkillID:               "skill1",
		UserID:                "user_001",
		ReviewID:              file.ReviewID,
		ExpectedReviewVersion: file.ReviewVersion,
		Items:                 items,
	})
	if err != nil {
		t.Fatalf("Action returned error: %v", err)
	}
	if _, err := svc.Commit(context.Background(), CommitRequest{
		SkillID:               "skill1",
		UserID:                "user_001",
		ReviewID:              file.ReviewID,
		ExpectedReviewVersion: action.ReviewVersion,
	}); err != nil {
		t.Fatalf("Commit returned error: %v", err)
	}
	var skill testutil.SkillRow
	if err := db.Where("id = ?", "skill1").Take(&skill).Error; err != nil {
		t.Fatalf("query published skill: %v", err)
	}
	if skill.SkillName != "论文精读 Pro" || skill.Description != "专业论文阅读技能" || skill.RelativeRoot != "research/论文精读 Pro" {
		t.Fatalf("published metadata not synchronized: %#v", skill)
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
	testutil.SeedTextBlob(t, db, "h_skill_draft", testutil.SkillMD("论文精读", "用于阅读和总结论文的技能")+"\n帮用户改写文章。\n")
	testutil.SeedDraftEntry(t, db, "skill1", "SKILL.md", "upsert", "file", "h_skill_draft")
}

func seedTwoHunkDraftReviewFixture(t *testing.T, db *testutil.TestDB) {
	t.Helper()
	testutil.SeedSkillWithRevision(t, db, "skill1", "rev1")
	frontmatter := "---\nname: 论文精读\ndescription: 用于阅读和总结论文的技能\n---\n"
	base := frontmatter + "title\nold one\nkeep\nold two\nend\n"
	draft := frontmatter + "title\nnew one\nkeep\nnew two\nend\n"
	testutil.SeedTextBlob(t, db, "h_skill_base_two_hunks", base)
	testutil.SeedTextBlob(t, db, "h_skill_draft_two_hunks", draft)
	if err := db.Model(&testutil.SkillRevisionEntryRow{}).
		Where("revision_id = ? AND path = ?", "rev1", "SKILL.md").
		Updates(map[string]any{"blob_hash": "h_skill_base_two_hunks", "size": int64(len([]byte(base)))}).Error; err != nil {
		t.Fatalf("update base fixture: %v", err)
	}
	testutil.SeedDraftEntry(t, db, "skill1", "SKILL.md", "upsert", "file", "h_skill_draft_two_hunks")
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

func hunkBlock(lines []skilldiff.DiffEntryLine, hunkID string) []skilldiff.DiffEntryLine {
	for i, line := range lines {
		if line.Type != "HUNK" || line.HunkID != hunkID {
			continue
		}
		end := len(lines)
		for j := i + 1; j < len(lines); j++ {
			if lines[j].Type == "HUNK" {
				end = j
				break
			}
		}
		return lines[i:end]
	}
	return nil
}

func hasChangedLine(lines []skilldiff.DiffEntryLine) bool {
	for _, line := range lines {
		if line.Type == "ADDITION" || line.Type == "DELETION" {
			return true
		}
	}
	return false
}

func collectHunkIDs(lines []skilldiff.DiffEntryLine) []string {
	ids := []string{}
	for _, line := range lines {
		if line.Type == "HUNK" {
			ids = append(ids, line.HunkID)
		}
	}
	return ids
}
