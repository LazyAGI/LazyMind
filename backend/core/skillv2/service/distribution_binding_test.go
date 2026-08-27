package service

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"gorm.io/gorm"
)

func TestCreateBuiltinSkillPersistsDistributionBaseline(t *testing.T) {
	db := newSkillV2TestDB(t)
	root := t.TempDir()
	zipPath := filepath.Join(root, "builtin.zip")
	writeSkillZip(t, zipPath, map[string][]byte{
		"SKILL.md": []byte("---\nname: demo\ndescription: demo skill\n---\n# Demo\n"),
		"guide.md": []byte("guide\n"),
	})
	service := NewSkillService(SkillServiceDeps{DB: db, BlobStore: NewBlobStore(db, NewLocalObjectStore(root)), Clock: fixedClock()})
	response, err := service.CreateSkill(context.Background(), CreateSkillRequest{
		OwnerUserID: "user_001", CreateUserID: "user_001", Name: "demo", Category: "research", Description: "demo skill",
		OriginBuiltinSkillUID: "bsk_demo", Source: SourceInput{Type: "builtin_zip", StoredPath: zipPath, Filename: "bsk_demo@1.0.0#" + strings.Repeat("a", 64)},
		Distribution: &DistributionSource{BuiltinUID: "bsk_demo", Version: "1.0.0", ArchiveSHA256: strings.Repeat("a", 64), TreeSHA256: strings.Repeat("b", 64)},
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, check := range []struct {
		table string
		where string
		args  []any
	}{
		{table: "skill_distribution_artifacts", where: "archive_sha256 = ?", args: []any{strings.Repeat("a", 64)}},
		{table: "skill_distribution_bindings", where: "skill_id = ?", args: []any{response.SkillID}},
		{table: "skill_distribution_entries", where: "archive_sha256 = ?", args: []any{strings.Repeat("a", 64)}},
		{table: "skill_revision_distributions", where: "revision_id = ?", args: []any{response.HeadRevisionID}},
	} {
		var count int64
		if err := db.Table(check.table).Where(check.where, check.args...).Count(&count).Error; err != nil {
			t.Fatal(err)
		}
		if count == 0 {
			t.Fatalf("%s has no baseline rows", check.table)
		}
	}
}

func TestCreateRenamedBuiltinSkillKeepsOfficialArtifact(t *testing.T) {
	db := newSkillV2TestDB(t)
	root := t.TempDir()
	official := []byte("---\nname: demo\ndescription: demo skill\ncategory: research\n---\n# Demo\n")
	localName := "demo-bskdemo"
	rewritten := []byte(RewriteSkillMDName(string(official), localName))
	zipPath := filepath.Join(root, "renamed.zip")
	writeSkillZip(t, zipPath, map[string][]byte{"SKILL.md": rewritten})
	archiveSHA := strings.Repeat("a", 64)
	service := NewSkillService(SkillServiceDeps{DB: db, BlobStore: NewBlobStore(db, NewLocalObjectStore(root)), Clock: fixedClock()})
	response, err := service.CreateSkill(context.Background(), CreateSkillRequest{
		OwnerUserID: "user_001", CreateUserID: "user_001", Name: localName, Category: "research", Description: "demo skill",
		OriginBuiltinSkillUID: "bsk_demo", Source: SourceInput{Type: "local_zip", StoredPath: zipPath, Filename: "bsk_demo.zip"},
		Distribution: &DistributionSource{
			BuiltinUID: "bsk_demo", Version: "1.0.0", ArchiveSHA256: archiveSHA, TreeSHA256: strings.Repeat("b", 64),
			OfficialFiles: map[string][]byte{"SKILL.md": official},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	file, err := service.ReadFile(context.Background(), FileRef{SkillID: response.SkillID, RefType: "head", Path: "SKILL.md"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(file.Content, "name: "+localName+"\n") {
		t.Fatalf("local SKILL.md was not renamed:\n%s", file.Content)
	}
	if got := distributionSkillMD(t, db, archiveSHA); !strings.Contains(got, "name: demo\n") || strings.Contains(got, localName) {
		t.Fatalf("official artifact was polluted:\n%s", got)
	}
}

func TestRewriteSkillMDFrontmatterPreservesFieldOrder(t *testing.T) {
	original := "---\nname: demo\ndescription: demo skill\ncategory: research\nversion: 1.0.0\n---\n# Demo\n"
	got := RewriteSkillMDFrontmatter(original, "demo-copy", "research", "demo skill")
	wantPrefix := "---\nname: demo-copy\ndescription: demo skill\ncategory: research\nversion: 1.0.0\n---\n"
	if !strings.HasPrefix(got, wantPrefix) {
		t.Fatalf("rewritten frontmatter = %q, want prefix %q", got, wantPrefix)
	}
}

func TestRewriteSkillMDNameOnlyChangesName(t *testing.T) {
	original := "---\n" +
		"name: cangjie-skill\n" +
		"description: |\n" +
		"  Distill a book into a coherent set of executable skills.\n" +
		"  Use when the user asks to \"拆书\".\n" +
		"# keep this comment\n" +
		"license: MIT\n" +
		"compatibility: claude\n" +
		"---\n" +
		"# Cangjie\n" +
		"body line\n"
	got := RewriteSkillMDName(original, "cangjie-skill-copy")
	want := strings.Replace(original, "name: cangjie-skill\n", "name: cangjie-skill-copy\n", 1)
	if got != want {
		t.Fatalf("rewritten = %q, want %q", got, want)
	}
}

func TestRewriteSkillMDNameKeepsCRLFQuotesAndComment(t *testing.T) {
	original := "---\r\nname: \"demo\" # local copy\r\ndescription: 'hello world'\r\n---\r\n# Demo\r\n"
	got := RewriteSkillMDName(original, "demo-copy")
	want := "---\r\nname: \"demo-copy\" # local copy\r\ndescription: 'hello world'\r\n---\r\n# Demo\r\n"
	if got != want {
		t.Fatalf("rewritten = %q, want %q", got, want)
	}
}

func distributionSkillMD(t *testing.T, db *gorm.DB, archiveSHA string) string {
	t.Helper()
	var entry struct {
		BlobHash *string `gorm:"column:blob_hash"`
	}
	if err := db.Table("skill_distribution_entries").Select("blob_hash").Where("archive_sha256 = ? AND path = ?", archiveSHA, "SKILL.md").Take(&entry).Error; err != nil {
		t.Fatal(err)
	}
	if entry.BlobHash == nil {
		t.Fatal("distribution SKILL.md blob hash is empty")
	}
	var blob struct {
		Content []byte `gorm:"column:content"`
	}
	if err := db.Table("skill_blobs").Where("hash = ?", *entry.BlobHash).Take(&blob).Error; err != nil {
		t.Fatal(err)
	}
	return string(blob.Content)
}
