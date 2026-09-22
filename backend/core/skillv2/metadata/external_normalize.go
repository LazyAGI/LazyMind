package metadata

import (
	"bytes"
	"strings"
	"unicode"
	"unicode/utf8"

	"gopkg.in/yaml.v3"
)

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
