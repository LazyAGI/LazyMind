package currentmemory

import "gopkg.in/yaml.v3"

type PreferenceProjectionState struct {
	StoredItems         int  `json:"stored_items"`
	FullProjectionChars int  `json:"full_projection_chars"`
	ProjectedItems      int  `json:"projected_items"`
	ProjectedChars      int  `json:"projected_chars"`
	ProjectionTruncated bool `json:"projection_truncated"`
}

type preferencePromptItem struct {
	Name      string `yaml:"name"`
	Summary   string `yaml:"summary"`
	Ref       string `yaml:"ref"`
	UpdatedAt string `yaml:"updated_at"`
}

func BuildPreferenceProjectionState(
	document PreferenceDocument,
	maxItems int,
	maxChars int,
) PreferenceProjectionState {
	all := make([]preferencePromptItem, 0, len(document.Preferences))
	for _, item := range document.Preferences {
		all = append(all, preferencePromptItem{
			Name: item.Name, Summary: item.Summary, Ref: item.Ref, UpdatedAt: item.UpdatedAt,
		})
	}
	fullContent := renderPreferencePromptItems(all)
	projected := make([]preferencePromptItem, 0, min(len(all), maxItems))
	for _, item := range all {
		if len(projected) >= maxItems {
			break
		}
		candidate := append(append([]preferencePromptItem{}, projected...), item)
		if len(renderPreferencePromptItems(candidate)) > maxChars {
			break
		}
		projected = candidate
	}
	projectedContent := renderPreferencePromptItems(projected)
	return PreferenceProjectionState{
		StoredItems: len(all), FullProjectionChars: len(fullContent),
		ProjectedItems: len(projected), ProjectedChars: len(projectedContent),
		ProjectionTruncated: len(projected) < len(all),
	}
}

func renderPreferencePromptItems(items []preferencePromptItem) string {
	content, err := yaml.Marshal(map[string]any{"preferences": items})
	if err != nil {
		return "preferences: []\n"
	}
	return string(content)
}
