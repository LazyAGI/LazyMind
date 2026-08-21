package builtin

import (
	"path/filepath"
	"testing"
)

// TestTemplateID prefixes the UID with the builtin prefix.
func TestTemplateID(t *testing.T) {
	got := TemplateID("abc123")
	want := "builtin:abc123"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

// TestTemplateID_TrimsWhitespace trims whitespace from the input UID.
func TestTemplateID_TrimsWhitespace(t *testing.T) {
	got := TemplateID("  uid-1  ")
	want := "builtin:uid-1"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

// TestIsTemplateID detects the builtin prefix.
func TestIsTemplateID(t *testing.T) {
	tests := []struct {
		id   string
		want bool
	}{
		{"builtin:abc", true},
		{"builtin:", true},
		{"normal-id", false},
		{"", false},
		{"BUILTIN:abc", false},
	}
	for _, tt := range tests {
		t.Run(tt.id, func(t *testing.T) {
			if got := IsTemplateID(tt.id); got != tt.want {
				t.Fatalf("got %v, want %v", got, tt.want)
			}
		})
	}
}

// TestParseSkillMDMetadata extracts name and description from YAML frontmatter.
func TestParseSkillMDMetadata(t *testing.T) {
	content := "---\nname: My Skill\ndescription: A test skill\n---\n\n# Body content"
	name, desc := parseSkillMDMetadata(content)
	if name != "My Skill" || desc != "A test skill" {
		t.Fatalf("got name=%q desc=%q", name, desc)
	}
}

// TestParseSkillMDMetadata_NoFrontmatter returns empty strings.
func TestParseSkillMDMetadata_NoFrontmatter(t *testing.T) {
	name, desc := parseSkillMDMetadata("# Just a heading")
	if name != "" || desc != "" {
		t.Fatalf("got name=%q desc=%q, want empty", name, desc)
	}
}

// TestParseSkillMDMetadata_MissingClosing returns empty strings.
func TestParseSkillMDMetadata_MissingClosing(t *testing.T) {
	content := "---\nname: Test\n# no closing ---"
	name, desc := parseSkillMDMetadata(content)
	if name != "" || desc != "" {
		t.Fatalf("got name=%q desc=%q, want empty", name, desc)
	}
}

// TestParseSkillMDMetadata_EmptyFrontmatter returns empty strings.
func TestParseSkillMDMetadata_EmptyFrontmatter(t *testing.T) {
	content := "---\n---\nbody"
	name, desc := parseSkillMDMetadata(content)
	if name != "" || desc != "" {
		t.Fatalf("got name=%q desc=%q, want empty", name, desc)
	}
}

// TestParseSkillMDMetadata_WindowsLineEndings handles CRLF.
func TestParseSkillMDMetadata_WindowsLineEndings(t *testing.T) {
	content := "---\r\nname: Win Skill\r\ndescription: CRLF test\r\n---\r\n\r\nBody"
	name, desc := parseSkillMDMetadata(content)
	if name != "Win Skill" || desc != "CRLF test" {
		t.Fatalf("got name=%q desc=%q", name, desc)
	}
}

// TestParseSkillMDMetadata_TrimsWhitespace trims name and description.
func TestParseSkillMDMetadata_TrimsWhitespace(t *testing.T) {
	content := "---\nname:   Padded   \ndescription:   Desc with spaces   \n---\nbody"
	name, desc := parseSkillMDMetadata(content)
	if name != "Padded" || desc != "Desc with spaces" {
		t.Fatalf("got name=%q desc=%q", name, desc)
	}
}

func TestThirdPartyBuiltinSkillPackages(t *testing.T) {
	root, err := filepath.Abs("../../../../skills")
	if err != nil {
		t.Fatalf("resolve builtin skill root: %v", err)
	}
	t.Setenv("LAZYMIND_BUILTIN_SKILLS_DIR", root)

	tests := []struct {
		dirName string
		files   []string
	}{
		{
			dirName: "hot-news-summary",
			files: []string{
				"SKILL.md",
				"references/output-format.md",
				"scripts/hot_list_fetcher.py",
			},
		},
		{
			dirName: "resume-assistant",
			files: []string{
				"README.md",
				"README_ZH.md",
				"SKILL.md",
				"SKILL_ZH.md",
				"_meta.json",
				"examples/sample-resume-en.md",
				"examples/sample-resume-weak.md",
				"examples/sample-resume-zh.md",
				"examples/usage.md",
				"prompts/customize.md",
				"prompts/export.md",
				"prompts/polish.md",
				"prompts/score.md",
				"prompts/system.md",
				"skill.json",
				"skill.yaml",
				"templates/academic.md",
				"templates/export/resume.html",
				"templates/minimal.md",
				"templates/modern.md",
				"templates/professional.md",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.dirName, func(t *testing.T) {
			var manifest *Manifest
			for i := range Manifests {
				if Manifests[i].DirName == tt.dirName {
					manifest = &Manifests[i]
					break
				}
			}
			if manifest == nil {
				t.Fatalf("manifest for %s not found", tt.dirName)
			}
			pkg, err := LoadPackage(*manifest)
			if err != nil {
				t.Fatalf("load package: %v", err)
			}
			if pkg.Name == "" || pkg.Description == "" {
				t.Fatalf("frontmatter metadata missing: name=%q description=%q", pkg.Name, pkg.Description)
			}
			if len(pkg.Files) != len(tt.files) {
				t.Fatalf("package contains %d files, want %d: %#v", len(pkg.Files), len(tt.files), pkg.Files)
			}
			for _, filePath := range tt.files {
				if _, ok := pkg.Files[filePath]; !ok {
					t.Errorf("package missing %s", filePath)
				}
			}
		})
	}
}
