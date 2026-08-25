package market

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	skillservice "lazymind/core/skillv2/service"
	"lazymind/core/skillv2/testutil"
)

type marketZipDownloader struct {
	path        string
	receivedURL string
}

func (d *marketZipDownloader) Download(_ context.Context, rawURL string) (string, error) {
	d.receivedURL = rawURL
	return d.path, nil
}

func TestMarketInstall_CopiesSkillTreeForUser(t *testing.T) {
	db := testutil.NewTestDB(t)
	testutil.SeedSkillWithRevision(t, db, "market_skill", "market_rev1")
	testutil.MustCreate(t, db, &testutil.SkillMarketItemRow{
		ID:            "market_item1",
		SourceSkillID: "market_skill",
		Status:        "published",
		Tags:          []byte(`["debugging","development"]`),
		CreatedAt:     testutil.TimeFixture(),
		UpdatedAt:     testutil.TimeFixture(),
	})
	service := NewService(ServiceDeps{DB: db.DB, BlobStore: NewBlobStore(db.DB, NewLocalObjectStore(t.TempDir()))})

	resp, err := service.Install(context.Background(), InstallRequest{MarketItemID: "market_item1", UserID: "user_002", UserName: "李四"})
	if err != nil {
		t.Fatalf("Install returned error: %v", err)
	}
	if resp.SkillID == "" || resp.SkillID == "market_skill" {
		t.Fatalf("Install did not create user-owned skill copy: %#v", resp)
	}
	var copied testutil.SkillRow
	if err := db.Where("id = ?", resp.SkillID).Take(&copied).Error; err != nil {
		t.Fatalf("query installed skill: %v", err)
	}
	if copied.OwnerUserID != "user_002" || copied.HeadRevisionID == nil {
		t.Fatalf("installed skill owner/head invalid: %#v", copied)
	}
	if copied.Category != "external" || copied.RelativeRoot != "external/论文精读-market_skill" {
		t.Fatalf("installed skill identity invalid: %#v", copied)
	}
	var copiedTags []string
	if err := json.Unmarshal(copied.Tags, &copiedTags); err != nil || len(copiedTags) != 2 || copiedTags[0] != "debugging" || copiedTags[1] != "development" {
		t.Fatalf("installed skill tags = %#v, err=%v", copiedTags, err)
	}
	if got := testutil.CountRows(t, db, "skill_revision_entries", "revision_id = ?", *copied.HeadRevisionID); got == 0 {
		t.Fatal("installed skill revision has no entries")
	}
	if got := testutil.CountRows(t, db, "skill_market_installs", "market_item_id = ? AND user_id = ? AND skill_id = ?", "market_item1", "user_002", resp.SkillID); got != 1 {
		t.Fatalf("market install record count = %d, want 1", got)
	}

	resp2, err := service.Install(context.Background(), InstallRequest{MarketItemID: "market_item1", UserID: "user_002", UserName: "李四"})
	if err != nil {
		t.Fatalf("second Install returned error: %v", err)
	}
	if resp2.SkillID != resp.SkillID {
		t.Fatalf("second install returned skill %q, want %q", resp2.SkillID, resp.SkillID)
	}
	if got := testutil.CountRows(t, db, "skill_market_installs", "market_item_id = ? AND user_id = ?", "market_item1", "user_002"); got != 1 {
		t.Fatalf("market install row count after reinstall = %d, want 1", got)
	}
}

