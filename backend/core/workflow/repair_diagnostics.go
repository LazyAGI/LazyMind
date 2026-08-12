package workflow

import (
	"encoding/json"
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
	"lazymind/core/workflow/graphengine"
)

type repairDiagnostic struct {
	Code       string         `json:"code"`
	Path       string         `json:"path"`
	Message    string         `json:"message"`
	Severity   string         `json:"severity"`
	NodeID     string         `json:"node_id,omitempty"`
	EdgeID     string         `json:"edge_id,omitempty"`
	MaterialID string         `json:"material_id,omitempty"`
	Details    map[string]any `json:"details,omitempty"`
	Fixable    bool           `json:"fixable"`
}

func diagnoseWorkflow(workflowYAML, stateYAML, scenario, scriptsJSON string) []repairDiagnostic {
	return diagnoseWorkflowWithProfile(workflowYAML, stateYAML, scenario, scriptsJSON, graphengine.ProfileEditor)
}

func diagnoseWorkflowWithProfile(workflowYAML, stateYAML, scenario, scriptsJSON string, profile graphengine.Profile) []repairDiagnostic {
	compiled := graphengine.Compile(workflowYAML, stateYAML, scenario, profile)
	out := make([]repairDiagnostic, 0, len(compiled.Diagnostics))
	for _, item := range compiled.Diagnostics {
		out = append(out, repairDiagnostic{Code: item.Code, Path: item.Path, Message: item.Message, Severity: item.Severity, NodeID: item.NodeID, EdgeID: item.EdgeID, MaterialID: item.MaterialID, Details: item.Details, Fixable: item.Fixable})
	}
	// Script diagnostics are deliberately separate from graph compilation, but
	// use the same public diagnostic envelope.
	var workflowDoc map[string]any
	_ = yaml.Unmarshal([]byte(workflowYAML), &workflowDoc)
	var scripts map[string]string
	if strings.TrimSpace(scriptsJSON) != "" && json.Unmarshal([]byte(scriptsJSON), &scripts) != nil {
		out = append(out, repairDiagnostic{Code: "E_SCRIPTS_JSON_INVALID", Path: "scripts", Message: "scripts_content is not valid JSON", Severity: "error", Fixable: true})
	}
	if declarations, ok := workflowDoc["tool_scripts"].([]any); ok {
		for _, raw := range declarations {
			declaration, _ := raw.(map[string]any)
			path := fmt.Sprint(declaration["path"])
			if _, exists := scripts[path]; !exists {
				out = append(out, repairDiagnostic{Code: "W_TOOL_SCRIPT_MISSING", Path: "workflow.yaml.tool_scripts", Message: "Declared script is unavailable and will be ignored: " + path, Severity: "warning", Fixable: true})
			}
		}
	}
	// `command` was accepted by early authoring YAML but is not part of the
	// runtime graph contract.  Reject it instead of silently letting the model
	// pretend that a CLI command ran successfully.
	if steps, ok := workflowDoc["steps"].([]any); ok {
		for index, raw := range steps {
			step, _ := raw.(map[string]any)
			command, _ := step["command"].(string)
			if strings.TrimSpace(command) != "" {
				nodeID, _ := step["id"].(string)
				out = append(out, repairDiagnostic{
					Code: "E_STEP_COMMAND_UNSUPPORTED", Path: fmt.Sprintf("workflow.yaml.steps[%d].command", index),
					Message:  "step command is not executable; use a declared tool or redesign this step with built-in capabilities",
					Severity: "error", NodeID: strings.TrimSpace(nodeID), Fixable: true,
				})
			}
		}
	}
	if len(scripts) == 0 && (strings.Contains(workflowYAML, "scripts/") || strings.Contains(stateYAML, "scripts/")) {
		out = append(out, repairDiagnostic{
			Code: "E_STEP_SCRIPT_REFERENCE_MISSING", Path: "workflow.yaml/scenario/state.yml",
			Message:  "workflow instructions reference scripts/ but the published package contains no scripts; use declared packaged tools or an honest built-in-capability fallback",
			Severity: "error", Fixable: true,
		})
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
	if target == "full" || item.Code == "E_WORKFLOW_YAML_INVALID" {
		return true
	}
	switch target {
	case "statemachine":
		return strings.HasPrefix(item.Path, "scenario/state.yml") || strings.HasPrefix(item.Code, "E_GRAPH_") || strings.HasPrefix(item.Code, "E_EDGE_") || strings.HasPrefix(item.Code, "E_STEP_") || strings.HasPrefix(item.Code, "E_ROUTE_") || strings.HasPrefix(item.Code, "E_SKIP_") || strings.HasPrefix(item.Code, "E_MATERIAL_") || strings.HasPrefix(item.Code, "E_EXPRESSION_") || strings.HasPrefix(item.Code, "E_BIND_")
	case "ui":
		return strings.Contains(item.Code, "_UI_")
	case "scenario":
		return strings.Contains(item.Code, "SCENARIO_")
	case "scripts":
		return strings.Contains(item.Code, "SCRIPTS_") || strings.Contains(item.Code, "TOOL_SCRIPT_")
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
