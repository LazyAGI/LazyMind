package service

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gorm.io/gorm"
)

func TestCreateSkillFromURL_CreatesInitialRevision(t *testing.T) {
	db := newSkillV2TestDB(t)
	zipPath := filepath.Join(t.TempDir(), "url-skill.zip")
	writeSkillZip(t, zipPath, map[string][]byte{
		"SKILL.md":        []byte("# URL 导入\n"),
		"references/a.md": []byte("# 参考资料\n"),
		"assets/logo.png": minimalPNGBytes(),
	})
	downloader := NewFakeZipDownloader(map[string]string{
		"https://example.test/skill.zip": zipPath,
	})
	svc := NewSkillService(SkillServiceDeps{
		DB:         db,
		Downloader: downloader,
		BlobStore:  NewBlobStore(db, NewLocalObjectStore(t.TempDir())),
		Clock:      fixedClock(),
	})

	resp, err := svc.CreateSkill(context.Background(), CreateSkillRequest{
		OwnerUserID:    "user_001",
		OwnerUserName:  "张三",
		CreateUserID:   "user_001",
		CreateUserName: "张三",
		Name:           "URL 导入",
		Category:       "research",
		Source: SourceInput{
			Type: "url",
			URL:  "https://example.test/skill.zip",
		},
	})
	if err != nil {
		t.Fatalf("CreateSkill from URL returned error: %v", err)
	}
	assertInitialRevision(t, db, resp.SkillID, resp.HeadRevisionID)
	assertRevisionEntries(t, db, resp.HeadRevisionID)
	assertDraftInitialized(t, db, resp.SkillID, resp.HeadRevisionID)
	assertBlobRouting(t, db, resp.HeadRevisionID)
}

func TestCreateSkillFromURL_DownloadFailureDoesNotCreateSkill(t *testing.T) {
	db := newSkillV2TestDB(t)
	downloader := NewFakeZipDownloader(map[string]string{})
	downloader.Fail("https://example.test/timeout.zip")
	svc := NewSkillService(SkillServiceDeps{
		DB:         db,
		Downloader: downloader,
		BlobStore:  NewBlobStore(db, NewLocalObjectStore(t.TempDir())),
		Clock:      fixedClock(),
	})

	_, err := svc.CreateSkill(context.Background(), CreateSkillRequest{
		OwnerUserID:  "user_001",
		CreateUserID: "user_001",
		Name:         "下载失败",
		Category:     "research",
		Source: SourceInput{
			Type: "url",
			URL:  "https://example.test/timeout.zip",
		},
	})
	if err == nil {
		t.Fatal("CreateSkill from failing URL succeeded")
	}
	assertNoSkillTruthRows(t, db)
	if got := countRows(t, db, "skill_blobs", ""); got != 0 {
		t.Fatalf("skill_blobs count = %d, want 0", got)
	}
}

func TestReplaceSkillContentFromUploadedZip_CreatesNewRevision(t *testing.T) {
	db := newSkillV2TestDB(t)
	seedSkillWithHeadRevision(t, db, "skill1", "rev1")
	zipPath := filepath.Join(t.TempDir(), "replacement.zip")
	writeSkillZip(t, zipPath, map[string][]byte{
		"SKILL.md":        []byte("# 论文精读 v2\n"),
		"references/b.md": []byte("# 新资料\n"),
	})
	uploadStore := newFakeUploadStore()
	uploadStore.Put(UploadSession{
		UploadID:    "upload_replace",
		OwnerUserID: "user_001",
		State:       "completed",
		StoredPath:  zipPath,
		Filename:    "replacement.zip",
	})
	svc := NewSkillService(SkillServiceDeps{
		DB:          db,
		UploadStore: uploadStore,
		BlobStore:   NewBlobStore(db, NewLocalObjectStore(t.TempDir())),
		Clock:       fixedClock(),
	})

	resp, err := svc.PatchSkill(context.Background(), PatchSkillRequest{
		SkillID: "skill1",
		UserID:  "user_001",
		Source: &SourceInput{
			Type:     "uploaded_zip",
			UploadID: "upload_replace",
			Filename: "replacement.zip",
		},
	})
	if err != nil {
		t.Fatalf("PatchSkill source replacement returned error: %v", err)
	}
	if resp.HeadRevisionID == "rev1" {
		t.Fatal("source replacement did not create a new head revision")
	}
	assertInitialReplacementRevision(t, db, "skill1", resp.HeadRevisionID)
	assertDraftInitialized(t, db, "skill1", resp.HeadRevisionID)
}