func TestMarketInstall_RestoresTrashedInstalledSkill(t *testing.T) {
	db := testutil.NewTestDB(t)
	testutil.SeedSkillWithRevision(t, db, "market_skill", "market_rev1")
	testutil.MustCreate(t, db, &testutil.SkillMarketItemRow{
		ID:            "market_item1",
		SourceSkillID: "market_skill",
		Status:        "published",
		CreatedAt:     testutil.TimeFixture(),
		UpdatedAt:     testutil.TimeFixture(),
	})
	marketService := NewService(ServiceDeps{DB: db.DB, BlobStore: NewBlobStore(db.DB, NewLocalObjectStore(t.TempDir()))})

	installed, err := marketService.Install(context.Background(), InstallRequest{
		MarketItemID: "market_item1",
		UserID:       "user_002",
		UserName:     "李四",
	})
	if err != nil {
		t.Fatalf("initial Install returned error: %v", err)
	}

	skillService := skillservice.NewSkillService(skillservice.SkillServiceDeps{DB: db.DB})
	if err := skillService.DeleteSkill(context.Background(), skillservice.DeleteSkillRequest{
		SkillID: installed.SkillID,
		UserID:  "user_002",
	}); err != nil {
		t.Fatalf("DeleteSkill returned error: %v", err)
	}

	reinstalled, err := marketService.Install(context.Background(), InstallRequest{
		MarketItemID: "market_item1",
		UserID:       "user_002",
		UserName:     "李四",
	})
	if err != nil {
		t.Fatalf("reinstall returned error: %v", err)
	}
	if reinstalled.SkillID != installed.SkillID {
		t.Fatalf("reinstall returned skill %q, want restored skill %q", reinstalled.SkillID, installed.SkillID)
	}
	if got := testutil.CountRows(t, db, "skills", "id = ? AND deleted_at IS NOT NULL", installed.SkillID); got != 0 {
		t.Fatalf("trashed installed skill count after reinstall = %d, want 0", got)
	}
	if got := testutil.CountRows(t, db, "skills", "id = ? AND deleted_at IS NULL", installed.SkillID); got != 1 {
		t.Fatalf("restored installed skill count after reinstall = %d, want 1", got)
	}
	if got := testutil.CountRows(t, db, "skill_market_installs", "market_item_id = ? AND user_id = ? AND skill_id = ?", "market_item1", "user_002", installed.SkillID); got != 1 {
		t.Fatalf("market install record count after restore = %d, want 1", got)
	}
}

func TestMarketInstall_DoesNotReferenceMarketAsTruth(t *testing.T) {
	db := testutil.NewTestDB(t)
	testutil.SeedSkillWithRevision(t, db, "market_skill", "market_rev1")
	testutil.MustCreate(t, db, &testutil.SkillMarketItemRow{ID: "market_item1", SourceSkillID: "market_skill", Status: "published", CreatedAt: testutil.TimeFixture(), UpdatedAt: testutil.TimeFixture()})
	service := NewService(ServiceDeps{DB: db.DB, BlobStore: NewBlobStore(db.DB, NewLocalObjectStore(t.TempDir()))})

	resp, err := service.Install(context.Background(), InstallRequest{MarketItemID: "market_item1", UserID: "user_002", UserName: "李四"})
	if err != nil {
		t.Fatalf("Install returned error: %v", err)
	}
	if err := db.Model(&testutil.SkillRevisionEntryRow{}).Where("revision_id = ? AND path = ?", "market_rev1", "SKILL.md").Update("path", "changed.md").Error; err != nil {
		t.Fatalf("mutate market source fixture: %v", err)
	}
	tree, err := service.GetInstalledTree(context.Background(), GetInstalledTreeRequest{SkillID: resp.SkillID, UserID: "user_002"})
	if err != nil {
		t.Fatalf("GetInstalledTree returned error: %v", err)
	}
	if !tree.HasPath("SKILL.md") || tree.HasPath("changed.md") {
		t.Fatalf("installed tree still references market truth: %#v", tree)
	}
}

func TestMarketAdminUnpublish_PreservesInstalledCopy(t *testing.T) {
	db := testutil.NewTestDB(t)
	testutil.SeedSkillWithRevision(t, db, "market_skill", "market_rev1")
	testutil.MustCreate(t, db, &testutil.SkillMarketItemRow{
		ID:            "market_item1",
		SourceSkillID: "market_skill",
		Status:        "published",
		CreatedAt:     testutil.TimeFixture(),
		UpdatedAt:     testutil.TimeFixture(),
	})
	service := NewService(ServiceDeps{DB: db.DB, BlobStore: NewBlobStore(db.DB, NewLocalObjectStore(t.TempDir()))})

	installed, err := service.Install(context.Background(), InstallRequest{
		MarketItemID: "market_item1",
		UserID:       "user_002",
		UserName:     "李四",
	})
	if err != nil {
		t.Fatalf("Install returned error: %v", err)
	}
	if _, err := service.Unpublish(context.Background(), UnpublishRequest{
		AdminUserID:  "admin_001",
		MarketItemID: "market_item1",
	}); err != nil {
		t.Fatalf("Unpublish returned error: %v", err)
	}

	if got := testutil.CountRows(t, db, "skill_market_items", "id = ? AND status = ?", "market_item1", "unpublished"); got != 1 {
		t.Fatalf("unpublished market item count = %d, want 1", got)
	}
	if got := testutil.CountRows(t, db, "skill_market_installs", "market_item_id = ? AND user_id = ? AND skill_id = ?", "market_item1", "user_002", installed.SkillID); got != 1 {
		t.Fatalf("market install record count = %d, want 1", got)
	}
	tree, err := service.GetInstalledTree(context.Background(), GetInstalledTreeRequest{SkillID: installed.SkillID, UserID: "user_002"})
	if err != nil {
		t.Fatalf("GetInstalledTree after unpublish returned error: %v", err)
	}
	if !tree.HasPath("SKILL.md") {
		t.Fatalf("installed tree missing after unpublish: %#v", tree)
	}
}

