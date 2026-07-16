package service

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"gorm.io/gorm"
)

func TestPatchSkillMetadata_RewritesSkillFrontmatter(t *testing.T) {
	db := newSkillV2TestDB(t)
	seedSkillWithHeadRevision(t, db, "skill1", "rev1")
	svc := NewSkillService(SkillServiceDeps{
		DB:        db,
		BlobStore: NewBlobStore(db, NewLocalObjectStore(t.TempDir())),
		Clock:     fixedClock(),
	})

	resp, err := svc.PatchSkill(context.Background(), PatchSkillRequest{
		SkillID:     "skill1",
		UserID:      "user_001",
		Name:        stringPtr("论文精读 Pro"),
		Category:    stringPtr("research-updated"),
		Description: stringPtr("更新后的描述"),
		Tags:        &[]string{"paper", "deep"},
		AutoEvo:     boolPtr(true),
		IsEnabled:   boolPtr(false),
	})
	if err != nil {
		t.Fatalf("PatchSkill returned error: %v", err)
	}
	if resp.HeadRevisionID == "" || resp.HeadRevisionID == "rev1" {
		t.Fatalf("HeadRevisionID = %q, want a new revision", resp.HeadRevisionID)
	}

	var skill testSkillV2SkillRow
	if err := db.Where("id = ?", "skill1").Take(&skill).Error; err != nil {
		t.Fatalf("query skill: %v", err)
	}
	if skill.SkillName != "论文精读 Pro" || skill.Category != "research-updated" || skill.Description != "更新后的描述" {
		t.Fatalf("metadata not updated: %#v", skill)
	}
	assertJSONTags(t, skill.Tags, []string{"paper", "deep"})
	if !skill.AutoEvo {
		t.Fatal("auto_evo = false, want true")
	}
	if skill.IsEnabled {
		t.Fatal("is_enabled = true, want false")
	}
	if skill.HeadRevisionID == nil || *skill.HeadRevisionID != resp.HeadRevisionID {
		t.Fatalf("head_revision_id = %v, want %q", skill.HeadRevisionID, resp.HeadRevisionID)
	}
	if got := countRows(t, db, "skill_revisions", "skill_id = ?", "skill1"); got != 2 {
		t.Fatalf("skill_revisions count = %d, want 2", got)
	}
	var revision testSkillV2RevisionRow
	if err := db.Where("id = ?", resp.HeadRevisionID).Take(&revision).Error; err != nil {
		t.Fatalf("query metadata revision: %v", err)
	}
	if revision.ChangeSource != "metadata_update" {
		t.Fatalf("change_source = %q, want metadata_update", revision.ChangeSource)
	}
	file, err := svc.ReadFile(context.Background(), FileRef{SkillID: "skill1", RefType: "head", Path: "SKILL.md"})
	if err != nil {
		t.Fatalf("read updated SKILL.md: %v", err)
	}
	meta, ok, err := parseSkillMDMetadata(file.Content)
	if err != nil || !ok || meta.Name != "论文精读 Pro" || meta.Category != "research-updated" || meta.Description != "更新后的描述" {
		t.Fatalf("frontmatter not updated: meta=%#v content=%q", meta, file.Content)
	}
	if !strings.HasSuffix(file.Content, "# 论文精读\n") {
		t.Fatalf("body changed: %q", file.Content)
	}
}

