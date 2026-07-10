package plugin

import (
	"encoding/json"
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
)

type repairDiagnostic struct{ Code, Path, Message, Severity string }

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