func TestMarketAdminPublishEditUnpublish(t *testing.T) {
	db := testutil.NewTestDB(t)
	zipPath := filepath.Join(t.TempDir(), "publish.zip")
	testutil.WriteSkillZip(t, zipPath, map[string][]byte{
		"SKILL.md": []byte("---\nname: 论文精读\ndescription: 阅读并总结论文\n---\n# 论文精读\n"),
	})
	service := NewAdminService(AdminServiceDeps{DB: db.DB, BlobStore: NewBlobStore(db.DB, NewLocalObjectStore(t.TempDir()))})

	published, err := service.Publish(context.Background(), PublishRequest{
		AdminUserID: "admin_001",
		Tags:        []string{"research", "paper"},
		Source: SourceInput{
			Type:       "uploaded_zip",
			UploadID:   "upload_market_zip",
			StoredPath: zipPath,
		},
	})
	if err != nil {
		t.Fatalf("Publish returned error: %v", err)
	}
	if published.MarketItemID == "" || published.SourceSkillID == "" {
		t.Fatalf("Publish returned incomplete response: %#v", published)
	}
	if got := testutil.CountRows(t, db, "skill_market_items", "id = ? AND status = ?", published.MarketItemID, "published"); got != 1 {
		t.Fatalf("published market item count = %d, want 1", got)
	}
	if got := testutil.CountRows(t, db, "skill_market_installs", "market_item_id = ? AND user_id = ? AND skill_id = ?", published.MarketItemID, "admin_001", published.SourceSkillID); got != 0 {
		t.Fatalf("publisher market install count = %d, want 0", got)
	}
	var source testutil.SkillRow
	if err := db.Where("id = ?", published.SourceSkillID).Take(&source).Error; err != nil {
		t.Fatalf("query published source: %v", err)
	}
	if source.OwnerUserID == "admin_001" || source.CreateUserID != "admin_001" {
		t.Fatalf("published source ownership = %#v, want internal owner and admin creator", source)
	}
	if source.Category != "external" {
		t.Fatalf("published source category = %q, want external", source.Category)
	}
	if source.SkillName != "论文精读" {
		t.Fatalf("published source name = %q, want SKILL.md name", source.SkillName)
	}
	var marketItem testutil.SkillMarketItemRow
	if err := db.Where("id = ?", published.MarketItemID).Take(&marketItem).Error; err != nil {
		t.Fatalf("query market item: %v", err)
	}
	if string(marketItem.Tags) != `["paper","research"]` {
		t.Fatalf("market item tags = %s, want sorted tags", marketItem.Tags)
	}

	versionNote := "v2"
	updatedTags := []string{"updated", "paper", "updated"}
	if _, err := service.Edit(context.Background(), EditRequest{AdminUserID: "admin_001", MarketItemID: published.MarketItemID, VersionNote: &versionNote, Tags: &updatedTags}); err != nil {
		t.Fatalf("Edit returned error: %v", err)
	}
	if err := db.Where("id = ?", published.MarketItemID).Take(&marketItem).Error; err != nil {
		t.Fatalf("query edited market item: %v", err)
	}
	if string(marketItem.Tags) != `["paper","updated"]` || marketItem.VersionNote != "v2" {
		t.Fatalf("edited market item = %#v", marketItem)
	}
	if _, err := service.Unpublish(context.Background(), UnpublishRequest{AdminUserID: "admin_001", MarketItemID: published.MarketItemID}); err != nil {
		t.Fatalf("Unpublish returned error: %v", err)
	}
	if got := testutil.CountRows(t, db, "skill_market_items", "id = ? AND status = ?", published.MarketItemID, "unpublished"); got != 1 {
		t.Fatalf("unpublished market item count = %d, want 1", got)
	}
}

