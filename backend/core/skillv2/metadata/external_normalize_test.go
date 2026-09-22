package metadata

import (
	"bytes"
	"strings"
	"testing"
)

func TestNormalizeExternalDescriptionQuotesOnlyRecoverableScalar(t *testing.T) {
	original := []byte("---\nname: example\ndescription: Analyze discussions for: useful patterns.\ncustom:\n  nested: [one, two]\n---\n# Body\n\nKeep: this text and references/notes.md.\n")
	if _, err := Parse(original); err == nil {
		t.Fatal("original malformed YAML unexpectedly parsed")
	}
	normalized, changed, err := NormalizeExternalDescription(original)
	if err != nil || !changed {
		t.Fatalf("normalize = changed %v, err %v", changed, err)
	}
	if !bytes.Contains(normalized, []byte("custom:\n  nested: [one, two]\n---\n# Body\n\nKeep: this text and references/notes.md.\n")) {
		t.Fatalf("unrelated metadata or body changed: %q", normalized)
	}
	meta, err := ParseRequired(normalized)
	if err != nil || meta.Description != "Analyze discussions for: useful patterns." {
		t.Fatalf("parsed normalized metadata = %#v, %v", meta, err)
	}
	again, changed, err := NormalizeExternalDescription(normalized)
	if err != nil || changed || !bytes.Equal(again, normalized) {
		t.Fatalf("normalization is not idempotent: changed %v, err %v", changed, err)
	}
}

func TestNormalizeExternalDescriptionLeavesAmbiguousAndValidInputsAlone(t *testing.T) {
	tests := []string{
		"---\nname: example\ndescription: - Alpha: beta\n---\n# Body\n",
		"---\nname: example\ndescription: ? Alpha: beta\n---\n# Body\n",
		"---\nname: example\ndescription: Alpha: beta # possible comment\n---\n# Body\n",
		"---\nname: example\ndescription: Alpha: beta\ntags: [broken\n---\n# Body\n",
		"---\nname: example\ndescription: Alpha: beta\ndescription: second\n---\n# Body\n",
		"---\nname: example\ndescription: \"Alpha: beta\"\n---\n# Body\n",
		"---\nname: example\ndescription: >\n  Alpha: beta\n---\n# Body\n",
		"---\nname: example\n  description: Alpha: beta\n---\n# Body\n",
		"# No frontmatter\n",
	}
	for _, input := range tests {
		t.Run(strings.ReplaceAll(input[:min(len(input), 35)], "\n", "_"), func(t *testing.T) {
			original := []byte(input)
			got, changed, err := NormalizeExternalDescription(original)
			if err != nil || changed || !bytes.Equal(got, original) {
				t.Fatalf("unexpected normalization: changed %v, err %v, content %q", changed, err, got)
			}
		})
	}
}