func TestReplaceSkillContent_RejectsWhenDraftExists(t *testing.T) {
	db := newSkillV2TestDB(t)
	seedSkillWithHeadRevision(t, db, "skill1", "rev1")
	now := fixedClock().Now()
	if err := db.Create(&testSkillV2DraftEntryRow{
		SkillID:   "skill1",
		Path:      "SKILL.md",
		Op:        "upsert",
		EntryType: "file",
		UpdatedAt: now,
	}).Error; err != nil {
		t.Fatalf("seed draft entry: %v", err)
	}
	svc := NewSkillService(SkillServiceDeps{DB: db, BlobStore: NewBlobStore(db, NewLocalObjectStore(t.TempDir())), Clock: fixedClock()})

	_, err := svc.PatchSkill(context.Background(), PatchSkillRequest{
		SkillID: "skill1",
		UserID:  "user_001",
		Source:  &SourceInput{Type: "url", URL: "https://example.test/skill.zip"},
	})
	if err == nil {
		t.Fatal("PatchSkill source replacement succeeded while draft overlay exists")
	}
	assertHeadRevisionUnchanged(t, db, "skill1", "rev1")
	if got := countRows(t, db, "skill_draft_entries", "skill_id = ?", "skill1"); got != 1 {
		t.Fatalf("skill_draft_entries count = %d, want 1", got)
	}
}

func TestDeleteSkill_TrashOnlyKeepsSkillGraph(t *testing.T) {
	db := newSkillV2TestDB(t)
	seedSkillWithHeadRevision(t, db, "skill1", "rev1")
	svc := NewSkillService(SkillServiceDeps{DB: db, BlobStore: NewBlobStore(db, NewLocalObjectStore(t.TempDir())), Clock: fixedClock()})

	if err := svc.DeleteSkill(context.Background(), DeleteSkillRequest{SkillID: "skill1", UserID: "user_001"}); err != nil {
		t.Fatalf("DeleteSkill returned error: %v", err)
	}
	var row testSkillV2SkillRow
	if err := db.Where("id = ?", "skill1").Take(&row).Error; err != nil {
		t.Fatalf("query trashed skill: %v", err)
	}
	if row.DeletedAt == nil || row.DeletedBy == nil || *row.DeletedBy != "user_001" {
		t.Fatalf("skill was not logically deleted: %#v", row)
	}
	for _, tc := range []struct {
		table string
		where string
		args  []any
	}{
		{table: "skills", where: "id = ?", args: []any{"skill1"}},
		{table: "skill_drafts", where: "skill_id = ?", args: []any{"skill1"}},
		{table: "skill_revisions", where: "skill_id = ?", args: []any{"skill1"}},
		{table: "skill_revision_entries", where: "revision_id = ?", args: []any{"rev1"}},
	} {
		if got := countRows(t, db, tc.table, tc.where, tc.args...); got == 0 {
			t.Fatalf("%s graph rows were deleted by trash", tc.table)
		}
	}
}