func TestMarketAdminDelete_RemovesMarketSourceGraphAndPreservesInstalledCopies(t *testing.T) {
	db := testutil.NewTestDB(t)
	testutil.SeedSkillWithRevision(t, db, "market_skill", "market_rev1")
	if err := db.Model(&testutil.SkillRow{}).Where("id = ?", "market_skill").Updates(map[string]any{
		"owner_user_id":   marketSourceOwnerID("market_item1"),
		"owner_user_name": "skill-market",
	}).Error; err != nil {
		t.Fatalf("mark market source ownership: %v", err)
	}
	testutil.MustCreate(t, db, &testutil.SkillMarketItemRow{
		ID:            "market_item1",
		SourceSkillID: "market_skill",
		Status:        "published",
		CreatedAt:     testutil.TimeFixture(),
		UpdatedAt:     testutil.TimeFixture(),
	})
	testutil.MustCreate(t, db, &testutil.SkillSearchIndexRow{
		SkillID:        "market_skill",
		OwnerUserID:    marketSourceOwnerID("market_item1"),
		HeadRevisionID: "market_rev1",
		Content:        "market content",
		UpdatedAt:      testutil.TimeFixture(),
	})
	exclusiveBlobHash := "market_draft_only_blob"
	testutil.SeedTextBlob(t, db, exclusiveBlobHash, "draft only")
	testutil.MustCreate(t, db, &testutil.SkillDraftEntryRow{
		SkillID:   "market_skill",
		Path:      "draft.md",
		Op:        "upsert",
		EntryType: "file",
		BlobHash:  &exclusiveBlobHash,
		UpdatedAt: testutil.TimeFixture(),
	})
	testutil.MustCreate(t, db, &testutil.SkillDraftReviewSessionRow{
		ID:                  "market_review1",
		SkillID:             "market_skill",
		BaseRevisionID:      "market_rev1",
		DraftVersionAtStart: 1,
		DraftSnapshotHash:   "snapshot",
		Status:              "active",
		Version:             1,
		UndoLimit:           20,
		CreatedAt:           testutil.TimeFixture(),
		UpdatedAt:           testutil.TimeFixture(),
	})
	testutil.MustCreate(t, db, &testutil.SkillDraftReviewActionBatchRow{
		ID:              "market_batch1",
		ReviewSessionID: "market_review1",
		Sequence:        1,
		CreatedAt:       testutil.TimeFixture(),
	})
	testutil.MustCreate(t, db, &testutil.SkillDraftReviewActionItemRow{
		ID:              "market_action1",
		BatchID:         "market_batch1",
		ReviewSessionID: "market_review1",
		Path:            "draft.md",
		HunkID:          "hunk1",
		AfterDecision:   "accepted",
		CreatedAt:       testutil.TimeFixture(),
	})

	service := NewAdminService(AdminServiceDeps{DB: db.DB, BlobStore: NewBlobStore(db.DB, NewLocalObjectStore(t.TempDir()))})
	installed, err := service.Install(context.Background(), InstallRequest{
		MarketItemID: "market_item1",
		UserID:       "user_002",
		UserName:     "李四",
	})
	if err != nil {
		t.Fatalf("Install returned error: %v", err)
	}

	deleted, err := service.Delete(context.Background(), DeleteRequest{MarketItemID: "market_item1"})
	if err != nil {
		t.Fatalf("Delete returned error: %v", err)
	}
	if deleted.MarketItemID != "market_item1" || deleted.SourceSkillID != "market_skill" {
		t.Fatalf("Delete response = %#v", deleted)
	}

	for _, tc := range []struct {
		table string
		where string
		args  []any
	}{
		{table: "skill_market_items", where: "id = ?", args: []any{"market_item1"}},
		{table: "skill_market_installs", where: "market_item_id = ?", args: []any{"market_item1"}},
		{table: "skills", where: "id = ?", args: []any{"market_skill"}},
		{table: "skill_revisions", where: "skill_id = ?", args: []any{"market_skill"}},
		{table: "skill_revision_entries", where: "revision_id = ?", args: []any{"market_rev1"}},
		{table: "skill_drafts", where: "skill_id = ?", args: []any{"market_skill"}},
		{table: "skill_draft_entries", where: "skill_id = ?", args: []any{"market_skill"}},
		{table: "skill_draft_review_sessions", where: "skill_id = ?", args: []any{"market_skill"}},
		{table: "skill_draft_review_action_batches", where: "review_session_id = ?", args: []any{"market_review1"}},
		{table: "skill_draft_review_action_items", where: "review_session_id = ?", args: []any{"market_review1"}},
		{table: "skill_search_indexes", where: "skill_id = ?", args: []any{"market_skill"}},
		{table: "skill_blobs", where: "hash = ?", args: []any{exclusiveBlobHash}},
	} {
		if got := testutil.CountRows(t, db, tc.table, tc.where, tc.args...); got != 0 {
			t.Fatalf("%s associated row count = %d, want 0", tc.table, got)
		}
	}
	if got := testutil.CountRows(t, db, "skills", "id = ? AND owner_user_id = ?", installed.SkillID, "user_002"); got != 1 {
		t.Fatalf("installed user copy count = %d, want 1", got)
	}
	if got := testutil.CountRows(t, db, "skill_blobs", "hash = ?", "h_skill_market_rev1"); got != 1 {
		t.Fatalf("shared installed blob count = %d, want 1", got)
	}
	tree, err := service.GetInstalledTree(context.Background(), GetInstalledTreeRequest{SkillID: installed.SkillID, UserID: "user_002"})
	if err != nil {
		t.Fatalf("GetInstalledTree after delete returned error: %v", err)
	}
	if !tree.HasPath("SKILL.md") {
		t.Fatalf("installed tree missing after delete: %#v", tree)
	}
}

