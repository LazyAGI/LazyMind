package metadata

import (
	"errors"
	"strings"
	"testing"
)

func TestParseRequired(t *testing.T) {
	meta, err := ParseRequired([]byte("---\nname: imported-skill\ndescription: Imported description\nversion: 1.2.3\ncategory: external\ntags: [test, test, '  verified  ']\n---\n# Skill\n"))
	if err != nil {
		t.Fatalf("ParseRequired returned error: %v", err)
	}
	if meta.Name != "imported-skill" || meta.Description != "Imported description" || meta.Version != "1.2.3" || meta.Category != "external" {
		t.Fatalf("ParseRequired metadata = %#v", meta)
	}
	if strings.Join(meta.Tags, ",") != "test,verified" {
		t.Fatalf("ParseRequired tags = %#v", meta.Tags)
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

func TestParseRequiredRejectsTooLongFields(t *testing.T) {
	cases := []struct {
		name    string
		content string
		field   string
	}{
		{
			name:    "name",
			content: "---\nname: " + strings.Repeat("a", MaxSkillNameLength+1) + "\ndescription: description\n---\n# Skill\n",
			field:   "name",
		},
		{
			name: "description",
			content: "---\nname: skill\ndescription: " +
				strings.Repeat("a", MaxSkillDescriptionLength+1) + "\n---\n# Skill\n",
			field: "description",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ParseRequired([]byte(tc.content))
			if err == nil {
				t.Fatal("ParseRequired succeeded")
			}
			var lengthErr *LengthError
			if !errors.As(err, &lengthErr) || lengthErr.Field != tc.field {
				t.Fatalf("ParseRequired error = %v, want LengthError field %q", err, tc.field)
			}
		})
	}
}

func TestParseAllowsMissingMetadataAndFindsFirstBodyParagraph(t *testing.T) {
	parsed, err := Parse([]byte("---\nversion: 1.2.3\n---\n# Skill\n\nFirst useful paragraph\ncontinues here.\n\nSecond paragraph.\n"))
	if err != nil {
		t.Fatalf("Parse returned error: %v", err)
	}
	if parsed.HasName || parsed.HasDescription || parsed.Version != "1.2.3" {
		t.Fatalf("Parse result = %#v", parsed)
	}
	if got := FirstBodyParagraph(parsed.Body); got != "First useful paragraph continues here." {
		t.Fatalf("FirstBodyParagraph = %q", got)
	}
}

func TestEffectiveDocumentUsesMetadataOnlyForRuntimeView(t *testing.T) {
	original := []byte("---\nversion: 1.2.3\n---\n# Skill\n\nA useful runtime description.\n")
	effective, err := EffectiveDocument(original, "runtime-skill", "A useful runtime description.")
	if err != nil {
		t.Fatalf("EffectiveDocument returned error: %v", err)
	}
	meta, err := ParseRequired(effective)
	if err != nil {
		t.Fatalf("runtime view is not strictly valid: %v", err)
	}
	if meta.Name != "runtime-skill" || meta.Description != "A useful runtime description." || meta.Version != "1.2.3" {
		t.Fatalf("runtime metadata = %#v", meta)
	}
	if !strings.Contains(string(effective), "# Skill") {
		t.Fatalf("runtime view lost body: %q", effective)
	}
	if string(original) != "---\nversion: 1.2.3\n---\n# Skill\n\nA useful runtime description.\n" {
		t.Fatalf("EffectiveDocument mutated original: %q", original)
	}

	complete := []byte("---\nname: source-skill\ndescription: Source description\n---\n# Source\n")
	unchanged, err := EffectiveDocument(complete, "ignored", "ignored")
	if err != nil {
		t.Fatalf("EffectiveDocument complete returned error: %v", err)
	}
	if string(unchanged) != string(complete) {
		t.Fatalf("complete document changed: %q", unchanged)
	}
}

func TestIsNameLengthError(t *testing.T) {
	nameErr := ValidateNameLength(strings.Repeat("a", MaxSkillNameLength+1))
	if !IsNameLengthError(nameErr) {
		t.Fatalf("IsNameLengthError(%v) = false, want true", nameErr)
	}
	descErr := ValidateDescriptionLength(strings.Repeat("a", MaxSkillDescriptionLength+1))
	if IsNameLengthError(descErr) {
		t.Fatalf("IsNameLengthError(%v) = true, want false", descErr)
	}
	if IsNameLengthError(nil) {
		t.Fatal("IsNameLengthError(nil) = true, want false")
	}
}