func TestTrashListAndRestoreSkill(t *testing.T) {
	db := newSkillV2TestDB(t)
	seedSkillWithHeadRevision(t, db, "skill1", "rev1")
	seedSkillWithHeadRevision(t, db, "skill2", "rev2")
	if err := db.Model(&testSkillV2SkillRow{}).Where("id = ?", "skill2").Updates(map[string]any{
		"category":      "research",
		"skill_name":    "论文精读备用",
		"relative_root": "research/论文精读备用",
	}).Error; err != nil {
		t.Fatalf("rename skill2 fixture: %v", err)
	}
	svc := NewSkillService(SkillServiceDeps{DB: db, BlobStore: NewBlobStore(db, NewLocalObjectStore(t.TempDir())), Clock: fixedClock()})

	if err := svc.DeleteSkill(context.Background(), DeleteSkillRequest{SkillID: "skill1", UserID: "user_001"}); err != nil {
		t.Fatalf("DeleteSkill returned error: %v", err)
	}
	active, err := svc.ListSkills(context.Background(), ListSkillsRequest{UserID: "user_001"})
	if err != nil {
		t.Fatalf("ListSkills returned error: %v", err)
	}
	if len(active.Items) != 1 || active.Items[0].ID != "skill2" {
		t.Fatalf("active list = %#v, want only skill2", active.Items)
	}
	trash, err := svc.ListTrashedSkills(context.Background(), ListSkillsRequest{UserID: "user_001"})
	if err != nil {
		t.Fatalf("ListTrashedSkills returned error: %v", err)
	}
	if len(trash.Items) != 1 || trash.Items[0].ID != "skill1" || trash.Items[0].DeletedAt == nil || trash.Items[0].DeletedBy != "user_001" {
		t.Fatalf("trash list = %#v, want trashed skill1", trash.Items)
	}
	if err := svc.RestoreSkill(context.Background(), RestoreSkillRequest{SkillID: "skill1", UserID: "user_001"}); err != nil {
		t.Fatalf("RestoreSkill returned error: %v", err)
	}
	active, err = svc.ListSkills(context.Background(), ListSkillsRequest{UserID: "user_001"})
	if err != nil {
		t.Fatalf("ListSkills after restore returned error: %v", err)
	}
	if len(active.Items) != 2 {
		t.Fatalf("active list after restore count = %d, want 2", len(active.Items))
	}
	trash, err = svc.ListTrashedSkills(context.Background(), ListSkillsRequest{UserID: "user_001"})
	if err != nil {
		t.Fatalf("ListTrashedSkills after restore returned error: %v", err)
	}
	if len(trash.Items) != 0 {
		t.Fatalf("trash list after restore = %#v, want empty", trash.Items)
	}
	if db.Migrator().HasTable("skill_search_indexes") {
		if got := countRows(t, db, "skill_search_indexes", "skill_id = ?", "skill1"); got != 1 {
			t.Fatalf("search index count after restore = %d, want 1", got)
		}
	}
}

func TestRestoreSkillRejectsActiveSamePath(t *testing.T) {
	db := newSkillV2TestDB(t)
	seedSkillWithHeadRevision(t, db, "skill1", "rev1")
	svc := NewSkillService(SkillServiceDeps{DB: db, BlobStore: NewBlobStore(db, NewLocalObjectStore(t.TempDir())), Clock: fixedClock()})

	if err := svc.DeleteSkill(context.Background(), DeleteSkillRequest{SkillID: "skill1", UserID: "user_001"}); err != nil {
		t.Fatalf("DeleteSkill returned error: %v", err)
	}
	seedSkillWithHeadRevision(t, db, "skill-reuploaded", "rev-reuploaded")

	err := svc.RestoreSkill(context.Background(), RestoreSkillRequest{SkillID: "skill1", UserID: "user_001"})
	if err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("RestoreSkill error = %v, want already exists", err)
	}
	var row testSkillV2SkillRow
	if err := db.Where("id = ?", "skill1").Take(&row).Error; err != nil {
		t.Fatalf("query skill1: %v", err)
	}
	if row.DeletedAt == nil {
		t.Fatal("skill1 was restored despite active same-path skill")
	}
}

