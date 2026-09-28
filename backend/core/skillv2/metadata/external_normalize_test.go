package metadata

import (
	"bytes"
	"strings"
	"testing"
	"unicode/utf8"

	"gopkg.in/yaml.v3"
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

func TestNormalizeExternalMetadataPreservesLongUnicodeDescription(t *testing.T) {
	originalDescription := strings.Repeat("隐私规则：仅分析明确授权的仓库；", 80)
	original := []byte("---\nname: who-is-actor\ndescription: >-\n  " + originalDescription + "\ncustom:\n  nested: [one, two]\n---\n# Who Is Actor\n\nKeep the original body.\n")

	normalized, warnings, err := NormalizeExternalMetadata(original, "who-is-actor")
	if err != nil || len(warnings) != 1 || warnings[0].Code != NormalizationDescriptionCompacted {
		t.Fatalf("normalize warnings = %#v, err = %v", warnings, err)
	}
	parsed, err := ParseRequired(normalized)
	if err != nil {
		t.Fatal(err)
	}
	if utf8.RuneCountInString(parsed.Description) > MaxSkillDescriptionLength {
		t.Fatalf("routing description length = %d", utf8.RuneCountInString(parsed.Description))
	}
	if !bytes.Contains(normalized, []byte("# Who Is Actor\n\nKeep the original body.\n")) {
		t.Fatalf("body changed: %q", normalized)
	}
	var document map[string]any
	front := normalized[len("---\n") : bytes.Index(normalized[len("---\n"):], []byte("\n---"))+len("---\n")]
	if err := yaml.Unmarshal(front, &document); err != nil {
		t.Fatal(err)
	}
	if document[OriginalDescriptionField] != originalDescription {
		t.Fatalf("original description was not retained exactly: %#v", document[OriginalDescriptionField])
	}
	if got := document[NormalizationField]; got == nil {
		t.Fatal("normalization marker missing")
	}

	again, warnings, err := NormalizeExternalMetadata(normalized, "who-is-actor")
	if err != nil || len(warnings) != 0 || !bytes.Equal(again, normalized) {
		t.Fatalf("normalization is not idempotent: warnings=%#v err=%v", warnings, err)
	}
}

func TestNormalizeExternalMetadataUsesCanonicalNameAndPreservesDisplayName(t *testing.T) {
	originalName := "用户画像与人群洞察 / User Persona & Crowd Insight"
	original := []byte("---\nname: " + originalName + "\ndescription: Build a structured persona from supplied data.\nunknown: retained\n---\n# Persona\n")

	normalized, warnings, err := NormalizeExternalMetadata(original, "lingyi-user-persona-and-crowd-insight")
	if err != nil || len(warnings) != 1 || warnings[0].Code != NormalizationCanonicalName {
		t.Fatalf("normalize warnings = %#v, err = %v", warnings, err)
	}
	meta, err := ParseRequired(normalized)
	if err != nil || meta.Name != "lingyi-user-persona-and-crowd-insight" {
		t.Fatalf("metadata = %#v, err = %v", meta, err)
	}
	var document map[string]any
	front := normalized[len("---\n") : bytes.Index(normalized[len("---\n"):], []byte("\n---"))+len("---\n")]
	if err := yaml.Unmarshal(front, &document); err != nil {
		t.Fatal(err)
	}
	if document[OriginalNameField] != originalName || document["unknown"] != "retained" {
		t.Fatalf("display name or unknown metadata lost: %#v", document)
	}

	again, warnings, err := NormalizeExternalMetadata(normalized, "lingyi-user-persona-and-crowd-insight")
	if err != nil || len(warnings) != 0 || !bytes.Equal(again, normalized) {
		t.Fatalf("normalization is not idempotent: warnings=%#v err=%v", warnings, err)
	}
}

func TestNormalizeExternalMetadataDoesNotReplacePathLikeName(t *testing.T) {
	original := []byte("---\nname: ../escape\ndescription: Invalid path identity.\n---\n# Body\n")
	normalized, warnings, err := NormalizeExternalMetadata(original, "safe-source-slug")
	if err != nil || len(warnings) != 0 || !bytes.Equal(normalized, original) {
		t.Fatalf("path-like name was normalized: warnings=%#v err=%v content=%q", warnings, err, normalized)
	}
	if _, err := ParseRequired(normalized); err == nil {
		t.Fatal("path-like authored name must remain invalid")
	}
}

func TestNormalizeExternalMetadataRequiresVerifiedSourceName(t *testing.T) {
	original := []byte("---\nname: Display Name / Localized Name\ndescription: A display name.\n---\n# Body\n")
	if _, _, err := NormalizeExternalMetadata(original, ""); err == nil {
		t.Fatal("display-name normalization without a verified source slug must fail")
	}
}

func TestNormalizeExternalMetadataRejectsReservedFieldConflict(t *testing.T) {
	original := []byte("---\nname: long-description\ndescription: " + strings.Repeat("x", MaxSkillDescriptionLength+1) + "\nx-lazymind-original-description: authored value\n---\n# Body\n")
	if _, _, err := NormalizeExternalMetadata(original, "long-description"); err == nil {
		t.Fatal("reserved compatibility field conflict must fail")
	}
}

func TestNormalizeExternalMetadataLeavesValidBoundedInputUnchanged(t *testing.T) {
	original := []byte("---\nname: valid-name\ndescription: Valid bounded description.\n---\n# Body\n")
	normalized, warnings, err := NormalizeExternalMetadata(original, "source-slug")
	if err != nil || len(warnings) != 0 || !bytes.Equal(normalized, original) {
		t.Fatalf("valid input changed: warnings=%#v err=%v content=%q", warnings, err, normalized)
	}
}
