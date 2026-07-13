package plugin

import (
	"encoding/json"
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
)

type repairDiagnostic struct {
	Code     string `json:"code"`
	Path     string `json:"path"`
	Message  string `json:"message"`
	Severity string `json:"severity"`
}

func diagnosePlugin(pluginYAML, stateYAML, scenario, scriptsJSON string) []repairDiagnostic {
	var pluginDoc, stateDoc map[string]any
	var out []repairDiagnostic
	if err := yaml.Unmarshal([]byte(pluginYAML), &pluginDoc); err != nil {
		return []repairDiagnostic{{"plugin_yaml_invalid", "plugin.yaml", err.Error(), "error"}}
	}
	if err := yaml.Unmarshal([]byte(stateYAML), &stateDoc); err != nil {
		return []repairDiagnostic{{"state_yaml_invalid", "scenario/state.yml", err.Error(), "error"}}
	}
	stepIDs := map[string]bool{}
	slotIDs := map[string]bool{}
	if slots, ok := pluginDoc["slots"].([]any); ok {
		for _, raw := range slots {
			if slot, ok := raw.(map[string]any); ok {
				if id := fmt.Sprint(slot["id"]); id != "" && id != "<nil>" {
					slotIDs[id] = true
				}
			}
		}
	}
	placedSlots := map[string]bool{}
	ui, _ := pluginDoc["ui"].(map[string]any)
	tabs, tabsOK := ui["tabs"].([]any)
	if !tabsOK || len(tabs) == 0 {
		out = append(out, repairDiagnostic{"ui_tabs_missing", "plugin.yaml.ui.tabs", "UI has no tabs and cannot display plugin artifacts", "error"})
	} else {
		for i, raw := range tabs {
			tab, _ := raw.(map[string]any)
			refs, ok := tab["slots"].([]any)
			if !ok || len(refs) == 0 {
				out = append(out, repairDiagnostic{"ui_tab_empty", fmt.Sprintf("plugin.yaml.ui.tabs[%d].slots", i), "UI tab has no slots and renders an empty page", "error"})
				continue
			}
			for _, rawRef := range refs {
				if ref, ok := rawRef.(map[string]any); ok {
					if id := fmt.Sprint(ref["id"]); id != "" && id != "<nil>" {
						placedSlots[id] = true
					}
				} else if id := fmt.Sprint(rawRef); id != "" && id != "<nil>" {
					placedSlots[id] = true
				}
			}
		}
	}
	for id := range slotIDs {
		if !placedSlots[id] {
			out = append(out, repairDiagnostic{"ui_slot_unplaced", "plugin.yaml.ui.tabs", "Declared slot is not placed in any UI tab: " + id, "error"})
		}
	}
	if steps, ok := pluginDoc["steps"].([]any); ok {
		for _, raw := range steps {
			if step, ok := raw.(map[string]any); ok {
				if id := fmt.Sprint(step["id"]); id != "" && id != "<nil>" {
					stepIDs[id] = true
				}
			}
		}
	}
	stateSteps, _ := stateDoc["steps"].(map[string]any)
	for id := range stepIDs {
		if _, ok := stateSteps[id]; !ok {
			out = append(out, repairDiagnostic{"state_step_missing", "scenario/state.yml.steps." + id, "Plugin step has no state configuration", "error"})
		}
	}
	transitions, _ := stateDoc["transitions"].(map[string]any)
	if start, ok := transitions["__start__"].([]any); !ok || len(start) == 0 {
		out = append(out, repairDiagnostic{"state_start_missing", "scenario/state.yml.transitions.__start__", "State machine has no entry transition", "error"})
	}
	for id := range stepIDs {
		if _, ok := transitions[id]; !ok {
			out = append(out, repairDiagnostic{"state_transition_missing", "scenario/state.yml.transitions." + id, "Step has no outgoing transition", "error"})
		}
		if scenario != "" && !strings.Contains(scenario, id) {
			out = append(out, repairDiagnostic{"scenario_step_missing", "scenario/scenario.md", "Scenario does not mention step " + id, "warning"})
		}
	}
	var scripts map[string]string
	if strings.TrimSpace(scriptsJSON) != "" && json.Unmarshal([]byte(scriptsJSON), &scripts) != nil {
		out = append(out, repairDiagnostic{"scripts_json_invalid", "scripts", "scripts_content is not valid JSON", "error"})
	}
	if declarations, ok := pluginDoc["tool_scripts"].([]any); ok {
		for _, raw := range declarations {
			declaration, _ := raw.(map[string]any)
			path := fmt.Sprint(declaration["path"])
			if _, exists := scripts[path]; !exists {
				out = append(out, repairDiagnostic{"tool_script_missing", "plugin.yaml.tool_scripts", "Declared script is unavailable and will be ignored: " + path, "warning"})
			}
		}
	}
	return out
}

func diagnosticsJSON(items []repairDiagnostic) string { b, _ := json.Marshal(items); return string(b) }
func hasDiagnosticErrors(items []repairDiagnostic) bool {
	for _, item := range items {
		if item.Severity == "error" {
			return true
		}
	}
	return false
}

func hasDiagnosticErrorsForTarget(items []repairDiagnostic, target string) bool {
	for _, item := range items {
		if item.Severity == "error" && diagnosticAppliesToTarget(item, target) {
			return true
		}
	}
	return false
}

func diagnosticAppliesToTarget(item repairDiagnostic, target string) bool {
	if target == "full" || item.Code == "plugin_yaml_invalid" {
		return true
	}
	switch target {
	case "statemachine":
		return strings.HasPrefix(item.Code, "state_")
	case "ui":
		return strings.HasPrefix(item.Code, "ui_")
	case "scenario":
		return strings.HasPrefix(item.Code, "scenario_")
	case "scripts":
		return strings.HasPrefix(item.Code, "scripts_") || strings.HasPrefix(item.Code, "tool_script_")
	default:
		return true
	}
}

func diagnosticsForTarget(items []repairDiagnostic, target string) []repairDiagnostic {
	filtered := make([]repairDiagnostic, 0, len(items))
	for _, item := range items {
		if diagnosticAppliesToTarget(item, target) {
			filtered = append(filtered, item)
		}
	}
	return filtered
}
