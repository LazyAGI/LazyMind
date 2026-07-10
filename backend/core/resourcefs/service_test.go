package resourcefs

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"lazymind/core/common/orm"
)

func newResourceFSTestDB(t *testing.T) *orm.DB {
	t.Helper()
	db, err := orm.Connect(orm.DriverSQLite, filepath.Join(t.TempDir(), "resourcefs.db"))
	if err != nil {
		t.Fatalf("connect db: %v", err)
	}
	if err := db.AutoMigrate(
		&orm.SystemMemory{},
		&orm.SystemUserPreference{},
		&orm.PersonalResource{},
		&orm.PersonalResourceBlob{},
		&orm.PersonalResourceRevision{},
		&orm.PersonalResourceDraft{},
		&orm.PersonalResourceReviewSession{},
		&orm.PersonalResourceReviewActionBatch{},
		&orm.PersonalResourceReviewActionItem{},
	); err != nil {
		t.Fatalf("auto migrate: %v", err)
	}
	return db
}

func TestServiceDraftCommitRevisionRollback(t *testing.T) {
	db := newResourceFSTestDB(t)
	service := NewService(ServiceDeps{DB: db.DB})
	ref := ResourceRef{UserID: "u1", ResourceType: ResourceTypeMemory}

	state, err := service.EnsureResource(context.Background(), ref, "initial memory")
	if err != nil {
		t.Fatalf("EnsureResource returned error: %v", err)
	}
	if state.Path != MemoryPath || state.HeadRevisionID == "" || state.DraftVersion != 1 {
		t.Fatalf("unexpected initial state: %#v", state)
	}

	head, err := service.ReadFile(context.Background(), ReadFileRequest{Ref: ref, RefType: FileRefHead})
	if err != nil {
		t.Fatalf("ReadFile head returned error: %v", err)
	}
	if head.Content != "initial memory" || head.RevisionNo != 1 {
		t.Fatalf("unexpected head: %#v", head)
	}

	draft, err := service.WriteDraft(context.Background(), WriteDraftRequest{
		Ref:                  ref,
		Content:              "updated memory",
		ExpectedDraftVersion: state.DraftVersion,
		UpdatedBy:            "u1",
	})
	if err != nil {
		t.Fatalf("WriteDraft returned error: %v", err)
	}
	if draft.DraftVersion != 2 || draft.DraftStatus != "pending_confirm" {
		t.Fatalf("unexpected draft response: %#v", draft)
	}

	preview, err := service.DraftPreview(context.Background(), DraftPreviewRequest{Ref: ref})
	if err != nil {
		t.Fatalf("DraftPreview returned error: %v", err)
	}
	if preview.HeadContent != "initial memory" || preview.DraftContent != "updated memory" || preview.Diff.HunkCount == 0 {
		t.Fatalf("unexpected preview: %#v", preview)
	}

	commit, err := service.CommitDraft(context.Background(), CommitDraftRequest{
		Ref:                  ref,
		Message:              "accept draft",
		ExpectedDraftVersion: draft.DraftVersion,
		CreatedBy:            "u1",
	})
	if err != nil {
		t.Fatalf("CommitDraft returned error: %v", err)
	}
	if commit.Content != "updated memory" || commit.RevisionNo != 2 {
		t.Fatalf("unexpected commit: %#v", commit)
	}

	revisions, err := service.ListRevisions(context.Background(), ListRevisionsRequest{Ref: ref})
	if err != nil {
		t.Fatalf("ListRevisions returned error: %v", err)
	}
	if len(revisions.Items) != 2 {
		t.Fatalf("expected 2 revisions, got %#v", revisions.Items)
	}
	initialRevisionID := ""
	for _, item := range revisions.Items {
		if item.RevisionNo == 1 {
			initialRevisionID = item.ID
		}
	}
	if initialRevisionID == "" {
		t.Fatalf("initial revision missing: %#v", revisions.Items)
	}

	rollback, err := service.Rollback(context.Background(), RollbackRequest{
		Ref:                    ref,
		RevisionID:             initialRevisionID,
		ExpectedHeadRevisionID: commit.RevisionID,
		CreatedBy:              "u1",
	})
	if err != nil {
		t.Fatalf("Rollback returned error: %v", err)
	}
	if rollback.Content != "initial memory" || rollback.RevisionNo != 3 {
		t.Fatalf("unexpected rollback: %#v", rollback)
	}

	rolledBackHead, err := service.ReadFile(context.Background(), ReadFileRequest{Ref: ref, RefType: FileRefHead})
	if err != nil {
		t.Fatalf("ReadFile rolled back head returned error: %v", err)
	}
	if rolledBackHead.Content != "initial memory" || rolledBackHead.RevisionNo != 3 {
		t.Fatalf("unexpected rolled back head: %#v", rolledBackHead)
	}
}