func TestPatchSkillEnableCommitsDraftForDisabledDraftSkill(t *testing.T) {
	db := newSkillV2TestDB(t)
	now := fixedClock().Now()
	head := "rev-empty"
	if err := db.Create(&testSkillV2SkillRow{
		ID:                 "skill-draft",
		OwnerUserID:        "user_001",
		OwnerUserName:      "张三",
		CreateUserID:       "user_001",
		CreateUserName:     "张三",
		Category:           "research",
		SkillName:          "web-research",
		Tags:               []byte("[]"),
		RelativeRoot:       "research/web-research",
		SkillMDPath:        "SKILL.md",
		HeadRevisionID:     &head,
		Version:            1,
		AutoEvoApplyStatus: "idle",
		IsEnabled:          false,
		UpdateStatus:       "up_to_date",
		CreatedAt:          now,
		UpdatedAt:          now,
	}).Error; err != nil {
		t.Fatalf("seed draft skill: %v", err)
	}
	if err := db.Create(&testSkillV2RevisionRow{
		ID:           head,
		SkillID:      "skill-draft",
		RevisionNo:   1,
		TreeHash:     "empty-tree",
		ChangeSource: "create",
		CreatedAt:    now,
	}).Error; err != nil {
		t.Fatalf("seed empty revision: %v", err)
	}
	if err := db.Create(&testSkillV2DraftRow{
		SkillID:        "skill-draft",
		BaseRevisionID: &head,
		TaskID:         "task-ai",
		Version:        1,
		CreatedAt:      now,
		UpdatedAt:      now,
	}).Error; err != nil {
		t.Fatalf("seed draft row: %v", err)
	}
	seedSkillMDDraftEntry(t, db, "skill-draft", "h_draft_skill", "# Web research\n")
	svc := NewSkillService(SkillServiceDeps{
		DB:        db,
		BlobStore: NewBlobStore(db, NewLocalObjectStore(t.TempDir())),
		Clock:     fixedClock(),
	})

	resp, err := svc.PatchSkill(context.Background(), PatchSkillRequest{
		SkillID:   "skill-draft",
		UserID:    "user_001",
		IsEnabled: boolPtr(true),
	})
	if err != nil {
		t.Fatalf("PatchSkill returned error: %v", err)
	}
	if resp.HeadRevisionID == "" || resp.HeadRevisionID == head {
		t.Fatalf("HeadRevisionID = %q, want a new revision", resp.HeadRevisionID)
	}

	var skill testSkillV2SkillRow
	if err := db.Where("id = ?", "skill-draft").Take(&skill).Error; err != nil {
		t.Fatalf("query skill: %v", err)
	}
	if !skill.IsEnabled {
		t.Fatal("is_enabled = false, want true")
	}
	if skill.HeadRevisionID == nil || *skill.HeadRevisionID != resp.HeadRevisionID {
		t.Fatalf("head_revision_id = %v, want %q", skill.HeadRevisionID, resp.HeadRevisionID)
	}
	if skill.Version != 2 {
		t.Fatalf("version = %d, want 2", skill.Version)
	}
	if got := countRows(t, db, "skill_revision_entries", "revision_id = ? AND path = ? AND entry_type = ?", resp.HeadRevisionID, "SKILL.md", "file"); got != 1 {
		t.Fatalf("new SKILL.md revision entry count = %d, want 1", got)
	}
	if got := countRows(t, db, "skill_draft_entries", "skill_id = ?", "skill-draft"); got != 0 {
		t.Fatalf("draft entry count = %d, want 0", got)
	}
	var draft testSkillV2DraftRow
	if err := db.Where("skill_id = ?", "skill-draft").Take(&draft).Error; err != nil {
		t.Fatalf("query draft: %v", err)
	}
	if draft.BaseRevisionID == nil || *draft.BaseRevisionID != resp.HeadRevisionID {
		t.Fatalf("draft base_revision_id = %v, want %q", draft.BaseRevisionID, resp.HeadRevisionID)
	}
}

