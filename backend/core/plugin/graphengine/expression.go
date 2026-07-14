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
	aliases := map[string]bool{}
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
			if aliases[e.BindAs] {
				out = append(out, Diagnostic{Code: "E_BIND_ALIAS_DUPLICATE", Severity: "error", Path: p + ".bind_as", NodeID: nodeID, Message: "bind alias is duplicated: " + e.BindAs, Details: map[string]any{"bind_as": e.BindAs}, Fixable: true})
			}
			aliases[e.BindAs] = true
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

// Evaluate uses declaration order for any-expressions and returns the selected
// material revisions as the execution witness.
func Evaluate(expr *Expression, materials []MaterialValue) Evaluation {
	if expr == nil {
		return Evaluation{Satisfied: true}
	}
	available := map[string]MaterialValue{}
	for _, value := range materials {
		if value.Valid {
			available[value.MaterialID] = value
		}
	}
	var eval func(Expression) Evaluation
	eval = func(e Expression) Evaluation {
		if e.Material != "" {
			if value, ok := available[e.Material]; ok {
				return Evaluation{Satisfied: true, Witnesses: []Witness{{MaterialID: e.Material, RevisionID: value.RevisionID, BindAs: e.BindAs}}}
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
				if e.BindAs != "" && len(part.Witnesses) == 1 {
					part.Witnesses[0].BindAs = e.BindAs
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
