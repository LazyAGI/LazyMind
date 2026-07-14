package graphengine

import "testing"

const validPlugin = `
id: graph-test
slots:
  - {id: seed, external: true}
  - {id: b_result}
  - {id: d_result}
  - {id: final}
steps:
  - {id: a, label: A}
  - {id: b, label: B}
  - {id: c, label: C}
  - {id: d, label: D}
  - {id: e, label: E}
  - {id: f, label: F}
`

const validState = `
transitions:
  __start__: [{to: a}]
  a: [{to: b}, {to: c}]
  b: [{to: f}]
  c: [{to: d}, {to: e}]
  d: [{to: f}]
  e: [{to: __end__}]
  f: [{to: __end__}]
steps:
  a: {outputs: []}
  b: {outputs: [b_result]}
  c: {outputs: []}
  d: {outputs: [d_result]}
  e: {outputs: []}
  f:
    input_expression:
      all:
        - {material: b_result}
        - {material: d_result}
    outputs: [final]
`

func TestCompileArbitraryDAGAndProjectBlockedMerge(t *testing.T) {
	result := Compile(validPlugin, validState, "", ProfilePublish)
	if !result.Valid {
		t.Fatalf("expected valid graph, diagnostics=%#v", result.Diagnostics)
	}
	projection := Project(result.Graph, RuntimeSnapshot{
		Attempts: []AttemptFact{
			{StepID: "a", Status: "succeeded", Validity: "effective"},
			{StepID: "b", Status: "succeeded", Validity: "effective"},
			{StepID: "c", Status: "succeeded", Validity: "effective"},
			{StepID: "d", Status: "succeeded", Validity: "effective"},
		},
		Materials: []MaterialValue{{MaterialID: "b_result", RevisionID: "b1", Valid: true}},
	})
	if projection.Nodes["f"].Readiness != "blocked" {
		t.Fatalf("F should be reachable but blocked: %#v", projection.Nodes["f"])
	}
	projection = Project(result.Graph, RuntimeSnapshot{
		Attempts: []AttemptFact{
			{StepID: "a", Status: "succeeded", Validity: "effective"},
			{StepID: "b", Status: "succeeded", Validity: "effective"},
			{StepID: "c", Status: "succeeded", Validity: "effective"},
			{StepID: "d", Status: "succeeded", Validity: "effective"},
		},
		Materials: []MaterialValue{
			{MaterialID: "b_result", RevisionID: "b1", Valid: true},
			{MaterialID: "d_result", RevisionID: "d1", Valid: true},
		},
	})
	if projection.Nodes["f"].Readiness != "ready" {
		t.Fatalf("F should be ready: %#v", projection.Nodes["f"])
	}
}

func TestCompileRejectsMultipleProducerAndSelfOverwrite(t *testing.T) {
	state := `
transitions:
  __start__: [{to: a}]
  a: [{to: b}]
  b: [{to: c}]
  c: [{to: d}]
  d: [{to: e}]
  e: [{to: f}]
  f: [{to: __end__}]
steps:
  a: {outputs: [b_result]}
  b: {inputs: [{slot: b_result, required: true}], outputs: [b_result]}
  c: {outputs: []}
  d: {outputs: [d_result]}
  e: {outputs: []}
  f: {outputs: [final]}
`
	result := Compile(validPlugin, state, "", ProfilePublish)
	codes := map[string]bool{}
	for _, diagnostic := range result.Diagnostics {
		codes[diagnostic.Code] = true
	}
	if !codes["E_MATERIAL_MULTIPLE_PRODUCERS"] || !codes["E_MATERIAL_SELF_OVERWRITE"] {
		t.Fatalf("expected producer diagnostics, got %#v", result.Diagnostics)
	}
}

func TestEvaluateOrderedORWitness(t *testing.T) {
	expr := &Expression{All: []Expression{
		{Any: []Expression{{Material: "revised"}, {Material: "outline"}}, BindAs: "outline_input"},
		{Material: "references"},
	}}
	evaluation := Evaluate(expr, []MaterialValue{
		{MaterialID: "revised", RevisionID: "r1", Valid: true},
		{MaterialID: "outline", RevisionID: "o1", Valid: true},
		{MaterialID: "references", RevisionID: "x1", Valid: true},
	})
	if !evaluation.Satisfied || len(evaluation.Witnesses) != 2 || evaluation.Witnesses[0].RevisionID != "r1" || evaluation.Witnesses[0].BindAs != "outline_input" {
		t.Fatalf("unexpected ordered witness: %#v", evaluation)
	}
}

func TestProjectionCompletesOnlyAfterEveryEffectiveBranchEnds(t *testing.T) {
	result := Compile(validPlugin, validState, "", ProfilePublish)
	if !result.Valid {
		t.Fatalf("expected valid graph: %#v", result.Diagnostics)
	}
	partial := Project(result.Graph, RuntimeSnapshot{Attempts: []AttemptFact{
		{StepID: "a", Status: "succeeded", Validity: "effective"},
		{StepID: "b", Status: "succeeded", Validity: "effective"},
		{StepID: "c", Status: "succeeded", Validity: "effective"},
		{StepID: "d", Status: "succeeded", Validity: "effective"},
		{StepID: "e", Status: "succeeded", Validity: "effective"},
	}})
	if partial.Completed || !partial.EndReached || len(partial.Blocked) == 0 {
		t.Fatalf("one branch at end must not complete while F is blocked: %#v", partial)
	}
	complete := Project(result.Graph, RuntimeSnapshot{Attempts: []AttemptFact{
		{StepID: "a", Status: "succeeded", Validity: "effective"},
		{StepID: "b", Status: "succeeded", Validity: "effective"},
		{StepID: "c", Status: "succeeded", Validity: "effective"},
		{StepID: "d", Status: "succeeded", Validity: "effective"},
		{StepID: "e", Status: "succeeded", Validity: "effective"},
		{StepID: "f", Status: "succeeded", Validity: "effective"},
	}})
	if !complete.Completed {
		t.Fatalf("all effective leaves reached end: %#v", complete)
	}
}

func TestRouteDecisionFreezesSkipBypass(t *testing.T) {
	graph := &CompiledStateGraph{
		StartRoute: "all",
		Nodes: map[string]CompiledNode{
			"a": {ID: "a", Route: "all", SkipIf: &Expression{Material: "existing"}},
			"b": {ID: "b", Route: "all"},
		},
		ControlEdges: []CompiledEdge{{From: "__start__", To: "a"}, {From: "a", To: "b"}, {From: "b", To: "__end__"}},
	}
	decision := DecideRoute(graph, "__start__", []MaterialValue{{MaterialID: "existing", RevisionID: "r1", Valid: true}})
	if len(decision.Activated) != 1 || decision.Activated[0] != "b" || len(decision.Bypassed) != 1 || decision.Bypassed[0] != "a" {
		t.Fatalf("unexpected frozen bypass decision: %#v", decision)
	}
}
