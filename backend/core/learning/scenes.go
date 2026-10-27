package learning

import (
	"fmt"
	"strings"
)

// NormalizeScene is shared by the page and capability contracts.
func NormalizeScene(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		value = "general"
	}
	if value == "paper_collection" {
		value = "academic_papers"
	}
	if value == "custom" {
		return value, nil
	}
	for _, profile := range BuiltinProfiles() {
		if profile.Key == value {
			return value, nil
		}
	}
	return "", fmt.Errorf("unknown knowledge scene: %s", value)
}
