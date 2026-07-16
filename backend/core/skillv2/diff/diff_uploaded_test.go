package diff

import (
	"context"
	"path/filepath"
	"testing"

	"lazymind/core/skillv2/testutil"
)

func TestDiffUploadedVsRevision_ForImportPreview(t *testing.T) {
	db := testutil.NewTestDB(t)
	testutil.SeedSkillWithRevision(t, db, "skill1", "rev1")
	testutil.SeedTextBlob(t, db, "h_a", "# A\n")
	testutil.SeedRevisionEntry(t, db, "rev1", "references/a.md", "file", "h_a", "markdown")
	zipPath := filepath.Join(t.TempDir(), "new.zip")
	testutil.WriteSkillZip(t, zipPath, map[string][]byte{
		"SKILL.md":        []byte("# 论文精读 v2\n"),
		"references/b.md": []byte("# B\n"),
	})
	uploads := testutil.NewFakeUploadStore()
	uploads.Put(testutil.UploadSession{UploadID: "upload_new_zip_1", OwnerUserID: "user_001", State: "completed", StoredPath: zipPath, Filename: "new.zip"})
	resolver := NewRefResolver(RefResolverDeps{DB: db.DB, UploadStore: uploads})
	service := NewService(ServiceDeps{})

	oldFS, newFS, err := resolver.ResolvePair(context.Background(), ResolvePairRequest{
		UserID: "user_001",
		Old:    DiffRef{Type: "revision", SkillID: "skill1", RevisionID: "rev1"},
		New:    DiffRef{Type: "uploaded", UploadID: "upload_new_zip_1"},
	})
	if err != nil {
		t.Fatalf("ResolvePair returned error: %v", err)
	}
	result, err := service.Compare(context.Background(), oldFS, newFS, DiffOptions{})
	if err != nil {
		t.Fatalf("Compare returned error: %v", err)
	}
	assertDiffStatus(t, result, "SKILL.md", "modified")
	assertDiffStatus(t, result, "references/a.md", "deleted")
	assertDiffStatus(t, result, "references/b.md", "added")
	if result.CacheWritten {
		t.Fatal("uploaded diff wrote cache")
	}
	if got := testutil.CountRows(t, db, "skill_revisions", "skill_id = ?", "skill1"); got != 1 {
		t.Fatalf("revision count = %d, want 1", got)
	}
}

func TestDiffUploadedVsRevision_StripsSingleTopLevelDirectory(t *testing.T) {
	db := testutil.NewTestDB(t)
	testutil.SeedSkillWithRevision(t, db, "skill1", "rev1")
	zipPath := filepath.Join(t.TempDir(), "wrapped.zip")
	testutil.WriteSkillZip(t, zipPath, map[string][]byte{
		"openclaw-openclaw-changelog-update/SKILL.md":        []byte("# 论文精读 v2\n"),
		"openclaw-openclaw-changelog-update/references/b.md": []byte("# B\n"),
	})
	uploads := testutil.NewFakeUploadStore()
	uploads.Put(testutil.UploadSession{UploadID: "upload_wrapped_zip", OwnerUserID: "user_001", State: "completed", StoredPath: zipPath, Filename: "wrapped.zip"})
	resolver := NewRefResolver(RefResolverDeps{DB: db.DB, UploadStore: uploads})
	service := NewService(ServiceDeps{})

	oldFS, newFS, err := resolver.ResolvePair(context.Background(), ResolvePairRequest{
		UserID: "user_001",
		Old:    DiffRef{Type: "revision", SkillID: "skill1", RevisionID: "rev1"},
		New:    DiffRef{Type: "uploaded", UploadID: "upload_wrapped_zip"},
	})
	if err != nil {
		t.Fatalf("ResolvePair returned error: %v", err)
	}
	result, err := service.Compare(context.Background(), oldFS, newFS, DiffOptions{})
	if err != nil {
		t.Fatalf("Compare returned error: %v", err)
	}
	assertDiffStatus(t, result, "SKILL.md", "modified")
	assertDiffStatus(t, result, "references/b.md", "added")
	for _, file := range result.Files {
		if file.Path == "openclaw-openclaw-changelog-update/SKILL.md" {
			t.Fatalf("diff kept wrapper directory path: %#v", file)
		}
	}
}

