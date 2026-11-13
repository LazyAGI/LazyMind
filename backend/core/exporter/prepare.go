package exporter

import (
	"context"
	"gopkg.in/yaml.v3"
	"lazymind/core/systemdeps"
	"log"
)

// PrewarmWorkflow prepares export providers declared by the pinned revision.
// Downloads are independent of the launch request and never delay execution.
func PrewarmWorkflow(workflowYAML []byte) {
	if !systemdeps.IsLocalRuntime() || !needsEditablePresentation(workflowYAML) {
		return
	}
	go func() {
		root, err := systemdeps.RuntimeRootFromEnv()
		if err == nil {
			_, err = systemdeps.EnsureEditablePPT(context.Background(), root)
		}
		if err != nil {
			log.Printf("prepare workflow presentation export: %v", err)
		}
	}()
}

func needsEditablePresentation(body []byte) bool {
	var declaration struct {
		UI struct {
			Tabs []struct {
				Actions []struct {
					Type     string   `yaml:"type"`
					Provider string   `yaml:"provider"`
					Formats  []string `yaml:"formats"`
				} `yaml:"actions"`
			} `yaml:"tabs"`
		} `yaml:"ui"`
	}
	if yaml.Unmarshal(body, &declaration) != nil {
		return false
	}
	for _, tab := range declaration.UI.Tabs {
		for _, action := range tab.Actions {
			if action.Type != "export" || action.Provider != htmlPresentationProviderID {
				continue
			}
			if len(action.Formats) == 0 {
				return true
			}
			for _, format := range action.Formats {
				if format == "editable-pptx" {
					return true
				}
			}
		}
	}
	return false
}