func TestReviewActionRejectAndUndoUpdatesDraftOnly(t *testing.T) {
	db := newResourceFSTestDB(t)
	service := NewService(ServiceDeps{DB: db.DB})
	ref := ResourceRef{UserID: "u1", ResourceType: ResourceTypeMemory}
	ctx := context.Background()

	state, err := service.EnsureResource(ctx, ref, "line 1\nline 2\n")
	if err != nil {
		t.Fatalf("EnsureResource returned error: %v", err)
	}
	draft, err := service.WriteDraft(ctx, WriteDraftRequest{
		Ref:                  ref,
		Content:              "line 1\nline two\n",
		ExpectedDraftVersion: state.DraftVersion,
		UpdatedBy:            "u1",
	})
	if err != nil {
		t.Fatalf("WriteDraft returned error: %v", err)
	}
	preview, err := service.DraftPreview(ctx, DraftPreviewRequest{Ref: ref})
	if err != nil {
		t.Fatalf("DraftPreview returned error: %v", err)
	}
	hunkID := firstReviewHunkID(t, preview)
	if preview.ReviewID == "" || preview.ReviewVersion != 1 || preview.PendingCount != 1 {
		t.Fatalf("unexpected review preview: %#v", preview)
	}

	action, err := service.Action(ctx, ReviewActionRequest{
		Ref:                   ref,
		ReviewID:              preview.ReviewID,
		ExpectedReviewVersion: preview.ReviewVersion,
		UpdatedBy:             "u1",
		Items: []ReviewActionItem{{
			HunkID:   hunkID,
			Decision: "reject",
		}},
	})
	if err != nil {
		t.Fatalf("Action returned error: %v", err)
	}
	if action.DraftContent != "line 1\nline 2\n" || action.DraftVersion != draft.DraftVersion+1 || !action.CanUndo {
		t.Fatalf("unexpected reject action response: %#v", action)
	}
	afterReject, err := service.ReadFile(ctx, ReadFileRequest{Ref: ref, RefType: FileRefDraft})
	if err != nil {
		t.Fatalf("ReadFile draft after reject returned error: %v", err)
	}
	if afterReject.Content != "line 1\nline 2\n" {
		t.Fatalf("reject should update draft content only, got %q", afterReject.Content)
	}
	head, err := service.ReadFile(ctx, ReadFileRequest{Ref: ref, RefType: FileRefHead})
	if err != nil {
		t.Fatalf("ReadFile head after reject returned error: %v", err)
	}
	if head.Content != "line 1\nline 2\n" || head.RevisionNo != 1 {
		t.Fatalf("reject should not commit head, got %#v", head)
	}

	undo, err := service.Undo(ctx, ReviewUndoRequest{
		Ref:                   ref,
		ReviewID:              preview.ReviewID,
		ExpectedReviewVersion: action.ReviewVersion,
		UpdatedBy:             "u1",
	})
	if err != nil {
		t.Fatalf("Undo returned error: %v", err)
	}
	if undo.DraftContent != "line 1\nline two\n" || undo.CanUndo {
		t.Fatalf("unexpected undo response: %#v", undo)
	}
	if len(undo.RestoredActions) != 1 || undo.RestoredActions[0].Decision != decisionPending {
		t.Fatalf("unexpected restored actions: %#v", undo.RestoredActions)
	}
	afterUndo, err := service.ReadFile(ctx, ReadFileRequest{Ref: ref, RefType: FileRefDraft})
	if err != nil {
		t.Fatalf("ReadFile draft after undo returned error: %v", err)
	}
	if afterUndo.Content != "line 1\nline two\n" {
		t.Fatalf("undo should restore draft content, got %q", afterUndo.Content)
	}
}

