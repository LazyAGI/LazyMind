package service

import (
	"context"
	"testing"

	"gorm.io/gorm"
)

func TestListSkills_IncludesCreateDraftWithoutHead(t *testing.T) {
	db := newSkillV2TestDB(t)
	seedCreateDraftSkillForServiceTest(t, db, "skill-create")
	svc := NewSkillService(SkillServiceDeps{DB: db, BlobStore: NewBlobStore(db, NewLocalObjectStore(t.TempDir())), Clock: fixedClock()})

	list, err := svc.ListSkills(context.Background(), ListSkillsRequest{UserID: "user_001"})
	if err != nil {
		t.Fatalf("ListSkills returned error: %v", err)
	}
	if len(list.Items) != 1 {
		t.Fatalf("ListSkills returned %d items, want 1", len(list.Items))
	}
	item := list.Items[0]
	if item.HeadRevisionID != "" || item.Draft.Type != "create" || item.Draft.Status != "pending_confirm" || !item.Draft.HasUncommittedDraft {
		t.Fatalf("create draft summary = %#v", item)
	}
	file, err := svc.ReadFile(context.Background(), FileRef{SkillID: "skill-create", RefType: "head", Path: "SKILL.md"})
	if err != nil || file.Content != "# New Skill\n" {
		t.Fatalf("create draft SKILL.md = %#v, err=%v", file, err)
	}
}

func TestDiscardDraft_DeletesCreateDraftSkill(t *testing.T) {
	db := newSkillV2TestDB(t)
	seedCreateDraftSkillForServiceTest(t, db, "skill-create")
	svc := NewSkillService(SkillServiceDeps{DB: db, BlobStore: NewBlobStore(db, NewLocalObjectStore(t.TempDir())), Clock: fixedClock()})

	if _, err := svc.DiscardDraft(context.Background(), DiscardDraftRequest{SkillID: "skill-create", UserID: "user_001"}); err != nil {
		t.Fatalf("DiscardDraft returned error: %v", err)
	}
	for _, row := range []struct {
		table  string
		column string
	}{
		{table: "skills", column: "id"},
		{table: "skill_drafts", column: "skill_id"},
		{table: "skill_draft_entries", column: "skill_id"},
		{table: "skill_blobs", column: "hash"},
	} {
		value := "skill-create"
		if row.table == "skill_blobs" {
			value = "h_skill_create"
		}
		if got := countRows(t, db, row.table, row.column+" = ?", value); got != 0 {
			t.Fatalf("%s row count = %d, want 0", row.table, got)
		}
	}
}

func seedCreateDraftSkillForServiceTest(t *testing.T, db *gorm.DB, skillID string) {
	t.Helper()
	now := fixedClock().Now()
	if err := db.Create(&testSkillV2SkillRow{
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
	}).Error; err != nil {
		t.Fatalf("seed create draft skill: %v", err)
	}
	if err := db.Create(&testSkillV2DraftRow{
		SkillID:     skillID,
		DraftStatus: "pending_confirm",
		TaskID:      "task1",
		Version:     1,
		CreatedAt:   now,
		UpdatedAt:   now,
	}).Error; err != nil {
		t.Fatalf("seed create draft: %v", err)
	}
	hash := "h_skill_create"
	content := []byte("# New Skill\n")
	if err := db.Create(&testSkillV2BlobRow{
		Hash:           hash,
		Size:           int64(len(content)),
		Mime:           "text/markdown",
		FileType:       "markdown",
		StorageBackend: "postgres",
		Content:        content,
		CreatedAt:      now,
	}).Error; err != nil {
		t.Fatalf("seed create draft blob: %v", err)
	}
	if err := db.Create(&testSkillV2DraftEntryRow{
		SkillID:   skillID,
		Path:      "SKILL.md",
		Op:        "upsert",
		EntryType: "file",
		BlobHash:  &hash,
		Size:      int64(len(content)),
		Mime:      "text/markdown",
		FileType:  "markdown",
		Mode:      0o644,
		UpdatedAt: now,
	}).Error; err != nil {
		t.Fatalf("seed create draft entry: %v", err)
	}
}