func TestMarketAdminPublish_AllowsSingleTopLevelDirectory(t *testing.T) {
	db := testutil.NewTestDB(t)
	zipPath := filepath.Join(t.TempDir(), "wrapped.zip")
	testutil.WriteSkillZip(t, zipPath, map[string][]byte{
		"openclaw-openclaw-changelog-update/SKILL.md":            []byte("---\nname: openclaw-changelog\ndescription: OpenClaw changelog skill\n---\n# OpenClaw\n"),
		"openclaw-openclaw-changelog-update/references/a.md":     []byte("# A\n"),
		"__MACOSX/openclaw-openclaw-changelog-update/._SKILL.md": []byte("macOS metadata"),
		"openclaw-openclaw-changelog-update/.DS_Store":           []byte("finder metadata"),
	})
	service := NewAdminService(AdminServiceDeps{DB: db.DB, BlobStore: NewBlobStore(db.DB, NewLocalObjectStore(t.TempDir()))})

	published, err := service.Publish(context.Background(), PublishRequest{
		AdminUserID: "admin_001",
		Tags:        []string{"team"},
		Source: SourceInput{
			Type:       "uploaded_zip",
			UploadID:   "upload_wrapped_market_zip",
			StoredPath: zipPath,
		},
	})
	if err != nil {
		t.Fatalf("Publish returned error: %v", err)
	}

	var skill skillRow
	if err := db.Where("id = ?", published.SourceSkillID).Take(&skill).Error; err != nil {
		t.Fatalf("query source skill: %v", err)
	}
	if skill.HeadRevisionID == nil {
		t.Fatal("published source skill missing head revision")
	}
	if skill.Category != "external" {
		t.Fatalf("category = %q, want external", skill.Category)
	}
	if skill.SkillName != "openclaw-changelog" {
		t.Fatalf("skill_name = %q, want canonical SKILL.md name", skill.SkillName)
	}
	if skill.RelativeRoot != "external/openclaw-changelog" {
		t.Fatalf("relative_root = %q, want external/openclaw-changelog", skill.RelativeRoot)
	}
	if skill.SkillMDPath != "SKILL.md" {
		t.Fatalf("skill_md_path = %q, want SKILL.md", skill.SkillMDPath)
	}
	var skillMDEntry skillRevisionEntryRow
	if err := db.Where("revision_id = ? AND path = ?", *skill.HeadRevisionID, "SKILL.md").Take(&skillMDEntry).Error; err != nil {
		t.Fatalf("query normalized SKILL.md entry: %v", err)
	}
	if skillMDEntry.BlobHash == nil || *skillMDEntry.BlobHash == "" {
		t.Fatal("normalized SKILL.md entry has empty blob_hash")
	}
	if got := testutil.CountRows(t, db, "skill_revision_entries", "revision_id = ? AND path = ?", *skill.HeadRevisionID, "references/a.md"); got != 1 {
		t.Fatalf("normalized references/a.md entry count = %d, want 1", got)
	}
	if got := testutil.CountRows(t, db, "skill_revision_entries", "revision_id = ? AND path LIKE ?", *skill.HeadRevisionID, "openclaw-openclaw-changelog-update/%"); got != 0 {
		t.Fatalf("wrapper path entry count = %d, want 0", got)
	}
	if got := testutil.CountRows(t, db, "skill_revision_entries", "revision_id = ? AND path LIKE ?", *skill.HeadRevisionID, "__MACOSX/%"); got != 0 {
		t.Fatalf("macOS metadata entry count = %d, want 0", got)
	}
	if got := testutil.CountRows(t, db, "skill_revision_entries", "revision_id = ? AND path = ?", *skill.HeadRevisionID, ".DS_Store"); got != 0 {
		t.Fatalf("Finder metadata entry count = %d, want 0", got)
	}
	var skillMDBlob skillBlobRow
	if err := db.Where("hash = ?", *skillMDEntry.BlobHash).Take(&skillMDBlob).Error; err != nil {
		t.Fatalf("query SKILL.md blob: %v", err)
	}
	if skillMDBlob.StorageBackend != "postgres" || len(skillMDBlob.Content) == 0 || skillMDBlob.StorageKey != nil {
		t.Fatalf("SKILL.md blob storage invalid: %#v", skillMDBlob)
	}
}

