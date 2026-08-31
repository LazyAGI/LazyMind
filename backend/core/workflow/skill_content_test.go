package workflow

import (
	"archive/zip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	skillbuiltin "lazymind/core/skillv2/builtin"
	skillpackage "lazymind/core/skillv2/skillpackage"
)

func TestLoadWorkflowBuiltinSkill(t *testing.T) {
	uid := useWorkflowBuiltinCatalog(t)
	content, name, err := loadWorkflowBuiltinSkill("builtin:" + uid)
	if err != nil || content == "" || name != "deep-research" {
		t.Fatalf("parent content=%q name=%q err=%v", content, name, err)
	}
	content, name, err = loadWorkflowBuiltinSkill("builtin:" + uid + ":references/guide.md")
	if err != nil || content != "guide" || name != "references/guide" {
		t.Fatalf("child content=%q name=%q err=%v", content, name, err)
	}
	snapshot, err := loadWorkflowBuiltinSkillPackage("builtin:" + uid)
	if err != nil || snapshot.TreeHash == "" || len(snapshot.Files) != 2 {
		t.Fatalf("builtin snapshot=%#v err=%v", snapshot, err)
	}
}

func TestLoadWorkflowBuiltinSkillRejectsTraversal(t *testing.T) {
	_, _, err := loadWorkflowBuiltinSkill("builtin:bsk_missing:../secret")
	if !errors.Is(err, errWorkflowSourceSkillNotFound) {
		t.Fatalf("err=%v, want not found", err)
	}
}

func useWorkflowBuiltinCatalog(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	workingDirectory := filepath.Join(root, "backend", "core")
	catalogDirectory := filepath.Join(root, "skills", ".runtime", "builtin-skills")
	packageDirectory := filepath.Join(catalogDirectory, "packages")
	if err := os.MkdirAll(workingDirectory, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(packageDirectory, 0o755); err != nil {
		t.Fatal(err)
	}
	files := map[string][]byte{
		"SKILL.md":            []byte("---\nname: deep-research\ndescription: research\n---\nbody"),
		"references/guide.md": []byte("guide"),
	}
	archivePath := filepath.Join(packageDirectory, "workflow.zip")
	archive, err := os.Create(archivePath)
	if err != nil {
		t.Fatal(err)
	}
	writer := zip.NewWriter(archive)
	for _, name := range []string{"SKILL.md", "references/guide.md"} {
		entry, err := writer.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := entry.Write(files[name]); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if err := archive.Close(); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(archivePath)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(body)
	uid := "bsk_workflow"
	catalog := skillbuiltin.Catalog{SchemaVersion: skillbuiltin.CatalogSchemaVersion, Skills: []skillbuiltin.CatalogSkill{{
		Key: "deep-research", UID: uid, SourceURL: "builtin://research/deep-research", ResolvedURL: "builtin://research/deep-research",
		Version: "1.0.0", Name: "deep-research", Description: "research", Category: "research",
		ArchiveSHA256: hex.EncodeToString(digest[:]), TreeSHA256: skillpackage.TreeHash(files), ArchiveSize: int64(len(body)), PackageFile: "packages/workflow.zip",
	}}}
	catalogBody, err := json.Marshal(catalog)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(catalogDirectory, "catalog.json"), catalogBody, 0o644); err != nil {
		t.Fatal(err)
	}
	previous, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(workingDirectory); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(previous) })
	return uid
}

func TestLoadWorkflowSourceSkillPinsHeadAndLoadsWholePackage(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file::memory:?cache=shared"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	for _, sql := range []string{
		`CREATE TABLE skills(id text primary key, owner_user_id text, skill_name text, head_revision_id text, deleted_at datetime)`,
		`CREATE TABLE skill_revisions(id text primary key, skill_id text, revision_no integer, tree_hash text)`,
		`CREATE TABLE skill_revision_entries(revision_id text, path text, entry_type text, blob_hash text, size integer, mime text, file_type text, binary boolean)`,
		`CREATE TABLE skill_blobs(hash text primary key, content blob)`,
		`INSERT INTO skills VALUES('s1','u1','Package Skill','r2',NULL)`,
		`INSERT INTO skill_revisions VALUES('r1','s1',1,'old')`,
		`INSERT INTO skill_revisions VALUES('r2','s1',2,'tree2')`,
		`INSERT INTO skill_blobs VALUES('h1', '# Skill\nDo the workflow.')`,
		`INSERT INTO skill_blobs VALUES('h2', 'def run(value): return value')`,
		`INSERT INTO skill_revision_entries VALUES('r2','SKILL.md','file','h1',24,'text/markdown','markdown',0)`,
		`INSERT INTO skill_revision_entries VALUES('r2','scripts/run.py','file','h2',28,'text/x-python','text',0)`,
	} {
		if err := db.Exec(sql).Error; err != nil {
			t.Fatal(err)
		}
	}
	snapshot, err := loadWorkflowSourceSkill(context.Background(), db, "u1", "s1")
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.RevisionID != "r2" || snapshot.RevisionNo != 2 || snapshot.TreeHash != "tree2" {
		t.Fatalf("wrong pinned revision: %#v", snapshot)
	}
	if len(snapshot.Files) != 2 || snapshot.Files[1].Path != "scripts/run.py" {
		t.Fatalf("whole package not loaded: %#v", snapshot.Files)
	}
}

func TestWorkflowSourceSkillEntryQueryQuotesBinaryColumn(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file::memory:?cache=shared"), &gorm.Config{DryRun: true})
	if err != nil {
		t.Fatal(err)
	}
	var entries []struct {
		Binary bool `gorm:"column:binary"`
	}
	stmt := db.Table("skill_revision_entries").
		Select(`path, blob_hash, size, mime, file_type, "binary"`).
		Where("revision_id = ? AND entry_type = ?", "r1", "file").
		Order("path ASC").
		Scan(&entries).Statement
	if !strings.Contains(stmt.SQL.String(), `"binary"`) {
		t.Fatalf("binary column is not quoted in SQL: %s", stmt.SQL.String())
	}
}
