package market

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"lazymind/core/skillv2/testutil"
)

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
	if copied.Category != "External" || copied.RelativeRoot != "External/论文精读-market_skill" {
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

func TestMarketAdminPublishEditUnpublish(t *testing.T) {
	db := testutil.NewTestDB(t)
	service := NewAdminService(AdminServiceDeps{DB: db.DB, BlobStore: NewBlobStore(db.DB, NewLocalObjectStore(t.TempDir()))})

	published, err := service.Publish(context.Background(), PublishRequest{
		AdminUserID: "admin_001",
		Name:        "论文精读",
		Tags:        []string{"research", "paper"},
		Source: SourceInput{
			Type:     "uploaded_zip",
			UploadID: "upload_market_zip",
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
	if source.Category != "External" {
		t.Fatalf("published source category = %q, want External", source.Category)
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

func TestMarketAdminPublish_AllowsSingleTopLevelDirectory(t *testing.T) {
	db := testutil.NewTestDB(t)
	zipPath := filepath.Join(t.TempDir(), "wrapped.zip")
	testutil.WriteSkillZip(t, zipPath, map[string][]byte{
		"openclaw-openclaw-changelog-update/SKILL.md":        []byte("---\nname: openclaw-changelog\ndescription: OpenClaw changelog skill\n---\n# OpenClaw\n"),
		"openclaw-openclaw-changelog-update/references/a.md": []byte("# A\n"),
	})
	service := NewAdminService(AdminServiceDeps{DB: db.DB, BlobStore: NewBlobStore(db.DB, NewLocalObjectStore(t.TempDir()))})

	published, err := service.Publish(context.Background(), PublishRequest{
		AdminUserID: "admin_001",
		Name:        "openclaw-openclaw-changelog-update",
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
	if skill.Category != "External" {
		t.Fatalf("category = %q, want External", skill.Category)
	}
	if skill.SkillName != "openclaw-changelog" {
		t.Fatalf("skill_name = %q, want canonical SKILL.md name", skill.SkillName)
	}
	if skill.RelativeRoot != "External/openclaw-changelog" {
		t.Fatalf("relative_root = %q, want External/openclaw-changelog", skill.RelativeRoot)
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
	var skillMDBlob skillBlobRow
	if err := db.Where("hash = ?", *skillMDEntry.BlobHash).Take(&skillMDBlob).Error; err != nil {
		t.Fatalf("query SKILL.md blob: %v", err)
	}
	if skillMDBlob.StorageBackend != "postgres" || len(skillMDBlob.Content) == 0 || skillMDBlob.StorageKey != nil {
		t.Fatalf("SKILL.md blob storage invalid: %#v", skillMDBlob)
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
		Name:        "first filename",
		Tags:        []string{"debugging"},
		Source:      SourceInput{Type: "uploaded_zip", StoredPath: firstZip},
	}); err != nil {
		t.Fatalf("first Publish returned error: %v", err)
	}
	if _, err := service.Publish(context.Background(), PublishRequest{
		AdminUserID: "admin_002",
		Name:        "different filename",
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
		"category":      "External",
		"skill_name":    "论文精读-market_skill",
		"relative_root": "External/论文精读-market_skill",
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
		"category":         "External",
		"relative_root":    "External/论文精读-market_skill",
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
		t.Fatalf("Install returned non-External source skill %q", resp.SkillID)
	}
	var installed testutil.SkillRow
	if err := db.Where("id = ?", resp.SkillID).Take(&installed).Error; err != nil {
		t.Fatalf("query installed skill: %v", err)
	}
	if installed.Category != "External" {
		t.Fatalf("installed category = %q, want External", installed.Category)
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
