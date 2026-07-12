package plugin

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
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
}

func TestLoadPluginBuiltinSkillRejectsTraversal(t *testing.T) {
	t.Setenv("LAZYMIND_BUILTIN_SKILLS_DIR", t.TempDir())
	_, _, err := loadPluginBuiltinSkill("builtin:" + pluginBuiltinSkillManifests[0].UID + ":../secret")
	if !errors.Is(err, errPluginSourceSkillNotFound) {
		t.Fatalf("err=%v, want not found", err)
	}
}
