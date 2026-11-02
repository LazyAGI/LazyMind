package workflow

import (
	"fmt"
	"slices"

	"lazymind/core/algo"
	"lazymind/core/workflow/graphengine"
)

func validateWorkflowInputCorrections(graph *graphengine.CompiledStateGraph, edit *algo.WorkflowEditContext) error {
	if edit == nil {
		return nil
	}
	seen := map[string]bool{}
	for _, correction := range edit.InputCorrections {
		node, exists := graph.Nodes[correction.NodeID]
		key := correction.NodeID + "\x00" + correction.From
		_, oldKnown := graph.MaterialTypes[correction.From]
		_, newKnown := graph.MaterialTypes[correction.To]
		if !exists || oldKnown || !newKnown || seen[key] || !slices.Contains(graphengine.Materials(node.Input), correction.From) || slices.Contains(node.Outputs, correction.To) {
			return fmt.Errorf("invalid workflow input correction for node %s", correction.NodeID)
		}
		seen[key] = true
	}
	return nil
}

func correctedRequiredInput(input *graphengine.Expression, nodeID string, edit *algo.WorkflowEditContext) *graphengine.Expression {
	if input == nil || edit == nil || len(edit.InputCorrections) == 0 {
		return input
	}
	var copyExpression func(graphengine.Expression) graphengine.Expression
	copyExpression = func(expr graphengine.Expression) graphengine.Expression {
		for _, correction := range edit.InputCorrections {
			if correction.NodeID == nodeID && correction.From == expr.Material {
				expr.Material = correction.To
			}
		}
		expr.All = append([]graphengine.Expression(nil), expr.All...)
		expr.Any = append([]graphengine.Expression(nil), expr.Any...)
		for i := range expr.All {
			expr.All[i] = copyExpression(expr.All[i])
		}
		for i := range expr.Any {
			expr.Any[i] = copyExpression(expr.Any[i])
		}
		return expr
	}
	corrected := copyExpression(*input)
	return &corrected
}

type workflowInputCorrectionOption struct {
	NodeID     string   `json:"node_id"`
	MaterialID string   `json:"material_id"`
	Choices    []string `json:"choices"`
}

func workflowInputCorrectionOptions(graph *graphengine.CompiledStateGraph) []workflowInputCorrectionOption {
	options := []workflowInputCorrectionOption{}
	if graph == nil {
		return options
	}
	ids := make([]string, 0, len(graph.Nodes))
	for id := range graph.Nodes {
		ids = append(ids, id)
	}
	slices.Sort(ids)
	for _, id := range ids {
		node := graph.Nodes[id]
		for _, material := range graphengine.Materials(node.Input) {
			if _, exists := graph.MaterialTypes[material]; exists {
				continue
			}
			choices := []string{}
			for known := range graph.MaterialTypes {
				if !slices.Contains(node.Outputs, known) {
					choices = append(choices, known)
				}
			}
			slices.Sort(choices)
			options = append(options, workflowInputCorrectionOption{id, material, choices})
		}
	}
	return options
}
