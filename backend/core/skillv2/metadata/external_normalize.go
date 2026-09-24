package metadata

import (
	"bytes"
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"

	"gopkg.in/yaml.v3"
)

const (
	OriginalDescriptionField          = "x-lazymind-original-description"
	OriginalNameField                 = "x-lazymind-original-name"
	NormalizationField                = "x-lazymind-normalizations"
	NormalizationDescriptionCompacted = "description_compacted"
	NormalizationCanonicalName        = "canonical_name_from_source"
)

type NormalizationWarning struct {
	Code    string
	Message string
}

// NormalizeExternalDescription quotes one unambiguous plain description scalar.
// Callers must keep strict metadata validation after this import-only preflight.
func NormalizeExternalDescription(content []byte) ([]byte, bool, error) {
	const marker = "---\n"
	if !bytes.HasPrefix(content, []byte(marker)) {
		return content, false, nil
	}
	end := bytes.Index(content[len(marker):], []byte("\n---"))
	if end < 0 {
		return content, false, nil
	}
	frontEnd := len(marker) + end
	front := content[len(marker):frontEnd]
	var existing map[string]any
	if yaml.Unmarshal(front, &existing) == nil {
		return content, false, nil
	}
	lines := bytes.Split(front, []byte("\n"))
	index := -1
	for i, line := range lines {
		if !bytes.HasPrefix(line, []byte("description: ")) {
			continue
		}
		if index >= 0 {
			return content, false, nil
		}
		index = i
	}
	if index < 0 {
		return content, false, nil
	}
	value := string(bytes.TrimPrefix(lines[index], []byte("description: ")))
	if value == "" || strings.TrimSpace(value) != value || !strings.Contains(value, ": ") || strings.Contains(value, " #") || strings.ContainsAny(value, "\t\r") {
		return content, false, nil
	}
	first, _ := utf8.DecodeRuneInString(value)
	if !unicode.IsLetter(first) && !unicode.IsDigit(first) {
		return content, false, nil
	}
	scalar := &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: value, Style: yaml.DoubleQuotedStyle}
	quoted, err := yaml.Marshal(scalar)
	if err != nil {
		return content, false, err
	}
	lines[index] = append([]byte("description: "), bytes.TrimSuffix(quoted, []byte("\n"))...)
	nextFront := bytes.Join(lines, []byte("\n"))
	var parsed map[string]any
	if err := yaml.Unmarshal(nextFront, &parsed); err != nil || parsed["description"] != value {
		return content, false, nil
	}
	next := make([]byte, 0, len(content)+len(nextFront)-len(front))
	next = append(next, marker...)
	next = append(next, nextFront...)
	next = append(next, content[frontEnd:]...)
	return next, true, nil
}