func TestPatchSkillEnabledTrueDoesNotCommitAlreadyEnabledDraft(t *testing.T) {
	db := newSkillV2TestDB(t)
	seedSkillWithHeadRevision(t, db, "skill1", "rev1")
	seedSkillMDDraftEntry(t, db, "skill1", "h_enabled_draft_skill", "# Draft update\n")
	svc := NewSkillService(SkillServiceDeps{
		DB:        db,
		BlobStore: NewBlobStore(db, NewLocalObjectStore(t.TempDir())),
		Clock:     fixedClock(),
	})

	resp, err := svc.PatchSkill(context.Background(), PatchSkillRequest{
		SkillID:   "skill1",
		UserID:    "user_001",
		AutoEvo:   boolPtr(true),
		IsEnabled: boolPtr(true),
	})
	if err != nil {
		t.Fatalf("PatchSkill returned error: %v", err)
	}
	if resp.HeadRevisionID != "rev1" {
		t.Fatalf("HeadRevisionID = %q, want rev1", resp.HeadRevisionID)
	}

	var skill testSkillV2SkillRow
	if err := db.Where("id = ?", "skill1").Take(&skill).Error; err != nil {
		t.Fatalf("query skill: %v", err)
	}
	if !skill.IsEnabled {
		t.Fatal("is_enabled = false, want true")
	}
	if skill.HeadRevisionID == nil || *skill.HeadRevisionID != "rev1" {
		t.Fatalf("head_revision_id changed to %v, want rev1", skill.HeadRevisionID)
	}
	if skill.Version != 1 {
		t.Fatalf("version = %d, want 1", skill.Version)
	}
	if got := countRows(t, db, "skill_revisions", "skill_id = ?", "skill1"); got != 1 {
		t.Fatalf("skill_revisions count = %d, want 1", got)
	}
	if got := countRows(t, db, "skill_draft_entries", "skill_id = ?", "skill1"); got != 1 {
		t.Fatalf("draft entry count = %d, want 1", got)
	}
}

func TestPatchSkillAutoEvoMarksPendingDraftAuto(t *testing.T) {
	db := newSkillV2TestDB(t)
	seedSkillWithHeadRevision(t, db, "skill1", "rev1")
	seedSkillMDDraftEntry(t, db, "skill1", "h_auto_pending_draft", "# Draft update\n")
	if err := db.Model(&testSkillV2DraftRow{}).Where("skill_id = ?", "skill1").Update("draft_status", skillDraftStatusPendingConfirm).Error; err != nil {
		t.Fatalf("mark draft pending: %v", err)
	}
	svc := NewSkillService(SkillServiceDeps{
		DB:        db,
		BlobStore: NewBlobStore(db, NewLocalObjectStore(t.TempDir())),
		Clock:     fixedClock(),
	})

	if _, err := svc.PatchSkill(context.Background(), PatchSkillRequest{
		SkillID: "skill1",
		UserID:  "user_001",
		AutoEvo: boolPtr(true),
	}); err != nil {
		t.Fatalf("PatchSkill returned error: %v", err)
	}
	var draft testSkillV2DraftRow
	if err := db.Where("skill_id = ?", "skill1").Take(&draft).Error; err != nil {
		t.Fatalf("query draft: %v", err)
	}
	if draft.DraftStatus != skillDraftStatusAutoPending {
		t.Fatalf("draft_status = %q, want %q", draft.DraftStatus, skillDraftStatusAutoPending)
	}
	detail, err := svc.GetSkill(context.Background(), GetSkillRequest{SkillID: "skill1", UserID: "user_001"})
	if err != nil {
		t.Fatalf("GetSkill returned error: %v", err)
	}
	if detail.Draft.Status != skillDraftStatusAutoPending {
		t.Fatalf("summary draft status = %q, want %q", detail.Draft.Status, skillDraftStatusAutoPending)
	}
	if detail.Draft.HasUncommittedDraft {
		t.Fatal("summary still exposes auto_pending draft as uncommitted")
	}
}

func TestPatchSkillAutoEvoKeepsEmptyDraftStatus(t *testing.T) {
	db := newSkillV2TestDB(t)
	seedSkillWithHeadRevision(t, db, "skill1", "rev1")
	svc := NewSkillService(SkillServiceDeps{
		DB:        db,
		BlobStore: NewBlobStore(db, NewLocalObjectStore(t.TempDir())),
		Clock:     fixedClock(),
	})

	if _, err := svc.PatchSkill(context.Background(), PatchSkillRequest{
		SkillID: "skill1",
		UserID:  "user_001",
		AutoEvo: boolPtr(true),
	}); err != nil {
		t.Fatalf("PatchSkill returned error: %v", err)
	}
	var draft testSkillV2DraftRow
	if err := db.Where("skill_id = ?", "skill1").Take(&draft).Error; err != nil {
		t.Fatalf("query draft: %v", err)
	}
	if draft.DraftStatus != "" {
		t.Fatalf("draft_status = %q, want empty", draft.DraftStatus)
	}
}

