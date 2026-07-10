package revision

import (
	"context"
	"testing"

	"lazymind/core/skillv2/testutil"
)

func TestRevisionDelete_PhysicallyDeletesHistoryRevision(t *testing.T) {
	db := testutil.NewTestDB(t)
	testutil.SeedSkillWithRevision(t, db, "skill1", "rev1")
	seedSecondRevision(t, db, "skill1", "rev1", "rev2")
	service := NewService(ServiceDeps{DB: db.DB, BlobStore: NewBlobStore(db.DB, NewLocalObjectStore(t.TempDir()))})

	if err := service.DeleteRevision(context.Background(), DeleteRevisionRequest{SkillID: "skill1", UserID: "user_001", RevisionID: "rev1"}); err != nil {
		t.Fatalf("DeleteRevision returned error: %v", err)
	}
	if got := testutil.CountRows(t, db, "skill_revisions", "id = ?", "rev1"); got != 0 {
		t.Fatalf("rev1 count = %d, want 0", got)
	}
	if got := testutil.CountRows(t, db, "skill_revision_entries", "revision_id = ?", "rev1"); got != 0 {
		t.Fatalf("rev1 entries count = %d, want 0", got)
	}
}

func TestRevisionDelete_DoesNotDeleteSharedBlob(t *testing.T) {
	db := testutil.NewTestDB(t)
	testutil.SeedSkillWithRevision(t, db, "skill1", "rev1")
	seedSecondRevision(t, db, "skill1", "rev1", "rev2")
	testutil.SeedTextBlob(t, db, "h_shared", "# shared\n")
	testutil.SeedRevisionEntry(t, db, "rev1", "shared.md", "file", "h_shared", "markdown")
	testutil.SeedRevisionEntry(t, db, "rev2", "shared.md", "file", "h_shared", "markdown")
	service := NewService(ServiceDeps{DB: db.DB, BlobStore: NewBlobStore(db.DB, NewLocalObjectStore(t.TempDir()))})

	if err := service.DeleteRevision(context.Background(), DeleteRevisionRequest{SkillID: "skill1", UserID: "user_001", RevisionID: "rev1"}); err != nil {
		t.Fatalf("DeleteRevision returned error: %v", err)
	}
	if got := testutil.CountRows(t, db, "skill_blobs", "hash = ?", "h_shared"); got != 1 {
		t.Fatalf("shared blob count = %d, want 1", got)
	}
	file, err := service.ReadRevisionFile(context.Background(), ReadRevisionFileRequest{SkillID: "skill1", RevisionID: "rev2", Path: "shared.md"})
	if err != nil {
		t.Fatalf("ReadRevisionFile returned error: %v", err)
	}
	if file.Content == "" {
		t.Fatal("shared file content is empty")
	}
}

func TestRevisionDelete_FailureRollsBack(t *testing.T) {
	db := testutil.NewTestDB(t)
	testutil.SeedSkillWithRevision(t, db, "skill1", "rev1")
	seedSecondRevision(t, db, "skill1", "rev1", "rev2")
	service := NewService(ServiceDeps{DB: db.DB, BlobStore: NewFailingBlobStore("delete failed")})

	if err := service.DeleteRevision(context.Background(), DeleteRevisionRequest{SkillID: "skill1", UserID: "user_001", RevisionID: "rev1"}); err == nil {
		t.Fatal("DeleteRevision succeeded despite blob delete failure")
	}
	if got := testutil.CountRows(t, db, "skill_revisions", "id = ?", "rev1"); got != 1 {
		t.Fatalf("rev1 count = %d, want 1", got)
	}
	if got := testutil.CountRows(t, db, "skill_revision_entries", "revision_id = ?", "rev1"); got == 0 {
		t.Fatal("rev1 entries were partially deleted")
	}
}

func TestRevisionDelete_ConcurrentReferenceKeepsBlob(t *testing.T) {
	db := testutil.NewTestDB(t)
	testutil.SeedSkillWithRevision(t, db, "skill1", "rev1")
	seedSecondRevision(t, db, "skill1", "rev1", "rev2")
	testutil.SeedTextBlob(t, db, "h_shared", "# shared\n")
	testutil.SeedRevisionEntry(t, db, "rev1", "shared.md", "file", "h_shared", "markdown")
	testutil.SeedDraftEntry(t, db, "skill1", "shared-copy.md", "upsert", "file", "h_shared")
	service := NewService(ServiceDeps{DB: db.DB, BlobStore: NewBlobStore(db.DB, NewLocalObjectStore(t.TempDir()))})

	if err := service.DeleteRevision(context.Background(), DeleteRevisionRequest{SkillID: "skill1", UserID: "user_001", RevisionID: "rev1"}); err != nil {
		t.Fatalf("DeleteRevision returned error: %v", err)
	}
	if got := testutil.CountRows(t, db, "skill_blobs", "hash = ?", "h_shared"); got != 1 {
		t.Fatalf("blob referenced by draft count = %d, want 1", got)
	}
}
