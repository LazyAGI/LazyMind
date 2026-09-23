package workflow

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"lazymind/core/algo"
	"lazymind/core/workflow/graphengine"
)

const (
	stepIODiagnosticStartInputOptional     = "W_STEP_IO_START_INPUT_OPTIONAL"
	stepIODiagnosticRequiredExternalOption = "W_STEP_IO_REQUIRED_EXTERNAL_OPTIONAL"
)

func finalizeStepIOAfterStateMachine(ctx context.Context, workflowYAML, stateYAML string, llmConfig map[string]any) (string, string, []string) {
	workflowYAML, stateYAML, applied, _ := applyDeterministicStepIORepairs(workflowYAML, stateYAML)
	warnings := make([]string, 0, len(applied))
	for _, item := range applied {
		warnings = append(warnings, "步骤输入输出校验："+item)
	}

	diagnostics := diagnoseStepIOFidelity(workflowYAML, stateYAML)
	if len(diagnostics) > 0 {
		repairResp, repairErr := algo.RepairStateMachine(ctx, algo.RepairStateMachineRequest{
			WorkflowYAML: workflowYAML,
			StateYAML:    stateYAML,
			RepairHint: strings.Join([]string{
				"只修复步骤输入输出链路，不改变工作流业务目标。",
				"确保每个步骤的必填输入来自外部输入或前序步骤输出。",
				"可以修改 workflow.yaml slots、scenario/state.yml steps.inputs、steps.outputs，以及必要的输入输出名称。",
				"不要删除已有步骤，不要新增不必要步骤。",
				"返回完整 workflow.yaml 和 scenario/state.yml。",
			}, "\n"),
			Warnings:    stepIODiagnosticMessages(diagnostics),
			Diagnostics: repairDiagnosticsPayload(diagnostics),
			Target:      "statemachine",
			LLMConfig:   llmConfig,
		})
		if repairErr == nil {
			nextWorkflowYAML, nextStateYAML := workflowYAML, stateYAML
			if strings.TrimSpace(repairResp.WorkflowYAML) != "" {
				nextWorkflowYAML = repairResp.WorkflowYAML
			}
			if strings.TrimSpace(repairResp.StateYAML) != "" {
				nextStateYAML = repairResp.StateYAML
			}
			nextWorkflowYAML, nextStateYAML, nextApplied, _ := applyDeterministicStepIORepairs(nextWorkflowYAML, nextStateYAML)
			nextDiagnostics := diagnoseWorkflowWithProfile(nextWorkflowYAML, nextStateYAML, "", "{}", graphengine.ProfileGenerationPhase)
			if !hasDiagnosticErrorsForTarget(nextDiagnostics, "statemachine") {
				workflowYAML, stateYAML = nextWorkflowYAML, nextStateYAML
				for _, item := range nextApplied {
					warnings = append(warnings, "步骤输入输出校验："+item)
				}
				diagnostics = diagnoseStepIOFidelity(workflowYAML, stateYAML)
			}
		}
	}
	for _, item := range diagnostics {
		warnings = append(warnings, "步骤输入输出校验："+item.Message)
	}
	return workflowYAML, stateYAML, dedupeStrings(warnings)
}

func diagnoseStepIOFidelity(workflowYAML, stateYAML string) []repairDiagnostic {
	compiled := graphengine.Compile(workflowYAML, stateYAML, "", graphengine.ProfileGenerationPhase)
	if compiled.Graph == nil || hasGraphStateMachineCompileErrors(compiled.Diagnostics) {
		return nil
	}
	graph := compiled.Graph
	firstSteps := directSuccessors(graph.ControlEdges, "__start__")
	firstStepSet := map[string]bool{}
	for _, stepID := range firstSteps {
		firstStepSet[stepID] = true
	}

	var out []repairDiagnostic
	for _, stepID := range sortedNodeIDs(graph.Nodes) {
		node := graph.Nodes[stepID]
		for _, materialID := range strictRequiredExpressionMaterials(node.Input) {
			producer, ok := graph.MaterialProducers[materialID]
			if !ok {
				continue
			}
			if producer.Kind == "external" && producer.Optional {
				out = append(out, repairDiagnostic{
					Code:       stepIODiagnosticRequiredExternalOption,
					Path:       "workflow.yaml.slots",
					Message:    fmt.Sprintf("%s 的必填输入 %s 来自可选外部素材，运行时可能不会要求用户提供。", stepID, materialID),
					Severity:   "warning",
					NodeID:     stepID,
					MaterialID: materialID,
					Fixable:    true,
				})
			}
		}
		if firstStepSet[stepID] && node.Input == nil {
			candidates := optionalExternalTextInputs(graph, node)
			if len(candidates) == 1 {
				materialID := candidates[0]
				out = append(out, repairDiagnostic{
					Code:       stepIODiagnosticStartInputOptional,
					Path:       "scenario/state.yml.steps." + stepID + ".inputs",
					Message:    fmt.Sprintf("首个步骤 %s 的文本输入 %s 被标记为可选，启动请求可能不会绑定到该输入。", stepID, materialID),
					Severity:   "warning",
					NodeID:     stepID,
					MaterialID: materialID,
					Fixable:    true,
				})
			}
		}
	}
	return out
}

