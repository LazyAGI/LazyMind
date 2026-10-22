package builtin

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPresentationIndependentAndFallback(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "catalog.json")
	p, err := LoadPresentation(path)
	if err != nil {
		t.Fatal(err)
	}
	n, d, l := p.Display("uid", "zh-CN", "Name", "Description")
	if n != "Name" || d != "Description" || l != "en-US" {
		t.Fatal("canonical fallback lost")
	}
	os.WriteFile(filepath.Join(root, "presentation.json"), []byte(`{"schema_version":1,"skills":{"uid":{"presentation_id":"stable","locales":{"zh-CN":{"name":"名称","description":"描述"}}}},"tag_labels":{"zh-CN":{"tools":"工具"}}}`), 0644)
	p, err = LoadPresentation(path)
	if err != nil {
		t.Fatal(err)
	}
	n, d, l = p.Display("uid", "zh-CN", "Name", "Description")
	if n != "名称" || d != "描述" || l != "zh-CN" {
		t.Fatal("translation missing")
	}
	n, _, _ = p.Display("uid", "en-US", "Name", "Description")
	if n != "Name" {
		t.Fatal("English fallback lost")
	}
}

func TestPresentationSearchTextIncludesAllLocalesRegardlessDisplay(t *testing.T) {
	p := Presentation{Skills: map[string]SkillPresentation{"uid": {Locales: map[string]PresentationText{"zh-CN": {Name: "深度研究", Description: "核对资料"}, "en-US": {Name: "Deep Research", Description: "Verify evidence"}}}}}
	name, _, _ := p.Display("uid", "en-US", "canonical", "original")
	if name != "Deep Research" {
		t.Fatal("English display changed")
	}
	if got := p.SearchText("uid"); got != "Deep Research Verify evidence 深度研究 核对资料" {
		t.Fatalf("search text=%q", got)
	}
	if got := p.SearchText("missing"); got != "" {
		t.Fatalf("missing search=%q", got)
	}
}
