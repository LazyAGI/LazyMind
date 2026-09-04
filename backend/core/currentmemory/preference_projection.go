package currentmemory

import (
	"bytes"
	"encoding/json"
	"strings"
	"unicode/utf8"
)

type PreferenceProjectionState struct {
	MaxChars            int  `json:"max_chars"`
	StoredItems         int  `json:"stored_items"`
	FullProjectionChars int  `json:"full_projection_chars"`
	ProjectedItems      int  `json:"projected_items"`
	ProjectedChars      int  `json:"projected_chars"`
	ProjectionTruncated bool `json:"projection_truncated"`
}

type preferencePromptItem struct {
	Summary string
	Ref     string
}

func BuildPreferenceProjectionState(
	document PreferenceDocument,
	maxChars int,
) PreferenceProjectionState {
	if maxChars <= 0 {
		panic("preference context max chars must be positive")
	}
	all := make([]preferencePromptItem, 0, len(document.Preferences))
	for _, item := range document.Preferences {
		all = append(all, preferencePromptItem{
			Summary: item.Summary, Ref: item.Ref,
		})
	}
	fullContent := renderPreferencePromptItems(all)
	projected := make([]preferencePromptItem, 0, len(all))
	for _, item := range all {
		candidate := append(append([]preferencePromptItem{}, projected...), item)
		if preferenceProjectionChars(renderPreferencePromptItems(candidate)) > maxChars {
			break
		}
		projected = candidate
	}
	projectedContent := renderPreferencePromptItems(projected)
	if preferenceProjectionChars(projectedContent) > maxChars {
		projectedContent = ""
	}
	return PreferenceProjectionState{
		MaxChars:    maxChars,
		StoredItems: len(all), FullProjectionChars: preferenceProjectionChars(fullContent),
		ProjectedItems: len(projected), ProjectedChars: preferenceProjectionChars(projectedContent),
		ProjectionTruncated: len(projected) < len(all),
	}
}

func (m *Module) BuildPreferenceProjectionState(document PreferenceDocument) PreferenceProjectionState {
	return BuildPreferenceProjectionState(document, m.preferenceContextMaxChars)
}

func (s PreferenceProjectionState) NeedsOrganization() bool {
	// ceil(max * 80 / 100), without overflowing for a large valid budget.
	return s.FullProjectionChars >= s.MaxChars-s.MaxChars/5
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
		output.WriteString("- summary: " + preferenceProjectionScalar(item.Summary) + "\n")
		output.WriteString("  ref: " + preferenceProjectionScalar(item.Ref) + "\n")
	}
	return output.String()
}

// JSON strings are valid YAML scalars. Keep this encoding in sync with Chat:
// literal Unicode, JSON escapes, no HTML escaping or automatic line wrapping.
func preferenceProjectionScalar(value string) string {
	var out bytes.Buffer
	encoder := json.NewEncoder(&out)
	encoder.SetEscapeHTML(false)
	_ = encoder.Encode(value) // Encoding a string cannot fail for a bytes.Buffer.
	return strings.TrimSuffix(out.String(), "\n")
}
