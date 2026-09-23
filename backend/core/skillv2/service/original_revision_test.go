package service

import (
	"context"
	"path/filepath"
	"testing"
)

func TestMetadataRevisionPreservesEarliestOriginal(t *testing.T) {
	db := newSkillV2TestDB(t)
	seedSkillWithHeadRevision(t, db, "skill1", "rev1")
	svc := NewSkillService(SkillServiceDeps{DB: db, BlobStore: NewBlobStore(db, NewLocalObjectStore(t.TempDir())), Clock: fixedClock()})
	for _, desc := range []string{"first update", "second update"} {
		if _, err := svc.PatchSkill(context.Background(), PatchSkillRequest{SkillID: "skill1", UserID: "user_001", Description: &desc}); err != nil {
			t.Fatal(err)
		}
	}
	var row struct{ OriginalRevisionID *string }
	if err := db.Table("skills").Select("original_revision_id").Where("id = ?", "skill1").Take(&row).Error; err != nil {
		t.Fatal(err)
	}
	if row.OriginalRevisionID == nil || *row.OriginalRevisionID != "rev1" {
		t.Fatalf("original=%v", row.OriginalRevisionID)
	}
	file, err := svc.ReadFile(context.Background(), FileRef{SkillID: "skill1", RefType: "original", Path: "SKILL.md"})
	if err != nil || file.Content == "" {
		t.Fatalf("original file=%v error=%v", file, err)
	}
}

func TestSearchMetadataPatchPreservesExecutionDocument(t *testing.T) {
	db := newSkillV2TestDB(t)
	seedSkillWithHeadRevision(t, db, "skill1", "rev1")
	svc := NewSkillService(SkillServiceDeps{DB: db, BlobStore: NewBlobStore(db, NewLocalObjectStore(t.TempDir())), Clock: fixedClock()})
	field := "writing"
	aliases := []string{"论文写作"}
	keywords := []string{"润色"}
	resp, err := svc.PatchSkill(context.Background(), PatchSkillRequest{SkillID: "skill1", UserID: "user_001", Field: &field, Aliases: &aliases, Keywords: &keywords})
	if err != nil {
		t.Fatal(err)
	}
	if resp.HeadRevisionID != "rev1" {
		t.Fatalf("search metadata created execution revision: %v", resp)
	}
	detail, err := svc.GetSkill(context.Background(), GetSkillRequest{SkillID: "skill1", UserID: "user_001"})
	if err != nil || detail.Field != "writing" || len(detail.Aliases) != 1 || len(detail.Keywords) != 1 {
		t.Fatalf("metadata=%v err=%v", detail, err)
	}
	description := "new description"
	if _, err := svc.PatchSkill(context.Background(), PatchSkillRequest{SkillID: "skill1", UserID: "user_001", Description: &description}); err != nil {
		t.Fatal(err)
	}
	detail, err = svc.GetSkill(context.Background(), GetSkillRequest{SkillID: "skill1", UserID: "user_001"})
	if err != nil || detail.Field != "writing" || len(detail.Aliases) != 1 {
		t.Fatalf("unrelated revision erased metadata: %v %v", detail, err)
	}
}

func TestImportedSearchMetadataSurvivesLaterExecutionRevision(t *testing.T) {
	db := newSkillV2TestDB(t)
	zipPath := filepath.Join(t.TempDir(), "metadata.zip")
	content := "---\nname: academic-writing\ndescription: Write papers\nfield: writing\naliases: [original-alias]\nkeywords: [original-keyword]\ntags: [original-tag]\n---\nWrite a paper.\n"
	writeSkillZip(t, zipPath, map[string][]byte{"SKILL.md": []byte(content)})
	svc := NewSkillService(SkillServiceDeps{DB: db, Downloader: NewFakeZipDownloader(map[string]string{"https://example.test/metadata.zip": zipPath}), BlobStore: NewBlobStore(db, NewLocalObjectStore(t.TempDir())), Clock: fixedClock()})
	created, err := svc.CreateSkill(context.Background(), CreateSkillRequest{OwnerUserID: "user_001", CreateUserID: "user_001", Source: SourceInput{Type: "url", URL: "https://example.test/metadata.zip"}})
	if err != nil {
		t.Fatal(err)
	}
	detail, err := svc.GetSkill(context.Background(), GetSkillRequest{SkillID: created.SkillID, UserID: "user_001"})
	if err != nil || detail.OriginalRevisionID != created.HeadRevisionID || detail.Field != "writing" || len(detail.Aliases) != 1 || detail.Aliases[0] != "original-alias" {
		t.Fatalf("import metadata=%v err=%v", detail, err)
	}
	field := "science"
	aliases := []string{"edited-alias"}
	keywords := []string{"edited-keyword"}
	tags := []string{"edited-tag"}
	if _, err := svc.PatchSkill(context.Background(), PatchSkillRequest{SkillID: created.SkillID, UserID: "user_001", Field: &field, Aliases: &aliases, Keywords: &keywords, Tags: &tags}); err != nil {
		t.Fatal(err)
	}
	description := "Improved description"
	if _, err := svc.PatchSkill(context.Background(), PatchSkillRequest{SkillID: created.SkillID, UserID: "user_001", Description: &description}); err != nil {
		t.Fatal(err)
	}
	detail, err = svc.GetSkill(context.Background(), GetSkillRequest{SkillID: created.SkillID, UserID: "user_001"})
	if err != nil || detail.Field != "science" || len(detail.Aliases) != 1 || detail.Aliases[0] != "edited-alias" || detail.Tags[0] != "edited-tag" || detail.Keywords[0] != "edited-keyword" {
		t.Fatalf("execution revision overwrote search metadata: %v err=%v", detail, err)
	}
	original, err := svc.ReadFile(context.Background(), FileRef{SkillID: created.SkillID, RefType: "original", Path: "SKILL.md"})
	if err != nil || original.Content != content {
		t.Fatalf("original changed: %s err=%v", original.Content, err)
	}
}