func TestMarketAdminPublishURLUsesSkillMDName(t *testing.T) {
	db := testutil.NewTestDB(t)
	zipPath := filepath.Join(t.TempDir(), "main.zip")
	testutil.WriteSkillZip(t, zipPath, map[string][]byte{
		"repository-main/SKILL.md": []byte("---\nname: canonical-url-skill\ndescription: Canonical URL skill description\n---\n# Skill\n"),
	})
	downloader := &marketZipDownloader{path: zipPath}
	service := NewAdminService(AdminServiceDeps{
		DB:         db.DB,
		BlobStore:  NewBlobStore(db.DB, NewLocalObjectStore(t.TempDir())),
		Downloader: downloader,
	})

	published, err := service.Publish(context.Background(), PublishRequest{
		AdminUserID: "admin_001",
		Tags:        []string{"team"},
		Source: SourceInput{
			Type: "url",
			URL:  "https://github.com/example/repository/archive/refs/heads/main.zip",
		},
	})
	if err != nil {
		t.Fatalf("Publish returned error: %v", err)
	}
	if downloader.receivedURL != "https://github.com/example/repository/archive/refs/heads/main.zip" {
		t.Fatalf("download URL = %q", downloader.receivedURL)
	}

	var skill skillRow
	if err := db.Where("id = ?", published.SourceSkillID).Take(&skill).Error; err != nil {
		t.Fatalf("query source skill: %v", err)
	}
	if skill.SkillName != "canonical-url-skill" {
		t.Fatalf("skill_name = %q, want SKILL.md name", skill.SkillName)
	}
	if skill.Description != "Canonical URL skill description" {
		t.Fatalf("description = %q, want SKILL.md description", skill.Description)
	}
	var revision skillRevisionRow
	if err := db.Where("id = ?", *skill.HeadRevisionID).Take(&revision).Error; err != nil {
		t.Fatalf("query source revision: %v", err)
	}
	if revision.SourceRefID != "https://github.com/example/repository/archive/refs/heads/main.zip" {
		t.Fatalf("source_ref_id = %q, want repository URL", revision.SourceRefID)
	}
}