func TestPurgeSkill_RemovesSkillGraphAndKeepsSharedBlob(t *testing.T) {
	db := newSkillV2TestDB(t)
	seedSkillWithHeadRevision(t, db, "skill1", "rev1")
	seedSkillWithHeadRevision(t, db, "skill2", "rev2")
	sharedHash := "h_shared"
	seedSharedBlobReference(t, db, "rev1", sharedHash)
	seedSharedBlobReference(t, db, "rev2", sharedHash)
	seedSkillDraftReviewRows(t, db, "skill1", "review1")
	svc := NewSkillService(SkillServiceDeps{DB: db, BlobStore: NewBlobStore(db, NewLocalObjectStore(t.TempDir())), Clock: fixedClock()})

	if err := svc.DeleteSkill(context.Background(), DeleteSkillRequest{SkillID: "skill1", UserID: "user_001"}); err != nil {
		t.Fatalf("DeleteSkill returned error: %v", err)
	}
	if err := svc.PurgeSkill(context.Background(), PurgeSkillRequest{SkillID: "skill1", UserID: "user_001"}); err != nil {
		t.Fatalf("PurgeSkill returned error: %v", err)
	}
	for _, tc := range []struct {
		table string
		where string
		args  []any
	}{
		{table: "skills", where: "id = ?", args: []any{"skill1"}},
		{table: "skill_drafts", where: "skill_id = ?", args: []any{"skill1"}},
		{table: "skill_draft_entries", where: "skill_id = ?", args: []any{"skill1"}},
		{table: "skill_revisions", where: "skill_id = ?", args: []any{"skill1"}},
		{table: "skill_revision_entries", where: "revision_id = ?", args: []any{"rev1"}},
		{table: "skill_draft_review_sessions", where: "skill_id = ?", args: []any{"skill1"}},
		{table: "skill_draft_review_action_batches", where: "review_session_id = ?", args: []any{"review1"}},
		{table: "skill_draft_review_action_items", where: "review_session_id = ?", args: []any{"review1"}},
	} {
		if got := countRows(t, db, tc.table, tc.where, tc.args...); got != 0 {
			t.Fatalf("%s retained skill1 rows: %d", tc.table, got)
		}
	}
	if got := countRows(t, db, "skill_blobs", "hash = ?", sharedHash); got != 1 {
		t.Fatalf("shared blob count = %d, want 1", got)
	}
	if _, err := svc.ReadFile(context.Background(), FileRef{SkillID: "skill2", RefType: "head", Path: "shared.md"}); err != nil {
		t.Fatalf("skill2 shared file became unreadable: %v", err)
	}
}

func TestEmptyTrash_PurgesOnlyTrashedSkills(t *testing.T) {
	db := newSkillV2TestDB(t)
	seedSkillWithHeadRevision(t, db, "skill1", "rev1")
	seedSkillWithHeadRevision(t, db, "skill2", "rev2")
	seedSkillWithHeadRevision(t, db, "skill3", "rev3")
	svc := NewSkillService(SkillServiceDeps{DB: db, BlobStore: NewBlobStore(db, NewLocalObjectStore(t.TempDir())), Clock: fixedClock()})

	for _, skillID := range []string{"skill1", "skill2"} {
		if err := svc.DeleteSkill(context.Background(), DeleteSkillRequest{SkillID: skillID, UserID: "user_001"}); err != nil {
			t.Fatalf("DeleteSkill(%s) returned error: %v", skillID, err)
		}
	}
	count, err := svc.EmptyTrash(context.Background(), EmptyTrashRequest{UserID: "user_001"})
	if err != nil {
		t.Fatalf("EmptyTrash returned error: %v", err)
	}
	if count != 2 {
		t.Fatalf("EmptyTrash purged %d skills, want 2", count)
	}
	if got := countRows(t, db, "skills", "id IN ?", []string{"skill1", "skill2"}); got != 0 {
		t.Fatalf("trashed skill rows retained after empty trash: %d", got)
	}
	if got := countRows(t, db, "skills", "id = ? AND deleted_at IS NULL", "skill3"); got != 1 {
		t.Fatalf("active skill3 count = %d, want 1", got)
	}
}