func TestDiffUploadedRefMismatch_ReturnsError(t *testing.T) {
	db := testutil.NewTestDB(t)
	uploads := testutil.NewFakeUploadStore()
	uploads.Put(testutil.UploadSession{UploadID: "upload_other_user_zip", OwnerUserID: "user_002", State: "completed", StoredPath: filepath.Join(t.TempDir(), "other.zip"), Filename: "other.zip"})
	resolver := NewRefResolver(RefResolverDeps{DB: db.DB, UploadStore: uploads})

	_, _, err := resolver.ResolvePair(context.Background(), ResolvePairRequest{
		UserID: "user_001",
		Old:    DiffRef{Type: "revision", SkillID: "skill1", RevisionID: "rev1"},
		New:    DiffRef{Type: "uploaded", UploadID: "upload_other_user_zip"},
	})
	if err == nil {
		t.Fatal("ResolvePair succeeded for uploaded ref owned by another user")
	}
}

func TestDiffDraftRef_MergesDraftOverlay(t *testing.T) {
	db := testutil.NewTestDB(t)
	testutil.SeedSkillWithRevision(t, db, "skill1", "rev1")
	testutil.SeedTextBlob(t, db, "h_a", "# A\n")
	testutil.SeedRevisionEntry(t, db, "rev1", "references/a.md", "file", "h_a", "markdown")
	testutil.SeedTextBlob(t, db, "h_skill_draft", "# 论文精读 v2\n")
	testutil.SeedTextBlob(t, db, "h_b", "# B\n")
	testutil.SeedDraftEntry(t, db, "skill1", "SKILL.md", "upsert", "file", "h_skill_draft")
	testutil.SeedDraftEntry(t, db, "skill1", "references/a.md", "delete", "", "")
	testutil.SeedDraftEntry(t, db, "skill1", "references/b.md", "upsert", "file", "h_b")

	resolver := NewRefResolver(RefResolverDeps{DB: db.DB})
	oldFS, newFS, err := resolver.ResolvePair(context.Background(), ResolvePairRequest{
		UserID: "user_001",
		Old:    DiffRef{Type: "head", SkillID: "skill1"},
		New:    DiffRef{Type: "draft", SkillID: "skill1"},
	})
	if err != nil {
		t.Fatalf("ResolvePair returned error: %v", err)
	}

	service := NewService(ServiceDeps{})
	result, err := service.Compare(context.Background(), oldFS, newFS, DiffOptions{})
	if err != nil {
		t.Fatalf("Compare returned error: %v", err)
	}
	assertDiffStatus(t, result, "SKILL.md", "modified")
	assertDiffStatus(t, result, "references/a.md", "deleted")
	assertDiffStatus(t, result, "references/b.md", "added")

	file, err := service.CompareFile(context.Background(), oldFS, newFS, DiffOptions{Path: "SKILL.md"})
	if err != nil {
		t.Fatalf("CompareFile returned error: %v", err)
	}
	assertLineTypes(t, file.DiffEntryLines, "DELETION", "ADDITION")
}

func TestDiffCreateDraft_UsesEmptyHead(t *testing.T) {
	db := testutil.NewTestDB(t)
	now := testutil.TimeFixture()
	testutil.MustCreate(t, db, &testutil.SkillRow{
		ID:                 "skill-create",
		OwnerUserID:        "user_001",
		CreateUserID:       "user_001",
		Category:           "research",
		SkillName:          "new-skill",
		Tags:               []byte("[]"),
		RelativeRoot:       "research/new-skill",
		SkillMDPath:        "SKILL.md",
		AutoEvoApplyStatus: "idle",
		UpdateStatus:       "up_to_date",
		CreatedAt:          now,
		UpdatedAt:          now,
	})
	testutil.MustCreate(t, db, &testutil.SkillDraftRow{
		SkillID:     "skill-create",
		DraftStatus: "pending_confirm",
		TaskID:      "session-1",
		Version:     1,
		CreatedAt:   now,
		UpdatedAt:   now,
	})
	testutil.SeedTextBlob(t, db, "h_create", "# New Skill\n")
	testutil.SeedDraftEntry(t, db, "skill-create", "SKILL.md", "upsert", "file", "h_create")

	resolver := NewRefResolver(RefResolverDeps{DB: db.DB})
	oldFS, newFS, err := resolver.ResolvePair(context.Background(), ResolvePairRequest{
		UserID: "user_001",
		Old:    DiffRef{Type: "head", SkillID: "skill-create"},
		New:    DiffRef{Type: "draft", SkillID: "skill-create"},
	})
	if err != nil {
		t.Fatalf("ResolvePair returned error: %v", err)
	}
	result, err := NewService(ServiceDeps{}).Compare(context.Background(), oldFS, newFS, DiffOptions{})
	if err != nil {
		t.Fatalf("Compare returned error: %v", err)
	}
	assertDiffStatus(t, result, "SKILL.md", "added")
}
