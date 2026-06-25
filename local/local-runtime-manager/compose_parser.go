package main

import (
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

type OverlayConfig struct {
	Mode                        string   `yaml:"mode"`
	DisabledContainerTypes      []string `yaml:"disabled_container_services"`
	ScaleDisabledContainerTypes []string `yaml:"scale_disabled_container_services"`
}

type composeOverlayFile struct {
	Local OverlayConfig `yaml:"x-lazymind-local"`
}

func parseRuntimeOverlay(path string) (OverlayConfig, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return OverlayConfig{}, err
	}

	var parsed composeOverlayFile
	if err := yaml.Unmarshal(b, &parsed); err != nil {
		return OverlayConfig{}, err
	}
	return parsed.Local, nil
}

func parseServiceLines(output string) []string {
	parts := strings.Split(output, "\n")
	items := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		items = append(items, p)
	}
	return items
}
