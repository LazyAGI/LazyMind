package main

import (
	"bytes"
	"gopkg.in/yaml.v3"
	"io"
	b "lazymind/core/skillv2/builtin"
	"os"
	"path/filepath"
	"strings"
)

type presentationSource struct {
	SchemaVersion int `yaml:"schema_version"`
	Locales       map[string]struct {
		Tags   map[string]string             `yaml:"tags"`
		Skills map[string]b.PresentationText `yaml:"skills"`
	} `yaml:"locales"`
}

func compilePresentation(sourcesPath string, sources []sourceInput, entries map[string]b.CatalogSkill) (b.Presentation, error) {
	p := b.Presentation{SchemaVersion: 1, Skills: map[string]b.SkillPresentation{}, TagLabels: map[string]map[string]string{}}
	data, err := os.ReadFile(filepath.Join(filepath.Dir(sourcesPath), "marketplace", "locales.yaml"))
	hasIDs := false
	for _, s := range sources {
		if s.PresentationID != "" {
			hasIDs = true
		}
	}
	if os.IsNotExist(err) && !hasIDs {
		return p, nil
	}
	if err != nil {
		return p, err
	}
	var raw presentationSource
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)
	if err = decoder.Decode(&raw); err != nil {
		return p, err
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return p, bundleFailure("presentation sources must contain one YAML document")
	}
	if raw.SchemaVersion != 1 {
		return p, bundleFailure("unsupported presentation schema %d", raw.SchemaVersion)
	}
	seen := map[string]bool{}
	for locale, content := range raw.Locales {
		p.TagLabels[locale] = content.Tags
	}
	for _, source := range sources {
		if !source.MarketVisible {
			continue
		}
		id := strings.TrimSpace(source.PresentationID)
		if id == "" {
			return p, bundleFailure("ordinary source missing presentation_id")
		}
		if seen[id] {
			return p, bundleFailure("duplicate presentation_id %s", id)
		}
		seen[id] = true
		url := source.URL
		if source.Bundled != nil {
			url = bundledSourceURL(source.Bundled.Path)
		}
		entry, ok := entries[url]
		if !ok {
			return p, bundleFailure("missing catalog source for presentation %s", id)
		}
		sp := b.SkillPresentation{PresentationID: id, Locales: map[string]b.PresentationText{}}
		for locale, content := range raw.Locales {
			if text, ok := content.Skills[id]; ok {
				if strings.TrimSpace(text.Name) == "" || strings.TrimSpace(text.Description) == "" {
					return p, bundleFailure("empty presentation %s/%s", locale, id)
				}
				sp.Locales[locale] = text
			}
		}
		if _, ok := sp.Locales["zh-CN"]; !ok {
			return p, bundleFailure("missing Chinese presentation %s", id)
		}
		for _, tag := range append(append([]string{}, entry.Tags...), entry.Category) {
			if strings.TrimSpace(raw.Locales["zh-CN"].Tags[tag]) == "" {
				return p, bundleFailure("missing Chinese tag label %s", tag)
			}
		}
		p.Skills[entry.UID] = sp
	}
	for locale, content := range raw.Locales {
		for id := range content.Skills {
			if !seen[id] {
				return p, bundleFailure("unknown presentation binding %s/%s", locale, id)
			}
		}
	}
	return p, nil
}
func writePresentation(sourcesPath, output string, sources []sourceInput, entries map[string]b.CatalogSkill) error {
	p, err := compilePresentation(sourcesPath, sources, entries)
	if err != nil {
		return err
	}
	return writeJSONAtomic(filepath.Join(output, "presentation.json"), p)
}
