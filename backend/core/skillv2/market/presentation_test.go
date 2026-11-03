package market

import (
	"context"
	skillservice "lazymind/core/skillv2/service"
	"lazymind/core/skillv2/testutil"
	"path/filepath"
	"reflect"
	"testing"
)

func TestMalformedPresentationFallsBackWholly(t *testing.T) {
	for _, raw := range []string{`{"zh-CN":{"name":"partial","description":7}}`, `{"zh-CN":{"name":"partial","tag_labels":{"research":7}}}`, `{"zh-CN":{"name":"partial"},"en-US":7}`, `{"zh-CN":{"name":"partial"}`} {
		t.Run(raw, func(t *testing.T) {
			got := ResolvePresentation([]byte(raw), "zh-CN", "canonical", "original")
			if got.Name != "canonical" || got.Description != "original" || len(got.TagLabels) != 0 {
				t.Fatalf("malformed presentation leaked fields: %#v", got)
			}
			if text := PresentationSearchText([]byte(raw)); text != "" {
				t.Fatalf("malformed presentation indexed: %q", text)
			}
		})
	}
}

func TestPublishedPresentationAndPartialEditLeaveInstalledPackageCanonical(t *testing.T) {
	ctx := context.Background()
	db := testutil.NewTestDB(t)
	objectRoot := t.TempDir()
	objects := NewLocalObjectStore(objectRoot)
	blobs := NewBlobStore(db.DB, objects)
	service := NewAdminService(AdminServiceDeps{DB: db.DB, BlobStore: blobs})
	zipPath := filepath.Join(t.TempDir(), "presentation.zip")
	canonical := "---\nname: original-skill\ndescription: Original package description\n---\n# Original instructions\n"
	testutil.WriteSkillZip(t, zipPath, map[string][]byte{"SKILL.md": []byte(canonical), "references/guide.txt": []byte("Untranslated reference\n")})
	published, err := service.Publish(ctx, PublishRequest{AdminUserID: "admin", Tags: []string{"research"}, DisplayName: "资料研究", DisplayDescription: "整理资料与证据", TagLabels: map[string]string{"research": "研究"}, Source: SourceInput{Type: "uploaded_zip", UploadID: "upload", StoredPath: zipPath}})
	if err != nil {
		t.Fatal(err)
	}
	var item testutil.SkillMarketItemRow
	if err := db.Where("id = ?", published.MarketItemID).Take(&item).Error; err != nil {
		t.Fatal(err)
	}
	value := ResolvePresentation(item.DisplayI18n, "zh-CN", "", "")
	if value.Name != "资料研究" || value.Description != "整理资料与证据" || value.TagLabels["research"] != "研究" {
		t.Fatalf("not persisted: %#v", value)
	}
	var source testutil.SkillRow
	if err := db.Where("id = ?", published.SourceSkillID).Take(&source).Error; err != nil {
		t.Fatal(err)
	}
	if source.SkillName != "original-skill" || source.Description != "Original package description" {
		t.Fatalf("canonical overwritten: %#v", source)
	}
	var beforeRevision testutil.SkillRevisionRow
	if err := db.Where("id = ?", *source.HeadRevisionID).Take(&beforeRevision).Error; err != nil {
		t.Fatal(err)
	}
	var beforeEntries []testutil.SkillRevisionEntryRow
	if err := db.Where("revision_id = ?", beforeRevision.ID).Order("path").Find(&beforeEntries).Error; err != nil {
		t.Fatal(err)
	}
	installed, err := service.Install(ctx, InstallRequest{MarketItemID: published.MarketItemID, UserID: "consumer", UserName: "Consumer"})
	if err != nil {
		t.Fatal(err)
	}
	var copy testutil.SkillRow
	if err := db.Where("id = ?", installed.SkillID).Take(&copy).Error; err != nil {
		t.Fatal(err)
	}
	if copy.SkillName != source.SkillName || copy.Description != source.Description || *copy.HeadRevisionID == *source.HeadRevisionID {
		t.Fatalf("installed metadata invalid: %#v", copy)
	}
	var copyRevision testutil.SkillRevisionRow
	if err := db.Where("id = ?", *copy.HeadRevisionID).Take(&copyRevision).Error; err != nil {
		t.Fatal(err)
	}
	if copyRevision.TreeHash != beforeRevision.TreeHash {
		t.Fatal("installation changed package tree")
	}
	reader := skillservice.NewSkillService(skillservice.SkillServiceDeps{DB: db.DB, BlobStore: skillservice.NewBlobStore(db.DB, skillservice.NewLocalObjectStore(objectRoot))})
	for _, skillID := range []string{source.ID, copy.ID} {
		file, err := reader.ReadFile(ctx, skillservice.FileRef{SkillID: skillID, RefType: "head", Path: "SKILL.md"})
		if err != nil || file.Content != canonical {
			t.Fatalf("canonical SKILL.md changed: %s %v", file.Content, err)
		}
	}
	newName := "证据研究"
	if _, err := service.Edit(ctx, EditRequest{AdminUserID: "admin", MarketItemID: published.MarketItemID, DisplayName: &newName}); err != nil {
		t.Fatal(err)
	}
	if err := db.Where("id = ?", item.ID).Take(&item).Error; err != nil {
		t.Fatal(err)
	}
	value = ResolvePresentation(item.DisplayI18n, "zh-CN", "", "")
	if value.Name != newName || value.Description != "整理资料与证据" || value.TagLabels["research"] != "研究" {
		t.Fatalf("partial edit lost data: %#v", value)
	}
	for _, before := range []testutil.SkillRow{source, copy} {
		var after testutil.SkillRow
		if err := db.Where("id = ?", before.ID).Take(&after).Error; err != nil {
			t.Fatal(err)
		}
		if *after.HeadRevisionID != *before.HeadRevisionID || after.SkillName != before.SkillName || after.Description != before.Description {
			t.Fatal("display edit changed canonical source or installation")
		}
	}
	var afterEntries []testutil.SkillRevisionEntryRow
	db.Where("revision_id = ?", beforeRevision.ID).Order("path").Find(&afterEntries)
	if !reflect.DeepEqual(beforeEntries, afterEntries) {
		t.Fatal("display edit changed package revision entries")
	}
}
