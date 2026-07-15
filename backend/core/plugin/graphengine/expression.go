package graphengine

import (
	"fmt"
	"sort"
)

func expressionMaterials(expr *Expression) []string {
	if expr == nil {
		return nil
	}
	seen := map[string]bool{}
	var out []string
	var walk func(Expression)
	walk = func(e Expression) {
		if e.Material != "" && !seen[e.Material] {
			seen[e.Material] = true
			out = append(out, e.Material)
		}
		for _, child := range e.All {
			walk(child)
		}
		for _, child := range e.Any {
			walk(child)
		}
	}
	walk(*expr)
	return out
}

func Materials(expr *Expression) []string { return expressionMaterials(expr) }

func validateExpression(expr *Expression, path, nodeID string, known map[string]bool) []Diagnostic {
	if expr == nil {
		return nil
	}
	var out []Diagnostic
	var walk func(Expression, string)
	walk = func(e Expression, p string) {
		kinds := 0
		if e.Material != "" {
			kinds++
		}
		if len(e.All) > 0 {
			kinds++
		}
		if len(e.Any) > 0 {
			kinds++
		}
		if kinds != 1 {
			out = append(out, Diagnostic{Code: "E_EXPRESSION_INVALID", Severity: "error", Path: p, NodeID: nodeID, Message: "expression node must contain exactly one of material, all, or any", Fixable: true})
			return
		}
		if e.BindAs != "" {
			out = append(out, Diagnostic{Code: "E_BIND_ALIAS_UNSUPPORTED", Severity: "error", Path: p + ".bind_as", NodeID: nodeID, Message: "bind_as is no longer supported; reference materials by their unique ids", Details: map[string]any{"bind_as": e.BindAs}, Fixable: true})
		}
		if e.Material != "" && !known[e.Material] {
			out = append(out, Diagnostic{Code: "E_MATERIAL_UNKNOWN", Severity: "error", Path: p + ".material", NodeID: nodeID, MaterialID: e.Material, Message: "expression references an unknown material: " + e.Material, Fixable: true})
		}
		for i, child := range e.All {
			walk(child, fmt.Sprintf("%s.all[%d]", p, i))
		}
		for i, child := range e.Any {
			walk(child, fmt.Sprintf("%s.any[%d]", p, i))
		}
	}
	walk(*expr, path)
	return out
}

// validateInputExpressionShape keeps required inputs intentionally simple:
// AND(required groups), where each group is either one material or OR(materials).
func validateInputExpressionShape(expr *Expression, path, nodeID string) []Diagnostic {
	if expr == nil {
		return nil
	}
	isMaterial := func(item Expression) bool {
		return item.Material != "" && len(item.All) == 0 && len(item.Any) == 0
	}
	isGroup := func(item Expression) bool {
		if isMaterial(item) {
			return true
		}
		if len(item.Any) < 2 || item.Material != "" || len(item.All) != 0 {
			return false
		}
		for _, child := range item.Any {
			if !isMaterial(child) {
				return false
			}
		}
		return true
	}
	valid := isGroup(*expr)
	if len(expr.All) > 0 && expr.Material == "" && len(expr.Any) == 0 {
		valid = true
		for _, child := range expr.All {
			if !isGroup(child) {
				valid = false
				break
			}
		}
	}
	if valid {
		return nil
	}
	return []Diagnostic{{
		Code:     "E_INPUT_EXPRESSION_SHAPE",
		Severity: "error",
		Path:     path,
		NodeID:   nodeID,
		Message:  "input_expression must be AND groups whose members are a material or OR(materials); nested groups are not supported",
		Fixable:  true,
	}}
}

// Skip conditions intentionally have one flat operator: all(materials) or
// any(materials). This mirrors the authoring UI and prevents nested logic that
// is difficult to explain at execution time.
func validateSkipExpressionShape(expr *Expression, path, nodeID string) []Diagnostic {
	if expr == nil {
		return nil
	}
	isMaterial := func(item Expression) bool {
		return item.Material != "" && len(item.All) == 0 && len(item.Any) == 0
	}
	valid := isMaterial(*expr)
	children := expr.All
	if len(expr.Any) > 0 {
		children = expr.Any
	}
	if len(children) > 0 && expr.Material == "" && !(len(expr.All) > 0 && len(expr.Any) > 0) {
		valid = true
		for _, child := range children {
			if !isMaterial(child) {
				valid = false
				break
			}
		}
	}
	if valid {
		return nil
	}
	return []Diagnostic{{
		Code: "E_SKIP_EXPRESSION_SHAPE", Severity: "error", Path: path, NodeID: nodeID,
		Message: "skip_if must be one flat all(materials) or any(materials) expression; nested groups are not supported", Fixable: true,
	}}
}

// Evaluate uses declaration order for any-expressions and returns the selected
// material revisions as the execution witness.
func Evaluate(expr *Expression, materials []MaterialValue) Evaluation {
	if expr == nil {
		return Evaluation{Satisfied: true}
	}
	available := map[string][]MaterialValue{}
	for _, value := range materials {
		if value.Valid {
			available[value.MaterialID] = append(available[value.MaterialID], value)
		}
	}
	for materialID := range available {
		sort.SliceStable(available[materialID], func(i, j int) bool {
			return available[materialID][i].RevisionID < available[materialID][j].RevisionID
		})
	}
	var eval func(Expression) Evaluation
	eval = func(e Expression) Evaluation {
		if e.Material != "" {
			if values := available[e.Material]; len(values) > 0 {
				witnesses := make([]Witness, 0, len(values))
				for _, value := range values {
					witnesses = append(witnesses, Witness{MaterialID: e.Material, RevisionID: value.RevisionID, BindAs: e.BindAs})
				}
				return Evaluation{Satisfied: true, Witnesses: witnesses}
			}
			return Evaluation{MissingGroups: [][]string{{e.Material}}}
		}
		if len(e.All) > 0 {
			result := Evaluation{Satisfied: true}
			for _, child := range e.All {
				part := eval(child)
				result.Witnesses = append(result.Witnesses, part.Witnesses...)
				if !part.Satisfied {
					result.Satisfied = false
					result.MissingGroups = append(result.MissingGroups, part.MissingGroups...)
				}
			}
			return result
		}
		for _, child := range e.Any {
			part := eval(child)
			if part.Satisfied {
				if e.BindAs != "" {
					for i := range part.Witnesses {
						part.Witnesses[i].BindAs = e.BindAs
					}
				}
				return part
			}
		}
		group := expressionMaterials(&e)
		sort.Strings(group)
		return Evaluation{MissingGroups: [][]string{group}}
	}
	return eval(*expr)
}

// EvaluateOptional returns provenance for every currently available optional
// material. Optional inputs never affect readiness, but an attempt that actually
// consumes one must persist the same revision witnesses so rewind propagation is
// precise.
func EvaluateOptional(refs []MaterialRef, materials []MaterialValue) Evaluation {
	result := Evaluation{Satisfied: true}
	for _, ref := range refs {
		part := Evaluate(&Expression{Material: ref.Material, BindAs: ref.BindAs}, materials)
		if part.Satisfied {
			result.Witnesses = append(result.Witnesses, part.Witnesses...)
		}
	}
	return result
}