func TestReviewActionConflictsWhenDraftChangesAfterPreview(t *testing.T) {
	db := newResourceFSTestDB(t)
	service := NewService(ServiceDeps{DB: db.DB})
	ref := ResourceRef{UserID: "u1", ResourceType: ResourceTypeMemory}
	ctx := context.Background()

	state, err := service.EnsureResource(ctx, ref, "base\n")
	if err != nil {
		t.Fatalf("EnsureResource returned error: %v", err)
	}
	draft, err := service.WriteDraft(ctx, WriteDraftRequest{
		Ref:                  ref,
		Content:              "draft one\n",
		ExpectedDraftVersion: state.DraftVersion,
		UpdatedBy:            "u1",
	})
	if err != nil {
		t.Fatalf("WriteDraft returned error: %v", err)
	}
	preview, err := service.DraftPreview(ctx, DraftPreviewRequest{Ref: ref})
	if err != nil {
		t.Fatalf("DraftPreview returned error: %v", err)
	}
	hunkID := firstReviewHunkID(t, preview)
	if _, err := service.WriteDraft(ctx, WriteDraftRequest{
		Ref:                  ref,
		Content:              "draft two\n",
		ExpectedDraftVersion: draft.DraftVersion,
		UpdatedBy:            "u1",
	}); err != nil {
		t.Fatalf("second WriteDraft returned error: %v", err)
	}

	_, err = service.Action(ctx, ReviewActionRequest{
		Ref:                   ref,
		ReviewID:              preview.ReviewID,
		ExpectedReviewVersion: preview.ReviewVersion,
		UpdatedBy:             "u1",
		Items: []ReviewActionItem{{
			HunkID:   hunkID,
			Decision: decisionAccepted,
		}},
	})
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("Action error = %v, want ErrConflict", err)
	}
}

func TestResourceTypeForPathNormalizesLeadingSlash(t *testing.T) {
	resourceType, err := ResourceTypeForPath("/memory/user.md")
	if err != nil {
		t.Fatalf("ResourceTypeForPath returned error: %v", err)
	}
	if resourceType != ResourceTypeUserPreference {
		t.Fatalf("expected user_preference, got %q", resourceType)
	}
}

func TestSyncBusinessColumnsParsesPreferenceFileWithMissingMetadata(t *testing.T) {
	db := newResourceFSTestDB(t)
	row := orm.SystemUserPreference{
		ID:            "preference-1",
		UserID:        "u1",
		Content:       "old",
		AgentPersona:  "old agent",
		PreferredName: "old name",
		ResponseStyle: "old style",
		Version:       1,
	}
	if err := db.Create(&row).Error; err != nil {
		t.Fatalf("create preference: %v", err)
	}
	content := "---\nagent_persona: new agent\n---\n\nnew body"
	err := syncBusinessColumns(context.Background(), db.DB, ResourceRef{UserID: "u1", ResourceType: ResourceTypeUserPreference}, content, 2)
	if err != nil {
		t.Fatalf("syncBusinessColumns returned error: %v", err)
	}
	var updated orm.SystemUserPreference
	if err := db.Where("id = ?", row.ID).Take(&updated).Error; err != nil {
		t.Fatalf("query updated preference: %v", err)
	}
	if updated.Content != "new body" || updated.AgentPersona != "new agent" || updated.PreferredName != "" || updated.ResponseStyle != "" || updated.Version != 2 {
		t.Fatalf("unexpected synced preference: %#v", updated)
	}
}

func firstReviewHunkID(t *testing.T, preview DraftPreviewResponse) string {
	t.Helper()
	for _, line := range preview.Diff.DiffEntryLines {
		if line.Type == "HUNK" && line.HunkID != "" {
			return line.HunkID
		}
	}
	t.Fatalf("preview missing review hunk: %#v", preview.Diff.DiffEntryLines)
	return ""
}
