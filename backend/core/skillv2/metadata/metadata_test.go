package metadata

import (
	"strings"
	"testing"
)

func TestParseRequired(t *testing.T) {
	meta, err := ParseRequired([]byte("---\nname: imported-skill\ndescription: Imported description\ncategory: ignored\n---\n# Skill\n"))
	if err != nil {
		t.Fatalf("ParseRequired returned error: %v", err)
	}
	if meta.Name != "imported-skill" || meta.Description != "Imported description" {
		t.Fatalf("ParseRequired metadata = %#v", meta)
	}
}

func TestParseRequiredRejectsMissingFields(t *testing.T) {
	for name, content := range map[string]string{
		"frontmatter":  "# Skill\n",
		"name":         "---\ndescription: description\n---\n# Skill\n",
		"description":  "---\nname: skill\n---\n# Skill\n",
		"invalid name": "---\nname: bad/name\ndescription: description\n---\n# Skill\n",
	} {
		t.Run(name, func(t *testing.T) {
			_, err := ParseRequired([]byte(content))
			if err == nil {
				t.Fatal("ParseRequired succeeded")
			}
			if !strings.Contains(err.Error(), strings.Split(name, " ")[0]) {
				t.Fatalf("ParseRequired error = %q", err)
			}
		})
	}
}

func TestParseAllowsMissingFrontmatterFields(t *testing.T) {
	parsed, err := Parse([]byte("---\ndescription: Imported description\n---\n# Skill\n\n正文首段。\n"))
	if err != nil {
		t.Fatalf("Parse returned error: %v", err)
	}
	if parsed.HasName || !parsed.HasDescription {
		t.Fatalf("Parse presence = name:%v description:%v", parsed.HasName, parsed.HasDescription)
	}
	if parsed.Description != "Imported description" || parsed.Body != "# Skill\n\n正文首段。\n" {
		t.Fatalf("Parse result = %#v", parsed)
	}

	parsed, err = Parse([]byte("# Skill\r\n\r\n正文首段。\r\n"))
	if err != nil {
		t.Fatalf("Parse without frontmatter returned error: %v", err)
	}
	if parsed.HasName || parsed.HasDescription || parsed.Body != "# Skill\n\n正文首段。\n" {
		t.Fatalf("Parse without frontmatter result = %#v", parsed)
	}
}

func TestFirstBodyParagraphSkipsHeadingsAndTruncatesRunes(t *testing.T) {
	if got := FirstBodyParagraph("# 标题\n\n这是第一段。\n仍是第一段。\n\n这是第二段。\n"); got != "这是第一段。 仍是第一段。" {
		t.Fatalf("FirstBodyParagraph = %q", got)
	}

	long := strings.Repeat("技", 101)
	if got := FirstBodyParagraph("## 标题\n\n" + long); got != strings.Repeat("技", 100)+"…" {
		t.Fatalf("FirstBodyParagraph long result length = %d, value = %q", len([]rune(got)), got)
	}

	exact := strings.Repeat("能", 100)
	if got := FirstBodyParagraph(exact); got != exact {
		t.Fatalf("FirstBodyParagraph exact result = %q", got)
	}
}