func applyDeterministicStepIORepairs(workflowYAML, stateYAML string) (string, string, []string, bool) {
	originalWorkflowYAML, originalStateYAML := workflowYAML, stateYAML
	diagnostics := diagnoseStepIOFidelity(workflowYAML, stateYAML)
	if len(diagnostics) == 0 {
		return workflowYAML, stateYAML, nil, false
	}
	changed := false
	applied := []string{}
	for _, item := range diagnostics {
		switch item.Code {
		case stepIODiagnosticStartInputOptional:
			if makeStateInputRequired(&stateYAML, item.NodeID, item.MaterialID) {
				changed = true
				applied = append(applied, fmt.Sprintf("已将首个步骤 %s 的 %s 输入调整为必填，确保启动请求可以绑定到该输入", item.NodeID, item.MaterialID))
			}
			if markWorkflowExternalSlotRequired(&workflowYAML, item.MaterialID) {
				changed = true
			}
		case stepIODiagnosticRequiredExternalOption:
			if markWorkflowExternalSlotRequired(&workflowYAML, item.MaterialID) {
				changed = true
				applied = append(applied, fmt.Sprintf("已将外部素材 %s 调整为必填，避免必填步骤输入被当作可选输入跳过", item.MaterialID))
			}
		}
	}
	if !changed {
		return workflowYAML, stateYAML, nil, false
	}
	if stateDiagnostics := diagnoseWorkflowWithProfile(workflowYAML, stateYAML, "", "{}", graphengine.ProfileGenerationPhase); hasDiagnosticErrorsForTarget(stateDiagnostics, "statemachine") {
		return originalWorkflowYAML, originalStateYAML, nil, false
	}
	return workflowYAML, stateYAML, dedupeStrings(applied), true
}

func makeStateInputRequired(stateYAML *string, stepID, materialID string) bool {
	var doc map[string]any
	if err := yaml.Unmarshal([]byte(*stateYAML), &doc); err != nil {
		return false
	}
	steps := doc["steps"]
	changed := false
	switch raw := steps.(type) {
	case map[string]any:
		step := mapValue(raw[stepID])
		if step != nil {
			if makeInputListItemRequired(step, materialID) {
				changed = true
			}
		}
	case []any:
		for _, item := range raw {
			step := mapValue(item)
			if step == nil || scalarAny(step["id"]) != stepID {
				continue
			}
			if makeInputListItemRequired(step, materialID) {
				changed = true
			}
		}
	}
	if !changed {
		return false
	}
	out, err := yaml.Marshal(doc)
	if err != nil {
		return false
	}
	*stateYAML = string(out)
	return true
}

func makeInputListItemRequired(step map[string]any, materialID string) bool {
	inputs := listValue(step["inputs"])
	if len(inputs) == 0 {
		return false
	}
	changed := false
	next := make([]any, 0, len(inputs))
	for _, item := range inputs {
		if scalarAny(item) == materialID {
			next = append(next, map[string]any{"slot": materialID, "required": true})
			changed = true
			continue
		}
		if input := mapValue(item); input != nil {
			id := scalarAny(firstNonNilAny(input["slot"], input["material"], input["id"]))
			if id == materialID && !boolAny(input["required"]) {
				input["required"] = true
				changed = true
			}
			next = append(next, input)
			continue
		}
		next = append(next, item)
	}
	if changed {
		step["inputs"] = next
	}
	return changed
}

