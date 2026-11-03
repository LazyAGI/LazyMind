package builtin

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Presentation is a separate artifact and never participates in package identity.
type Presentation struct {
	SchemaVersion int                          `json:"schema_version"`
	Skills        map[string]SkillPresentation `json:"skills"`
	TagLabels     map[string]map[string]string `json:"tag_labels"`
}
type PresentationText struct {
	Name        string `json:"name" yaml:"name"`
	Description string `json:"description" yaml:"description"`
}
type SkillPresentation struct {
	PresentationID string                      `json:"presentation_id"`
	Locales        map[string]PresentationText `json:"locales"`
}

func LoadPresentation(catalogPath string) (Presentation, error) {
	p := Presentation{}
	if catalogPath == "" {
		return p, nil
	}
	data, err := os.ReadFile(filepath.Join(filepath.Dir(catalogPath), "presentation.json"))
	if os.IsNotExist(err) {
		return p, nil
	}
	if err != nil {
		return p, err
	}
	if err = json.Unmarshal(data, &p); err != nil {
		return p, err
	}
	if p.SchemaVersion != 1 {
		return p, catalogFailure("unsupported presentation schema %d", p.SchemaVersion)
	}
	return p, nil
}
func (p Presentation) Display(uid, locale, name, description string) (string, string, string) {
	if text, ok := p.Skills[uid].Locales[locale]; ok && text.Name != "" && text.Description != "" {
		return text.Name, text.Description, locale
	}
	return name, description, "en-US"
}

// SearchText preserves bilingual marketplace search independently of display locale.
func (p Presentation) SearchText(uid string) string {
	texts := p.Skills[uid].Locales
	locales := make([]string, 0, len(texts))
	for locale := range texts {
		locales = append(locales, locale)
	}
	sort.Strings(locales)
	parts := make([]string, 0, len(locales)*2)
	for _, locale := range locales {
		text := texts[locale]
		parts = append(parts, text.Name, text.Description)
	}
	return strings.Join(parts, " ")
}