// NormalizeExternalMetadata converts recoverable external metadata to the
// strict runtime contract while retaining the original values in frontmatter.
// It is deliberately limited to long descriptions and names that can use a
// verified source identity supplied by the caller.
func NormalizeExternalMetadata(content []byte, canonicalName string) ([]byte, []NormalizationWarning, error) {
	const marker = "---\n"
	if !bytes.HasPrefix(content, []byte(marker)) {
		return content, nil, nil
	}
	relativeEnd := bytes.Index(content[len(marker):], []byte("\n---"))
	if relativeEnd < 0 {
		return content, nil, nil
	}
	frontEnd := len(marker) + relativeEnd
	var document yaml.Node
	if err := yaml.Unmarshal(content[len(marker):frontEnd], &document); err != nil {
		return content, nil, fmt.Errorf("invalid SKILL.md frontmatter: %w", err)
	}
	if len(document.Content) != 1 || document.Content[0].Kind != yaml.MappingNode {
		return content, nil, nil
	}
	mapping := document.Content[0]
	nameNode, nameExists, err := uniqueScalarField(mapping, "name")
	if err != nil {
		return content, nil, err
	}
	descriptionNode, descriptionExists, err := uniqueScalarField(mapping, "description")
	if err != nil {
		return content, nil, err
	}

	warnings := make([]NormalizationWarning, 0, 2)
	if descriptionExists {
		description := strings.TrimSpace(descriptionNode.Value)
		if utf8.RuneCountInString(description) > MaxSkillDescriptionLength {
			if mappingField(mapping, OriginalDescriptionField) != nil {
				return content, nil, fmt.Errorf("SKILL.md frontmatter field %q conflicts with compatibility normalization", OriginalDescriptionField)
			}
			appendScalarField(mapping, OriginalDescriptionField, description, yaml.LiteralStyle)
			descriptionNode.Value = boundedDescription(description)
			descriptionNode.Tag = "!!str"
			descriptionNode.Style = yaml.DoubleQuotedStyle
			warnings = append(warnings, NormalizationWarning{
				Code:    NormalizationDescriptionCompacted,
				Message: "routing description was compacted to 1024 characters; the complete original is retained in SKILL.md frontmatter",
			})
		}
	}
	if nameExists {
		originalName := strings.TrimSpace(nameNode.Value)
		if originalName != "" && ValidateName(originalName) != nil {
			if !isDisplayNameWithSlash(originalName) {
				return content, nil, nil
			}
			if err := validateCanonicalSourceName(canonicalName); err != nil {
				return content, nil, fmt.Errorf("invalid SKILL.md frontmatter field \"name\": %w", err)
			}
			if mappingField(mapping, OriginalNameField) != nil {
				return content, nil, fmt.Errorf("SKILL.md frontmatter field %q conflicts with compatibility normalization", OriginalNameField)
			}
			appendScalarField(mapping, OriginalNameField, originalName, yaml.DoubleQuotedStyle)
			nameNode.Value = strings.TrimSpace(canonicalName)
			nameNode.Tag = "!!str"
			nameNode.Style = yaml.DoubleQuotedStyle
			warnings = append(warnings, NormalizationWarning{
				Code:    NormalizationCanonicalName,
				Message: "runtime name uses the verified source slug; the original display name is retained in SKILL.md frontmatter",
			})
		}
	}
	if len(warnings) == 0 {
		return content, nil, nil
	}
	codes := make([]string, 0, len(warnings))
	for _, warning := range warnings {
		codes = append(codes, warning.Code)
	}
	if mappingField(mapping, NormalizationField) != nil {
		return content, nil, fmt.Errorf("SKILL.md frontmatter field %q conflicts with compatibility normalization", NormalizationField)
	}
	appendStringSequenceField(mapping, NormalizationField, codes)
	frontmatter, err := yaml.Marshal(mapping)
	if err != nil {
		return content, nil, err
	}
	next := make([]byte, 0, len(content)+len(frontmatter))
	next = append(next, marker...)
	next = append(next, frontmatter...)
	next = append(next, content[frontEnd:]...)
	if _, err := ParseRequired(next); err != nil {
		return content, nil, err
	}
	return next, warnings, nil
}

// isDisplayNameWithSlash recognizes a slash used as a human-readable title
// separator. Path-like names remain subject to the strict runtime validator.
func isDisplayNameWithSlash(name string) bool {
	if strings.ContainsAny(name, "\\\x00") || !strings.Contains(name, " / ") {
		return false
	}
	parts := strings.Split(name, "/")
	if len(parts) < 2 {
		return false
	}
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" || part == "." || part == ".." {
			return false
		}
	}
	return true
}

func boundedDescription(description string) string {
	runes := []rune(strings.TrimSpace(description))
	if len(runes) <= MaxSkillDescriptionLength {
		return string(runes)
	}
	return string(runes[:MaxSkillDescriptionLength-1]) + "…"
}

func validateCanonicalSourceName(name string) error {
	name = strings.TrimSpace(name)
	if err := ValidateName(name); err != nil {
		return fmt.Errorf("verified source name is unavailable: %w", err)
	}
	for _, char := range name {
		if !(char >= 'a' && char <= 'z' || char >= 'A' && char <= 'Z' || char >= '0' && char <= '9' || char == '.' || char == '_' || char == '-') {
			return fmt.Errorf("verified source name has an unsupported runtime path character")
		}
	}
	return nil
}

func uniqueScalarField(mapping *yaml.Node, key string) (*yaml.Node, bool, error) {
	var found *yaml.Node
	for index := 0; index+1 < len(mapping.Content); index += 2 {
		if mapping.Content[index].Value != key {
			continue
		}
		if found != nil {
			return nil, false, fmt.Errorf("invalid SKILL.md frontmatter: duplicate field %q", key)
		}
		found = mapping.Content[index+1]
	}
	if found == nil {
		return nil, false, nil
	}
	if found.Kind != yaml.ScalarNode || found.Tag != "!!str" {
		// Leave type errors to the strict parser so normalization does not
		// change the established diagnostic category.
		return nil, false, nil
	}
	return found, true, nil
}

func mappingField(mapping *yaml.Node, key string) *yaml.Node {
	for index := 0; index+1 < len(mapping.Content); index += 2 {
		if mapping.Content[index].Value == key {
			return mapping.Content[index+1]
		}
	}
	return nil
}

func appendScalarField(mapping *yaml.Node, key, value string, style yaml.Style) {
	mapping.Content = append(mapping.Content,
		&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key},
		&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: value, Style: style},
	)
}

func appendStringSequenceField(mapping *yaml.Node, key string, values []string) {
	sequence := &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq", Style: yaml.FlowStyle}
	for _, value := range values {
		sequence.Content = append(sequence.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: value})
	}
	mapping.Content = append(mapping.Content,
		&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key}, sequence,
	)
}