func TestListAndGetSkill_ReturnsMetadataAndDraftSummary(t *testing.T) {
	db := newSkillV2TestDB(t)
	seedSkillWithHeadRevision(t, db, "skill1", "rev1")
	seedSkillWithHeadRevision(t, db, "skill2", "rev2")
	if err := db.Model(&testSkillV2SkillRow{}).Where("id = ?", "skill2").Update("created_at", fixedClock().Now().Add(time.Hour)).Error; err != nil {
		t.Fatalf("update skill2 created_at: %v", err)
	}
	if err := db.Model(&testSkillV2DraftRow{}).Where("skill_id = ?", "skill1").Updates(map[string]any{
		"task_id": "task1",
		"version": 3,
	}).Error; err != nil {
		t.Fatalf("update draft fixture: %v", err)
	}
	if err := db.Create(&testSkillV2DraftEntryRow{
		SkillID:   "skill1",
		Path:      "SKILL.md",
		Op:        "upsert",
		EntryType: "file",
		UpdatedAt: fixedClock().Now(),
	}).Error; err != nil {
		t.Fatalf("seed draft overlay: %v", err)
	}
	svc := NewSkillService(SkillServiceDeps{DB: db, BlobStore: NewBlobStore(db, NewLocalObjectStore(t.TempDir())), Clock: fixedClock()})

	list, err := svc.ListSkills(context.Background(), ListSkillsRequest{UserID: "user_001"})
	if err != nil {
		t.Fatalf("ListSkills returned error: %v", err)
	}
	if len(list.Items) != 2 {
		t.Fatalf("ListSkills returned %d items, want 2", len(list.Items))
	}
	if list.Items[0].ID != "skill2" || list.Items[1].ID != "skill1" {
		t.Fatalf("ListSkills order = [%s, %s], want [skill2, skill1]", list.Items[0].ID, list.Items[1].ID)
	}
	for _, item := range list.Items {
		if item.FileContent != "" {
			t.Fatalf("list item returned file content: %#v", item)
		}
		if item.HeadRevisionID == "" {
			t.Fatalf("list item missing head revision id: %#v", item)
		}
	}

	detail, err := svc.GetSkill(context.Background(), GetSkillRequest{SkillID: "skill1", UserID: "user_001"})
	if err != nil {
		t.Fatalf("GetSkill returned error: %v", err)
	}
	if !detail.Draft.HasUncommittedDraft || detail.Draft.TaskID != "task1" {
		t.Fatalf("unexpected draft summary: %#v", detail.Draft)
	}
}

func TestAutoEvo_WritesDraftOverlay(t *testing.T) {
	db := newSkillV2TestDB(t)
	seedSkillWithHeadRevision(t, db, "skill1", "rev1")
	svc := NewSkillService(SkillServiceDeps{DB: db, BlobStore: NewBlobStore(db, NewLocalObjectStore(t.TempDir())), Clock: fixedClock()})

	if err := svc.ApplyAutoEvoDraft(context.Background(), AutoEvoDraftRequest{
		SkillID:        "skill1",
		ConversationID: "conv_auto",
		Files: map[string][]byte{
			"SKILL.md": []byte("# 自动演进草稿\n"),
		},
	}); err != nil {
		t.Fatalf("ApplyAutoEvoDraft returned error: %v", err)
	}
	assertHeadRevisionUnchanged(t, db, "skill1", "rev1")
	if got := countRows(t, db, "skill_draft_entries", "skill_id = ?", "skill1"); got != 1 {
		t.Fatalf("skill_draft_entries count = %d, want 1", got)
	}
}

func seedSkillDraftReviewRows(t *testing.T, db *gorm.DB, skillID, reviewID string) {
	t.Helper()
	for _, stmt := range []string{
		`CREATE TABLE IF NOT EXISTS skill_draft_review_sessions (id varchar(36) PRIMARY KEY, skill_id varchar(36), base_revision_id varchar(36), draft_version_at_start integer, draft_snapshot_hash varchar(64), status varchar(32), version integer, undo_limit integer, created_at datetime, updated_at datetime)`,
		`CREATE TABLE IF NOT EXISTS skill_draft_review_action_batches (id varchar(36) PRIMARY KEY, review_session_id varchar(36), sequence integer, created_at datetime)`,
		`CREATE TABLE IF NOT EXISTS skill_draft_review_action_items (id varchar(36) PRIMARY KEY, batch_id varchar(36), review_session_id varchar(36), path text, hunk_id text, before_decision varchar(16), after_decision varchar(16), created_at datetime)`,
	} {
		if err := db.Exec(stmt).Error; err != nil {
			t.Fatalf("create review test table: %v", err)
		}
	}
	now := fixedClock().Now()
	if err := db.Table("skill_draft_review_sessions").Create(map[string]any{
		"id":                     reviewID,
		"skill_id":               skillID,
		"base_revision_id":       "rev1",
		"draft_version_at_start": 1,
		"draft_snapshot_hash":    "h_review_snapshot",
		"status":                 "active",
		"version":                1,
		"undo_limit":             20,
		"created_at":             now,
		"updated_at":             now,
	}).Error; err != nil {
		t.Fatalf("seed review session: %v", err)
	}
	if err := db.Table("skill_draft_review_action_batches").Create(map[string]any{
		"id":                "batch-" + reviewID,
		"review_session_id": reviewID,
		"sequence":          1,
		"created_at":        now,
	}).Error; err != nil {
		t.Fatalf("seed review action batch: %v", err)
	}
	if err := db.Table("skill_draft_review_action_items").Create(map[string]any{
		"id":                "item-" + reviewID,
		"batch_id":          "batch-" + reviewID,
		"review_session_id": reviewID,
		"path":              "SKILL.md",
		"hunk_id":           "hunk_1",
		"before_decision":   "pending",
		"after_decision":    "accept",
		"created_at":        now,
	}).Error; err != nil {
		t.Fatalf("seed review action item: %v", err)
	}
}

