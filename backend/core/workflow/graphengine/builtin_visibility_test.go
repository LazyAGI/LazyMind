package graphengine

import (
	"gopkg.in/yaml.v3"
	"os"
	"path/filepath"
	"testing"
)

// Steps must remain visible when optional outputs are absent.
func TestBuiltinStepsKeepVisibleOutputs(t *testing.T) {
	paths, err := filepath.Glob("../../../../workflows/*/workflow.yaml")
	if err != nil || len(paths) == 0 {
		t.Fatalf("discover workflows: %v", err)
	}
	for _, path := range paths {
		t.Run(filepath.Base(filepath.Dir(path)), func(t *testing.T) {
			var workflow struct {
				Steps []struct{ ID string }
				Slots []struct {
					ID          string
					UserVisible *bool `yaml:"user_visible"`
					Exposed     *bool
				}
				UI struct {
					TaskPresentation struct {
						HiddenSteps []string `yaml:"hidden_steps"`
					} `yaml:"task_presentation"`
					Tabs []struct {
						Slots           []struct{ ID string }
						Layout          string
						CompositeLayout any `yaml:"composite_layout"`
					}
				}
			}
			var state struct {
				Steps map[string]struct {
					Outputs []struct {
						Material string
						Required *bool
					}
				}
			}
			read := func(path string, target any) {
				t.Helper()
				data, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				if err := yaml.Unmarshal(data, target); err != nil {
					t.Fatal(err)
				}
			}
			read(path, &workflow)
			read(filepath.Join(filepath.Dir(path), "scenario/state.yml"), &state)
			visible := map[string]bool{}
			for _, slot := range workflow.Slots {
				visible[slot.ID] = (slot.UserVisible == nil || *slot.UserVisible) && (slot.Exposed == nil || *slot.Exposed)
			}
			var containsSlot func(any, string) bool
			containsSlot = func(node any, id string) bool {
				switch n := node.(type) {
				case string:
					return n == id
				case []any:
					for _, child := range n {
						if containsSlot(child, id) {
							return true
						}
					}
				case map[string]any:
					if n["slot"] == id {
						return true
					}
					return containsSlot(n["children"], id)
				}
				return false
			}
			displayed := map[string]bool{}
			for _, tab := range workflow.UI.Tabs {
				for _, slot := range tab.Slots {
					if tab.Layout != "composite" || tab.CompositeLayout == nil || containsSlot(tab.CompositeLayout, slot.ID) {
						displayed[slot.ID] = true
					}
				}
			}
			for _, step := range workflow.Steps {
				for _, hidden := range workflow.UI.TaskPresentation.HiddenSteps {
					if hidden == step.ID {
						t.Errorf("step %s is explicitly hidden", step.ID)
					}
				}
				outputs := state.Steps[step.ID].Outputs
				hasVisibleRequired := false
				for _, output := range outputs {
					if visible[output.Material] && (output.Required == nil || *output.Required) {
						hasVisibleRequired = true
					}
				}
				found := false
				for _, output := range outputs {
					// Alternative delivery formats can be optional even when a step
					// has required internal outputs, such as a postprocessing status.
					if hasVisibleRequired && output.Required != nil && !*output.Required {
						continue
					}
					if visible[output.Material] && (len(workflow.UI.Tabs) == 0 || displayed[output.Material]) {
						found = true
					}
				}
				if !found {
					t.Errorf("step %s has no visible output in its panel layout", step.ID)
				}
			}
		})
	}
}
