package graphengine

import (
	"testing"
)

// TestAppendUnique adds a value if not already present.
func TestAppendUnique(t *testing.T) {
	got := appendUnique([]string{"a", "b"}, "c")
	if len(got) != 3 || got[2] != "c" {
		t.Fatalf("got %v", got)
	}
}

// TestAppendUnique_SkipsDuplicate does not add duplicate values.
func TestAppendUnique_SkipsDuplicate(t *testing.T) {
	got := appendUnique([]string{"a", "b", "c"}, "b")
	if len(got) != 3 {
		t.Fatalf("got %v, want 3 elements", got)
	}
}

// TestAppendUnique_EmptySlice adds to empty slice.
func TestAppendUnique_EmptySlice(t *testing.T) {
	got := appendUnique([]string{}, "first")
	if len(got) != 1 || got[0] != "first" {
		t.Fatalf("got %v", got)
	}
}

// TestAppendUnique_NilSlice adds to nil slice.
func TestAppendUnique_NilSlice(t *testing.T) {
	got := appendUnique(nil, "only")
	if len(got) != 1 || got[0] != "only" {
		t.Fatalf("got %v", got)
	}
}

// TestProjectedEdgeKey uses edge ID when present.
func TestProjectedEdgeKey(t *testing.T) {
	edge := CompiledEdge{ID: "e1", From: "a", To: "b"}
	if got := projectedEdgeKey(edge); got != "e1" {
		t.Fatalf("got %q, want e1", got)
	}
}

// TestProjectedEdgeKey_Fallback uses from->to when ID is empty.
func TestProjectedEdgeKey_Fallback(t *testing.T) {
	edge := CompiledEdge{From: "a", To: "b"}
	if got := projectedEdgeKey(edge); got != "a->b" {
		t.Fatalf("got %q, want a->b", got)
	}
}

// TestDecideRoute_StartNode returns start-route activated nodes.
func TestDecideRoute_StartNode(t *testing.T) {
	graph := &CompiledStateGraph{
		StartRoute: "sequence",
		ControlEdges: []CompiledEdge{
			{From: "__start__", To: "a", Condition: &Expression{Material: "input_ready"}},
			{From: "__start__", To: "b"},
		},
	}
	materials := []MaterialValue{
		{MaterialID: "input_ready", RevisionID: "r1", Valid: true},
	}
	decision := DecideRoute(graph, "__start__", materials)
	if len(decision.Activated) < 1 {
		t.Fatalf("expected activated nodes, got %v", decision.Activated)
	}
}

// TestDecideRoute_SkipCondition prunes edge when material is missing.
func TestDecideRoute_SkipCondition(t *testing.T) {
	graph := &CompiledStateGraph{
		StartRoute: "sequence",
		ControlEdges: []CompiledEdge{
			{From: "__start__", To: "a", Condition: &Expression{Material: "missing_mat"}},
		},
	}
	decision := DecideRoute(graph, "__start__", nil)
	if decision.Activated != nil {
		t.Fatalf("expected no activated nodes, got %v", decision.Activated)
	}
	if len(decision.Pruned) != 1 || decision.Pruned[0] != "a" {
		t.Fatalf("expected a pruned, got %v", decision.Pruned)
	}
}

// TestDecideRoute_BypassesNodeWithSatisfiedSkipIf.
func TestDecideRoute_BypassesNodeWithSatisfiedSkipIf(t *testing.T) {
	graph := &CompiledStateGraph{
		StartRoute: "sequence",
		Nodes: map[string]CompiledNode{
			"b": {ID: "b", SkipIf: &Expression{Material: "skip_flag"}},
		},
		ControlEdges: []CompiledEdge{
			{From: "__start__", To: "b"},
			{From: "b", To: "__end__"},
		},
	}
	materials := []MaterialValue{
		{MaterialID: "skip_flag", RevisionID: "r1", Valid: true},
	}
	decision := DecideRoute(graph, "__start__", materials)
	if len(decision.Bypassed) != 1 || decision.Bypassed[0] != "b" {
		t.Fatalf("expected b bypassed, got %v", decision.Bypassed)
	}
}

// TestSelectRouteTarget_LLMChoice freezes the selected target.
func TestSelectRouteTarget_LLMChoice(t *testing.T) {
	graph := &CompiledStateGraph{
		StartRoute: "sequence",
		Nodes: map[string]CompiledNode{
			"step_a": {ID: "step_a", Route: "choice"},
		},
		ControlEdges: []CompiledEdge{
			{From: "step_a", To: "target_1", When: "user chose 1"},
			{From: "step_a", To: "target_2", When: "user chose 2"},
		},
	}
	decision := RouteDecision{
		Activated: []string{"target_1", "target_2"},
	}
	result := SelectRouteTarget(graph, "step_a", "target_1", decision)
	if len(result.Activated) != 1 || result.Activated[0] != "target_1" {
		t.Fatalf("expected only target_1 activated, got %v", result.Activated)
	}
	if len(result.Pruned) != 1 || result.Pruned[0] != "target_2" {
		t.Fatalf("expected target_2 pruned, got %v", result.Pruned)
	}
}

// TestSelectRouteTarget_NonChoiceRoute returns decision unchanged.
func TestSelectRouteTarget_NonChoiceRoute(t *testing.T) {
	graph := &CompiledStateGraph{
		StartRoute: "sequence",
		ControlEdges: []CompiledEdge{
			{From: "__start__", To: "a"},
		},
	}
	decision := RouteDecision{Activated: []string{"a"}}
	result := SelectRouteTarget(graph, "__start__", "a", decision)
	if len(result.Activated) != 1 || result.Activated[0] != "a" {
		t.Fatalf("expected decision unchanged, got %v", result)
	}
}
