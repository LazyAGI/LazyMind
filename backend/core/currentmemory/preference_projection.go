package currentmemory

import (
	"strings"
	"unicode/utf8"

	"gopkg.in/yaml.v3"
)

type PreferenceProjectionState struct {
	StoredItems         int  `json:"stored_items"`
	FullProjectionChars int  `json:"full_projection_chars"`
	ProjectedItems      int  `json:"projected_items"`
	ProjectedChars      int  `json:"projected_chars"`
	ProjectionTruncated bool `json:"projection_truncated"`
}

type preferencePromptItem struct {
	Summary string `yaml:"summary"`
	Ref     string `yaml:"ref"`
}

func BuildPreferenceProjectionState(
	document PreferenceDocument,
	maxItems int,
	maxChars int,
) PreferenceProjectionState {
	all := make([]preferencePromptItem, 0, len(document.Preferences))
	for _, item := range document.Preferences {
		all = append(all, preferencePromptItem{
			Summary: item.Summary, Ref: item.Ref,
		})
	}
	fullContent := renderPreferencePromptItems(all)
	projected := make([]preferencePromptItem, 0, min(len(all), maxItems))
	for _, item := range all {
		if len(projected) >= maxItems {
			break
		}
		candidate := append(append([]preferencePromptItem{}, projected...), item)
		if preferenceProjectionChars(renderPreferencePromptItems(candidate)) > maxChars {
			break
		}
		projected = candidate
	}
	projectedContent := renderPreferencePromptItems(projected)
	return PreferenceProjectionState{
		StoredItems: len(all), FullProjectionChars: preferenceProjectionChars(fullContent),
		ProjectedItems: len(projected), ProjectedChars: preferenceProjectionChars(projectedContent),
		ProjectionTruncated: len(projected) < len(all),
	}
}

func preferenceProjectionChars(content string) int {
	return utf8.RuneCountInString(content)
}

func renderPreferencePromptItems(items []preferencePromptItem) string {
	if len(items) == 0 {
		return "preferences: []\n"
	}
	var output strings.Builder
	output.WriteString("preferences:\n")
	for _, item := range items {
		content, err := yaml.Marshal(item)
		if err != nil {
			return "preferences: []\n"
		}
		lines := strings.Split(strings.TrimSuffix(string(content), "\n"), "\n")
		for index, line := range lines {
			if index == 0 {
				output.WriteString("- ")
			} else {
				output.WriteString("  ")
			}
			output.WriteString(line)
			output.WriteByte('\n')
		}
	}
	return output.String()
}
