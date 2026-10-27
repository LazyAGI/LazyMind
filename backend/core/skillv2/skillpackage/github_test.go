package skillpackage

import (
	"fmt"
	"strings"
	"testing"
)

func TestReadGitHubZipSelectsOnlyUniqueNestedSkill(t *testing.T) {
	entries := map[string]string{
		"repo-main/README.md":                  "repository documentation",
		"repo-main/skills/demo/skill.md":       "skill document",
		"repo-main/skills/demo/scripts/run.sh": "echo demo",
	}
	for i := 0; i < MaxFiles+1; i++ {
		entries[fmt.Sprintf("repo-main/unrelated/%d.txt", i)] = "irrelevant"
	}
	pkg, err := ReadGitHubZip(writeZip(t, entries), "")
	if err != nil {
		t.Fatal(err)
	}
	if pkg.PackageRoot != "demo" || len(pkg.Files) != 2 || string(pkg.Files["SKILL.md"]) != "skill document" || string(pkg.Files["scripts/run.sh"]) != "echo demo" {
		t.Fatalf("wrong selected skill: %#v", pkg)
	}
}

func TestReadGitHubZipRejectsMultipleSkillsIncludingRootSkill(t *testing.T) {
	for _, entries := range []map[string]string{
		{"repo-main/skills/one/SKILL.md": "one", "repo-main/skills/two/SKILL.md": "two"},
		{"repo-main/SKILL.md": "root", "repo-main/skills/two/SKILL.md": "two"},
	} {
		archive := writeZip(t, entries)
		if _, err := ReadGitHubZip(archive, ""); err == nil || !strings.Contains(err.Error(), "multiple skills") {
			t.Fatalf("error = %v, want actionable multiple-skill rejection", err)
		}
		pkg, err := ReadGitHubZip(archive, "skills/two")
		if err != nil || len(pkg.Files) != 1 || string(pkg.Files["SKILL.md"]) != "two" {
			t.Fatalf("explicit selection failed: %#v, %v", pkg, err)
		}
	}
}

func TestReadGitHubZipMissingDocumentAndUnsafePaths(t *testing.T) {
	for _, entries := range []map[string]string{
		{"repo-main/README.md": "no skill"},
		{"repo-main/skills/one/SKILL.md": "one", "../escape": "unsafe"},
	} {
		if _, err := ReadGitHubZip(writeZip(t, entries), ""); err == nil {
			t.Fatal("expected invalid repository archive to fail")
		}
	}
}
