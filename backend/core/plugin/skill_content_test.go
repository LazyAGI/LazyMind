package plugin

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestLoadPluginBuiltinSkill(t *testing.T) {
	root := t.TempDir()
	manifest := pluginBuiltinSkillManifests[0]
	dir := filepath.Join(root, manifest.Category, manifest.DirName)
	if err := os.MkdirAll(filepath.Join(dir, "references"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("---\nname: deep-research\n---\nbody"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "references", "guide.md"), []byte("guide"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("LAZYMIND_BUILTIN_SKILLS_DIR", root)
	content, name, err := loadPluginBuiltinSkill("builtin:" + manifest.UID)
	if err != nil || content == "" || name != "deep-research" {
		t.Fatalf("parent content=%q name=%q err=%v", content, name, err)
	}
	content, name, err = loadPluginBuiltinSkill("builtin:" + manifest.UID + ":references/guide.md")
	if err != nil || content != "guide" || name != "references/guide" {
		t.Fatalf("child content=%q name=%q err=%v", content, name, err)
	}
	snapshot, err := loadPluginBuiltinSkillPackage("builtin:" + manifest.UID)
	if err != nil || snapshot.TreeHash == "" || len(snapshot.Files) != 2 {
		t.Fatalf("builtin snapshot=%#v err=%v", snapshot, err)
	}
}

func TestLoadPluginBuiltinSkillRejectsTraversal(t *testing.T) {
	t.Setenv("LAZYMIND_BUILTIN_SKILLS_DIR", t.TempDir())
	_, _, err := loadPluginBuiltinSkill("builtin:" + pluginBuiltinSkillManifests[0].UID + ":../secret")
	if !errors.Is(err, errPluginSourceSkillNotFound) {
		t.Fatalf("err=%v, want not found", err)
	}
}

func TestLoadPluginSourceSkillPinsHeadAndLoadsWholePackage(t *testing.T) {
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
	snapshot, err := loadPluginSourceSkill(context.Background(), db, "u1", "s1")
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

func TestPluginSourceSkillEntryQueryQuotesBinaryColumn(t *testing.T) {
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