func markWorkflowExternalSlotRequired(workflowYAML *string, materialID string) bool {
	var doc map[string]any
	if err := yaml.Unmarshal([]byte(*workflowYAML), &doc); err != nil {
		return false
	}
	slots := listValue(doc["slots"])
	changed := false
	for _, item := range slots {
		slot := mapValue(item)
		if slot == nil || scalarAny(slot["id"]) != materialID {
			continue
		}
		if !boolAny(slot["external"]) && scalarAny(slot["producer"]) != "external" {
			continue
		}
		if !boolAny(slot["required"]) {
			slot["required"] = true
			changed = true
		}
	}
	if !changed {
		return false
	}
	doc["slots"] = slots
	out, err := yaml.Marshal(doc)
	if err != nil {
		return false
	}
	*workflowYAML = string(out)
	return true
}

func optionalExternalTextInputs(graph *graphengine.CompiledStateGraph, node graphengine.CompiledNode) []string {
	out := []string{}
	for _, input := range node.OptionalInputs {
		materialID := strings.TrimSpace(input.Material)
		if materialID == "" {
			continue
		}
		producer, ok := graph.MaterialProducers[materialID]
		if !ok || producer.Kind != "external" {
			continue
		}
		materialType := strings.ToLower(strings.TrimSpace(graph.MaterialTypes[materialID]))
		cardinality := strings.ToLower(strings.TrimSpace(graph.MaterialCardinalities[materialID]))
		if materialType == "text" && (cardinality == "" || cardinality == "single") {
			out = append(out, materialID)
		}
	}
	return dedupeStrings(out)
}

func strictRequiredExpressionMaterials(expr *graphengine.Expression) []string {
	if expr == nil {
		return nil
	}
	seen := map[string]bool{}
	var out []string
	var walk func(graphengine.Expression)
	walk = func(current graphengine.Expression) {
		if material := strings.TrimSpace(current.Material); material != "" && !seen[material] {
			seen[material] = true
			out = append(out, material)
			return
		}
		for _, nested := range current.All {
			walk(nested)
		}
		// Any-expressions are alternatives: no single material inside them is
		// strictly required, so do not promote an alternative external input to
		// required here.
	}
	walk(*expr)
	return out
}

func directSuccessors(edges []graphengine.CompiledEdge, from string) []string {
	var out []string
	for _, edge := range edges {
		if edge.From == from && edge.To != "" && edge.To != "__end__" {
			out = append(out, edge.To)
		}
	}
	return dedupeStrings(out)
}

func sortedNodeIDs(nodes map[string]graphengine.CompiledNode) []string {
	out := make([]string, 0, len(nodes))
	for id := range nodes {
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}

func hasGraphStateMachineCompileErrors(items []graphengine.Diagnostic) bool {
	for _, item := range items {
		if item.Severity == "error" && graphDiagnosticAppliesToStateMachine(item) {
			return true
		}
	}
	return false
}

func graphDiagnosticAppliesToStateMachine(item graphengine.Diagnostic) bool {
	return strings.HasPrefix(item.Path, "scenario/state.yml") ||
		strings.HasPrefix(item.Code, "E_GRAPH_") ||
		strings.HasPrefix(item.Code, "E_EDGE_") ||
		strings.HasPrefix(item.Code, "E_STEP_") ||
		strings.HasPrefix(item.Code, "E_ROUTE_") ||
		strings.HasPrefix(item.Code, "E_SKIP_") ||
		strings.HasPrefix(item.Code, "E_MATERIAL_") ||
		strings.HasPrefix(item.Code, "E_EXPRESSION_") ||
		strings.HasPrefix(item.Code, "E_BIND_")
}

func stepIODiagnosticMessages(items []repairDiagnostic) []string {
	out := make([]string, 0, len(items))
	for _, item := range items {
		out = append(out, item.Message)
	}
	return out
}

func dedupeStrings(items []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(items))
	for _, item := range items {
		item = strings.TrimSpace(item)
		if item == "" || seen[item] {
			continue
		}
		seen[item] = true
		out = append(out, item)
	}
	return out
}