func seedSkillMDDraftEntry(t *testing.T, db *gorm.DB, skillID, blobHash, content string) {
	t.Helper()
	now := fixedClock().Now()
	data := []byte(content)
	if err := db.Create(&testSkillV2BlobRow{
		Hash:           blobHash,
		Size:           int64(len(data)),
		Mime:           "text/markdown",
		FileType:       "markdown",
		Binary:         false,
		StorageBackend: "postgres",
		Content:        data,
		CreatedAt:      now,
	}).Error; err != nil {
		t.Fatalf("seed draft blob: %v", err)
	}
	if err := db.Create(&testSkillV2DraftEntryRow{
		SkillID:   skillID,
		Path:      "SKILL.md",
		Op:        "upsert",
		EntryType: "file",
		BlobHash:  &blobHash,
		Size:      int64(len(data)),
		Mime:      "text/markdown",
		FileType:  "markdown",
		Mode:      420,
		UpdatedAt: now,
	}).Error; err != nil {
		t.Fatalf("seed draft entry: %v", err)
	}
}

func seedSkillWithHeadRevision(t *testing.T, db *gorm.DB, skillID, revisionID string) {
	t.Helper()
	now := fixedClock().Now()
	tags, _ := json.Marshal([]string{"paper"})
	head := revisionID
	if err := db.Create(&testSkillV2SkillRow{
		ID:                 skillID,
		OwnerUserID:        "user_001",
		OwnerUserName:      "张三",
		CreateUserID:       "user_001",
		CreateUserName:     "张三",
		Category:           "research",
		SkillName:          "论文精读",
		Description:        "原始描述",
		Tags:               tags,
		RelativeRoot:       "research/论文精读",
		SkillMDPath:        "SKILL.md",
		HeadRevisionID:     &head,
		Version:            1,
		AutoEvoApplyStatus: "idle",
		IsEnabled:          true,
		UpdateStatus:       "up_to_date",
		CreatedAt:          now,
		UpdatedAt:          now,
	}).Error; err != nil {
		t.Fatalf("seed skill: %v", err)
	}
	if err := db.Create(&testSkillV2RevisionRow{
		ID:           revisionID,
		SkillID:      skillID,
		RevisionNo:   1,
		TreeHash:     "tree_hash_v1",
		ChangeSource: "create",
		CreatedAt:    now,
	}).Error; err != nil {
		t.Fatalf("seed revision: %v", err)
	}
	blobHash := "h_skill_" + revisionID
	if err := db.Create(&testSkillV2BlobRow{
		Hash:           blobHash,
		Size:           12,
		Mime:           "text/markdown",
		FileType:       "markdown",
		Binary:         false,
		StorageBackend: "postgres",
		Content:        []byte("# 论文精读\n"),
		CreatedAt:      now,
	}).Error; err != nil {
		t.Fatalf("seed blob: %v", err)
	}
	if err := db.Create(&testSkillV2RevisionEntryRow{
		RevisionID: revisionID,
		Path:       "SKILL.md",
		EntryType:  "file",
		BlobHash:   &blobHash,
		Mime:       "text/markdown",
		FileType:   "markdown",
		Mode:       420,
	}).Error; err != nil {
		t.Fatalf("seed revision entry: %v", err)
	}
	if err := db.Create(&testSkillV2DraftRow{
		SkillID:        skillID,
		BaseRevisionID: &head,
		TaskID:         "",
		Version:        1,
		CreatedAt:      now,
		UpdatedAt:      now,
	}).Error; err != nil {
		t.Fatalf("seed draft: %v", err)
	}
}

func stringPtr(v string) *string {
	return &v
}
