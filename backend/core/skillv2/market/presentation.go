package market

import (
	"encoding/json"
	"sort"
	"strings"
)

// Presentation is marketplace copy, deliberately separate from SKILL.md metadata.
type Presentation struct {
	Name        string            `json:"name"`
	Description string            `json:"description"`
	TagLabels   map[string]string `json:"tag_labels,omitempty"`
}

func presentations(raw []byte) map[string]Presentation {
	values := map[string]Presentation{}
	if len(raw) != 0 {
		if err := json.Unmarshal(raw, &values); err != nil {
			return map[string]Presentation{}
		}
	}
	if values == nil {
		values = map[string]Presentation{}
	}
	return values
}

// ResolvePresentation falls back field-by-field without changing canonical data.
func ResolvePresentation(raw []byte, locale, name, description string) Presentation {
	value := presentations(raw)[locale]
	if strings.TrimSpace(value.Name) == "" {
		value.Name = name
	}
	if strings.TrimSpace(value.Description) == "" {
		value.Description = description
	}
	if value.TagLabels == nil {
		value.TagLabels = map[string]string{}
	}
	return value
}

// PresentationSearchText allows either original or translated names to be found,
// independently of which UI language is currently selected.
func PresentationSearchText(raw []byte) string {
	values := presentations(raw)
	locales := make([]string, 0, len(values))
	for locale := range values {
		locales = append(locales, locale)
	}
	sort.Strings(locales)
	parts := make([]string, 0, len(locales)*2)
	for _, locale := range locales {
		value := values[locale]
		parts = append(parts, value.Name, value.Description)
	}
	return strings.Join(parts, " ")
}

func updateChinesePresentation(raw []byte, name, description *string, labels *map[string]string) []byte {
	values := presentations(raw)
	value := values["zh-CN"]
	if name != nil {
		value.Name = strings.TrimSpace(*name)
	}
	if description != nil {
		value.Description = strings.TrimSpace(*description)
	}
	if labels != nil {
		value.TagLabels = make(map[string]string, len(*labels))
		for code, label := range *labels {
			code, label = strings.TrimSpace(code), strings.TrimSpace(label)
			if code != "" && label != "" {
				value.TagLabels[code] = label
			}
		}
	}
	values["zh-CN"] = value
	encoded, _ := json.Marshal(values)
	return encoded
}