func TestReviewAccept_CommitsDraftOrCreatesRevision(t *testing.T) {
	db := newSkillV2TestDB(t)
	seedSkillWithHeadRevision(t, db, "skill1", "rev1")
	svc := NewSkillService(SkillServiceDeps{DB: db, BlobStore: NewBlobStore(db, NewLocalObjectStore(t.TempDir())), Clock: fixedClock()})

	resp, err := svc.AcceptReview(context.Background(), AcceptReviewRequest{
		SkillID:  "skill1",
		UserID:   "user_001",
		ReviewID: "review1",
		Files: map[string][]byte{
			"SKILL.md": []byte("# Review 接受\n"),
		},
	})
	if err != nil {
		t.Fatalf("AcceptReview returned error: %v", err)
	}
	if resp.HeadRevisionID == "" || resp.HeadRevisionID == "rev1" {
		t.Fatalf("AcceptReview did not create a new head revision: %#v", resp)
	}
	assertNoDraftEntries(t, db, "skill1")
}

func assertInitialReplacementRevision(t *testing.T, db *gorm.DB, skillID, revisionID string) {
	t.Helper()
	var revision testSkillV2RevisionRow
	if err := db.Where("id = ?", revisionID).Take(&revision).Error; err != nil {
		t.Fatalf("query replacement revision: %v", err)
	}
	if revision.SkillID != skillID || revision.RevisionNo != 2 || revision.ParentRevisionID == nil || *revision.ParentRevisionID != "rev1" {
		t.Fatalf("unexpected replacement revision: %#v", revision)
	}
	entries := listRevisionEntries(t, db, revisionID)
	if _, ok := entries["references/b.md"]; !ok {
		t.Fatalf("replacement revision missing references/b.md: %#v", entries)
	}
}

func assertHeadRevisionUnchanged(t *testing.T, db *gorm.DB, skillID, revisionID string) {
	t.Helper()
	var skill testSkillV2SkillRow
	if err := db.Where("id = ?", skillID).Take(&skill).Error; err != nil {
		t.Fatalf("query skill: %v", err)
	}
	if skill.HeadRevisionID == nil || *skill.HeadRevisionID != revisionID {
		t.Fatalf("head_revision_id = %v, want %q", skill.HeadRevisionID, revisionID)
	}
}

func seedSharedBlobReference(t *testing.T, db *gorm.DB, revisionID, blobHash string) {
	t.Helper()
	now := fixedClock().Now()
	var blob testSkillV2BlobRow
	err := db.Where("hash = ?", blobHash).Take(&blob).Error
	if err != nil {
		if err := db.Create(&testSkillV2BlobRow{
			Hash:           blobHash,
			Size:           6,
			Mime:           "text/markdown",
			FileType:       "markdown",
			Binary:         false,
			StorageBackend: "postgres",
			Content:        []byte("shared"),
			CreatedAt:      now,
		}).Error; err != nil {
			t.Fatalf("seed shared blob: %v", err)
		}
	}
	if err := db.Create(&testSkillV2RevisionEntryRow{
		RevisionID: revisionID,
		Path:       "shared.md",
		EntryType:  "file",
		BlobHash:   &blobHash,
		Size:       6,
		Mime:       "text/markdown",
		FileType:   "markdown",
		Mode:       420,
	}).Error; err != nil {
		t.Fatalf("seed shared revision entry: %v", err)
	}
}
