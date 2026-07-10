package handler

import (
	"strings"
	"testing"

	"lazymind/core/common/orm"
)

func TestEnsureSkillMDFrontmatterAddsMissingCategory(t *testing.T) {
	row := orm.SkillV2Skill{
		ID:          "skill1",
		SkillName:   "deep-research",
		Category:    "research",
		Description: "Deep research skill.",
	}
	content := "---\nname: deep-research\ndescription: Deep research skill.\n---\n# Deep Research\n"

	got := ensureSkillMDFrontmatter(content, row)

	for _, want := range []string{
		"name: deep-research",
		"category: research",
		"description: Deep research skill.",
		"# Deep Research",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("normalized content missing %q:\n%s", want, got)
		}
	}
}

func TestEnsureSkillMDFrontmatterWrapsContentWithoutFrontmatter(t *testing.T) {
	row := orm.SkillV2Skill{
		ID:          "skill1",
		SkillName:   "deep-research",
		Category:    "research",
		Description: "Deep research skill.",
	}

	got := ensureSkillMDFrontmatter("# Deep Research\n\nUse this skill.", row)

	if !strings.HasPrefix(got, "---\n") {
		t.Fatalf("content should start with frontmatter:\n%s", got)
	}
	for _, want := range []string{
		"name: deep-research",
		"category: research",
		"description: Deep research skill.",
		"# Deep Research",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("normalized content missing %q:\n%s", want, got)
		}
	}
}

func TestEnsureSkillMDFrontmatterLeavesCompleteContentUnchanged(t *testing.T) {
	row := orm.SkillV2Skill{
		ID:          "skill1",
		SkillName:   "deep-research",
		Category:    "research",
		Description: "Deep research skill.",
	}
	content := "---\nname: custom\ncategory: custom-category\ndescription: Custom description.\n---\n# Custom\n"

	got := ensureSkillMDFrontmatter(content, row)

	if got != content {
		t.Fatalf("complete frontmatter should be unchanged:\n%s", got)
	}
}
