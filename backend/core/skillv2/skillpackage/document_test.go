package skillpackage

import (
	"bytes"
	"maps"
	"reflect"
	"strings"
	"testing"
)

func TestNormalizeSkillDocumentPreservesContentAndIsIdempotent(t *testing.T) {
	for _, name := range []string{"SKILL.md", "skill.md", "Skill.Md", "SKILL.MD"} {
		t.Run(name, func(t *testing.T) {
			original := []byte("---\nname: fixture\ndescription: Fixture.\nunknown: {nested: [one, two]}\n---\n# Original\r\nBody.\r\n")
			files := map[string][]byte{
				name:                  original,
				"scripts/run.py":      []byte("# Not executed.\n"),
				"references/SKILL.md": []byte("An example document, not the package entry point.\n"),
			}
			want := maps.Clone(files)
			delete(want, name)
			want["SKILL.md"] = original
			if err := NormalizeSkillDocument(files); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(files, want) || !bytes.Equal(files["SKILL.md"], original) {
				t.Fatalf("normalization changed content: %#v", files)
			}
			if err := NormalizeSkillDocument(files); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(files, want) {
				t.Fatal("normalization is not idempotent")
			}
		})
	}
}

func TestNormalizeSkillDocumentRejectsMissingAndAmbiguousWithoutMutation(t *testing.T) {
	tests := []struct {
		name    string
		files   map[string][]byte
		message string
	}{
		{"nil", nil, "must contain SKILL.md"},
		{"missing", map[string][]byte{"README.md": []byte("readme")}, "must contain SKILL.md"},
		{"nested", map[string][]byte{"nested/skill.md": []byte("nested")}, "must contain SKILL.md"},
		{"unicode lookalike", map[string][]byte{"\u017fkill.md": []byte("lookalike")}, "must contain SKILL.md"},
		{"canonical and lowercase", map[string][]byte{"SKILL.md": []byte("same"), "skill.md": []byte("same")}, "ambiguous SKILL.md"},
		{"two variants", map[string][]byte{"Skill.md": []byte("one"), "skill.MD": []byte("two")}, "ambiguous SKILL.md"},
		{"canonical directory", map[string][]byte{"skill.md": []byte("file"), "SKILL.md/child": []byte("child")}, "ambiguous SKILL.md"},
		{"variant directory", map[string][]byte{"SKILL.md": []byte("file"), "skill.md/child": []byte("child")}, "ambiguous SKILL.md"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			before := maps.Clone(tt.files)
			for i := 0; i < 2; i++ {
				err := NormalizeSkillDocument(tt.files)
				if err == nil || !strings.Contains(err.Error(), tt.message) {
					t.Fatalf("error = %v, want %q", err, tt.message)
				}
				if !reflect.DeepEqual(tt.files, before) {
					t.Fatal("rejected package was mutated")
				}
			}
		})
	}
}
