package revision

import (
	"context"
	"testing"

	"lazymind/core/skillv2/testutil"
)

func TestRevisionListDetailAndTree(t *testing.T) {
	db := testutil.NewTestDB(t)
	testutil.SeedSkillWithRevision(t, db, "skill1", "rev1")
	seedSecondRevision(t, db, "skill1", "rev1", "rev2")
	testutil.SeedTextBlob(t, db, "h_a", "# A\n")
	testutil.SeedRevisionEntry(t, db, "rev2", "references", "dir", "", "directory")
	testutil.SeedRevisionEntry(t, db, "rev2", "references/a.md", "file", "h_a", "markdown")
	service := NewService(ServiceDeps{DB: db.DB, BlobStore: NewBlobStore(db.DB, NewLocalObjectStore(t.TempDir()))})

	list, err := service.ListRevisions(context.Background(), ListRevisionsRequest{SkillID: "skill1", UserID: "user_001"})
	if err != nil {
		t.Fatalf("ListRevisions returned error: %v", err)
	}
	if len(list.Items) != 2 {
		t.Fatalf("ListRevisions returned %d items, want 2", len(list.Items))
	}
	if !list.Items[0].IsHead || list.Items[0].RevisionID != "rev2" || list.Items[1].IsHead {
		t.Fatalf("revision list head markers are incorrect: %#v", list.Items)
	}
	for _, item := range list.Items {
		if item.RevisionNo == 0 || item.ChangeSource == "" || item.CreatedAt.IsZero() {
			t.Fatalf("revision list item missing metadata: %#v", item)
		}
		if item.FileContent != "" {
			t.Fatalf("revision list item returned file content: %#v", item)
		}
	}

	detail, err := service.GetRevision(context.Background(), GetRevisionRequest{SkillID: "skill1", UserID: "user_001", RevisionID: "rev1"})
	if err != nil {
		t.Fatalf("GetRevision returned error: %v", err)
	}
	if detail.ID != "rev1" || detail.FileContent != "" || detail.IsHead {
		t.Fatalf("unexpected revision detail: %#v", detail)
	}
	tree, err := service.GetRevisionTree(context.Background(), GetRevisionTreeRequest{SkillID: "skill1", UserID: "user_001", RevisionID: "rev1"})
	if err != nil {
		t.Fatalf("GetRevisionTree returned error: %v", err)
	}
	if tree.Path != "" || !tree.HasPath("SKILL.md") || tree.HasPath("references/a.md") {
		t.Fatalf("revision tree should be rev1 only, got %#v", tree)
	}
}

func TestDraftStatus_ReturnsTaskConversationAndOverlayState(t *testing.T) {
	db := testutil.NewTestDB(t)
	testutil.SeedSkillWithRevision(t, db, "skill1", "rev1")
	if err := db.Model(&testutil.SkillDraftRow{}).Where("skill_id = ?", "skill1").Updates(map[string]any{
		"task_id":         "task1",
		"conversation_id": "conv1",
		"version":         3,
	}).Error; err != nil {
		t.Fatalf("update draft: %v", err)
	}
	testutil.SeedDraftEntry(t, db, "skill1", "SKILL.md", "upsert", "file", "h1")
	testutil.SeedDraftEntry(t, db, "skill1", "references/a.md", "delete", "", "")
	service := NewService(ServiceDeps{DB: db.DB, BlobStore: NewBlobStore(db.DB, NewLocalObjectStore(t.TempDir()))})

	status, err := service.DraftStatus(context.Background(), DraftStatusRequest{SkillID: "skill1", UserID: "user_001"})
	if err != nil {
		t.Fatalf("DraftStatus returned error: %v", err)
	}
	if status.BaseRevisionID != "rev1" || status.TaskID != "task1" || status.ConversationID != "conv1" || status.DraftVersion != 3 || !status.HasUncommittedDraft || status.OverlayCount != 2 {
		t.Fatalf("unexpected draft status: %#v", status)
	}
}