func TestMarketAdminPublishURLRejectsMissingSkillName(t *testing.T) {
	db := testutil.NewTestDB(t)
	zipPath := filepath.Join(t.TempDir(), "main.zip")
	testutil.WriteSkillZip(t, zipPath, map[string][]byte{
		"repository-main/SKILL.md": []byte("---\ndescription: Missing canonical name\n---\n# Skill\n"),
	})
	service := NewAdminService(AdminServiceDeps{
		DB:         db.DB,
		BlobStore:  NewBlobStore(db.DB, NewLocalObjectStore(t.TempDir())),
		Downloader: &marketZipDownloader{path: zipPath},
	})

	_, err := service.Publish(context.Background(), PublishRequest{
		AdminUserID: "admin_001",
		Tags:        []string{"team"},
		Source: SourceInput{
			Type: "url",
			URL:  "https://github.com/example/repository/archive/refs/heads/main.zip",
		},
	})
	if err == nil || !strings.Contains(err.Error(), `frontmatter field "name" is required`) {
		t.Fatalf("Publish error = %v, want missing SKILL.md name", err)
	}
	if got := testutil.CountRows(t, db, "skill_market_items", ""); got != 0 {
		t.Fatalf("market item count = %d, want 0", got)
	}
}

func TestMarketPublishRejectsDuplicateCanonicalName(t *testing.T) {
	db := testutil.NewTestDB(t)
	service := NewAdminService(AdminServiceDeps{DB: db.DB, BlobStore: NewBlobStore(db.DB, NewLocalObjectStore(t.TempDir()))})

	firstZip := filepath.Join(t.TempDir(), "first.zip")
	secondZip := filepath.Join(t.TempDir(), "second.zip")
	for _, zipPath := range []string{firstZip, secondZip} {
		testutil.WriteSkillZip(t, zipPath, map[string][]byte{
			"SKILL.md": []byte("---\nname: Same Skill\ndescription: canonical description\n---\n# Skill\n"),
		})
	}
	if _, err := service.Publish(context.Background(), PublishRequest{
		AdminUserID: "admin_001",
		Tags:        []string{"debugging"},
		Source:      SourceInput{Type: "uploaded_zip", StoredPath: firstZip},
	}); err != nil {
		t.Fatalf("first Publish returned error: %v", err)
	}
	if _, err := service.Publish(context.Background(), PublishRequest{
		AdminUserID: "admin_002",
		Tags:        []string{"research"},
		Source:      SourceInput{Type: "uploaded_zip", StoredPath: secondZip},
	}); err == nil || !strings.Contains(err.Error(), "skill market name already exists") {
		t.Fatalf("second Publish error = %v, want duplicate canonical name", err)
	}
	if got := testutil.CountRows(t, db, "skill_market_items", ""); got != 1 {
		t.Fatalf("market item count = %d, want 1", got)
	}
	if got := testutil.CountRows(t, db, "skills", "owner_user_name = ?", "skill-market"); got != 1 {
		t.Fatalf("market source count = %d, want 1", got)
	}
}

func TestMarketInstall_NameConflict(t *testing.T) {
	db := testutil.NewTestDB(t)
	testutil.SeedSkillWithRevision(t, db, "user_skill", "user_rev1")
	testutil.SeedSkillWithRevision(t, db, "market_skill", "market_rev1")
	if err := db.Model(&testutil.SkillRow{}).Where("id = ?", "market_skill").Updates(map[string]any{
		"owner_user_id":    "admin_001",
		"owner_user_name":  "管理员",
		"create_user_id":   "admin_001",
		"create_user_name": "管理员",
	}).Error; err != nil {
		t.Fatalf("reassign market skill owner: %v", err)
	}
	if err := db.Model(&testutil.SkillRow{}).Where("id = ?", "user_skill").Updates(map[string]any{
		"category":      "external",
		"skill_name":    "论文精读-market_skill",
		"relative_root": "external/论文精读-market_skill",
	}).Error; err != nil {
		t.Fatalf("rename conflicting user skill: %v", err)
	}
	testutil.MustCreate(t, db, &testutil.SkillMarketItemRow{ID: "market_item1", SourceSkillID: "market_skill", Status: "published", CreatedAt: testutil.TimeFixture(), UpdatedAt: testutil.TimeFixture()})
	service := NewService(ServiceDeps{DB: db.DB, BlobStore: NewBlobStore(db.DB, NewLocalObjectStore(t.TempDir()))})

	if _, err := service.Install(context.Background(), InstallRequest{MarketItemID: "market_item1", UserID: "user_001", UserName: "张三"}); err == nil {
		t.Fatal("Install succeeded despite same category/name conflict")
	}
	if got := testutil.CountRows(t, db, "skills", "owner_user_id = ?", "user_001"); got != 1 {
		t.Fatalf("user skill count = %d, want 1", got)
	}
}

