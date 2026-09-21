package workflow

import (
	"encoding/json"
	"fmt"
	"sort"
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
	if profile == graphengine.ProfilePublish {
		for _, item := range builtinArtifactActionDiagnostics(workflowYAML) {
			out = append(out, repairDiagnostic{Code: item.Code, Path: item.Path, Message: item.Message, Severity: item.Severity, Fixable: true})
		}
	}
	return out
}

type builtinArtifactActionContract struct {
	Action string
	Phases map[string]bool
}

var builtinArtifactActionContracts = map[string]builtinArtifactActionContract{
	"builtin:document.rewrite_selection.v1": {Action: "rewrite_selection", Phases: map[string]bool{"preview": true, "execute": true}},
	"builtin:document.render_document.v1":   {Action: "render_document", Phases: map[string]bool{"preview": true, "execute": true}},
	"builtin:document.save_document.v1":     {Action: "save_document", Phases: map[string]bool{"execute": true}},
	"builtin:document.sync_document.v1":     {Action: "sync_document", Phases: map[string]bool{"execute": true}},
	"builtin:document.convert_document.v1":  {Action: "convert_document", Phases: map[string]bool{"preview": true, "execute": true}},
	"builtin:document.write_document.v1":    {Action: "write_document", Phases: map[string]bool{"execute": true}},
}

// builtinArtifactActionDiagnostics mirrors the immutable Runtime registry at
// the Workflow publication boundary. Package-local tool names remain valid and
// continue to be resolved from the pinned Workflow package.
func builtinArtifactActionDiagnostics(workflowYAML string) []authoringDiagnostic {
	var manifest struct {
		ArtifactActions map[string]struct {
			PreviewTool string `yaml:"preview_tool"`
			ExecuteTool string `yaml:"execute_tool"`
		} `yaml:"artifact_actions"`
	}
	if yaml.Unmarshal([]byte(workflowYAML), &manifest) != nil {
		return nil // The graph compiler owns malformed-YAML diagnostics.
	}
	var diagnostics []authoringDiagnostic
	actions := make([]string, 0, len(manifest.ArtifactActions))
	for action := range manifest.ArtifactActions {
		actions = append(actions, action)
	}
	sort.Strings(actions)
	for _, action := range actions {
		definition := manifest.ArtifactActions[action]
		tools := []struct{ phase, reference string }{
			{phase: "preview", reference: definition.PreviewTool},
			{phase: "execute", reference: definition.ExecuteTool},
		}
		for _, tool := range tools {
			phase, reference := tool.phase, tool.reference
			if !strings.HasPrefix(reference, "builtin:") {
				continue
			}
			contract, exists := builtinArtifactActionContracts[reference]
			path := fmt.Sprintf("workflow.yaml.artifact_actions.%s.%s_tool", action, phase)
			if !exists {
				diagnostics = append(diagnostics, authoringDiagnostic{Code: "E_DOCUMENT_ACTION_REFERENCE_INVALID", Severity: "error", Path: path, Message: "unknown built-in document Action reference: " + reference})
				continue
			}
			if contract.Action != action {
				diagnostics = append(diagnostics, authoringDiagnostic{Code: "E_DOCUMENT_ACTION_REFERENCE_INVALID", Severity: "error", Path: path, Message: fmt.Sprintf("built-in reference %s belongs to Action %s", reference, contract.Action)})
				continue
			}
			if !contract.Phases[phase] {
				diagnostics = append(diagnostics, authoringDiagnostic{Code: "E_DOCUMENT_ACTION_PHASE_UNSUPPORTED", Severity: "error", Path: path, Message: fmt.Sprintf("built-in reference %s does not support %s", reference, phase)})
			}
		}
	}
	return diagnostics
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