func TestMarketInstall_CreatesExternalCopyForPublisher(t *testing.T) {
	db := testutil.NewTestDB(t)
	testutil.SeedSkillWithRevision(t, db, "market_skill", "market_rev1")
	adminID := "admin_001"
	if err := db.Model(&testutil.SkillRow{}).Where("id = ?", "market_skill").Updates(map[string]any{
		"owner_user_id":    "admin_001",
		"owner_user_name":  "管理员",
		"create_user_id":   "admin_001",
		"create_user_name": "管理员",
		"category":         "external",
		"relative_root":    "external/论文精读-market_skill",
	}).Error; err != nil {
		t.Fatalf("reassign market skill owner: %v", err)
	}
	testutil.MustCreate(t, db, &testutil.SkillMarketItemRow{
		ID:            "market_item1",
		SourceSkillID: "market_skill",
		Status:        "published",
		CreatedBy:     &adminID,
		CreatedAt:     testutil.TimeFixture(),
		UpdatedAt:     testutil.TimeFixture(),
	})
	service := NewService(ServiceDeps{DB: db.DB, BlobStore: NewBlobStore(db.DB, NewLocalObjectStore(t.TempDir()))})

	resp, err := service.Install(context.Background(), InstallRequest{MarketItemID: "market_item1", UserID: "admin_001", UserName: "管理员"})
	if err != nil {
		t.Fatalf("Install returned error: %v", err)
	}
	if resp.SkillID == "market_skill" {
		t.Fatalf("Install returned non-external source skill %q", resp.SkillID)
	}
	var installed testutil.SkillRow
	if err := db.Where("id = ?", resp.SkillID).Take(&installed).Error; err != nil {
		t.Fatalf("query installed skill: %v", err)
	}
	if installed.Category != "external" {
		t.Fatalf("installed category = %q, want external", installed.Category)
	}
	if got := testutil.CountRows(t, db, "skill_market_installs", "market_item_id = ? AND user_id = ? AND skill_id = ?", "market_item1", "admin_001", resp.SkillID); got != 1 {
		t.Fatalf("publisher install row count = %d, want 1", got)
	}
}

func TestMarketInstall_ReplacesLegacyPublisherSourceInstall(t *testing.T) {
	db := testutil.NewTestDB(t)
	testutil.SeedSkillWithRevision(t, db, "market_skill", "market_rev1")
	adminID := "admin_001"
	if err := db.Model(&testutil.SkillRow{}).Where("id = ?", "market_skill").Updates(map[string]any{
		"owner_user_id":    "admin_001",
		"owner_user_name":  "管理员",
		"create_user_id":   "admin_001",
		"create_user_name": "管理员",
	}).Error; err != nil {
		t.Fatalf("reassign market skill owner: %v", err)
	}
	testutil.MustCreate(t, db, &testutil.SkillMarketItemRow{
		ID:            "market_item1",
		SourceSkillID: "market_skill",
		Status:        "published",
		CreatedBy:     &adminID,
		CreatedAt:     testutil.TimeFixture(),
		UpdatedAt:     testutil.TimeFixture(),
	})
	testutil.MustCreate(t, db, &testutil.SkillMarketInstallRow{
		MarketItemID: "market_item1",
		UserID:       "admin_001",
		SkillID:      "market_skill",
		CreatedAt:    testutil.TimeFixture(),
		UpdatedAt:    testutil.TimeFixture(),
	})
	service := NewService(ServiceDeps{DB: db.DB, BlobStore: NewBlobStore(db.DB, NewLocalObjectStore(t.TempDir()))})

	resp, err := service.Install(context.Background(), InstallRequest{MarketItemID: "market_item1", UserID: "admin_001", UserName: "管理员"})
	if err != nil {
		t.Fatalf("Install returned error: %v", err)
	}
	if resp.SkillID == "market_skill" {
		t.Fatal("Install reused the legacy marketplace source")
	}
	if got := testutil.CountRows(t, db, "skill_market_installs", "market_item_id = ? AND user_id = ? AND skill_id = ?", "market_item1", "admin_001", resp.SkillID); got != 1 {
		t.Fatalf("corrected publisher install row count = %d, want 1", got)
	}
}
