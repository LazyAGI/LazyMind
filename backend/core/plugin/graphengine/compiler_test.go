package graphengine

import (
	"os"
	"path/filepath"
	"testing"
)

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
		{Any: []Expression{{Material: "revised"}, {Material: "outline"}}},
		{Material: "references"},
	}}
	evaluation := Evaluate(expr, []MaterialValue{
		{MaterialID: "revised", RevisionID: "r1", Valid: true},
		{MaterialID: "outline", RevisionID: "o1", Valid: true},
		{MaterialID: "references", RevisionID: "x1", Valid: true},
	})
	if !evaluation.Satisfied || len(evaluation.Witnesses) != 2 || evaluation.Witnesses[0].RevisionID != "r1" {
		t.Fatalf("unexpected ordered witness: %#v", evaluation)
	}
}

func TestEvaluateSelectsEveryListRevisionDeterministically(t *testing.T) {
	expr := &Expression{Any: []Expression{{Material: "references"}, {Material: "fallback"}}}
	evaluation := Evaluate(expr, []MaterialValue{
		{MaterialID: "references", RevisionID: "r2", Valid: true},
		{MaterialID: "references", RevisionID: "r1", Valid: true},
		{MaterialID: "fallback", RevisionID: "f1", Valid: true},
	})
	if !evaluation.Satisfied || len(evaluation.Witnesses) != 2 {
		t.Fatalf("expected both list revisions as witnesses: %#v", evaluation)
	}
	if evaluation.Witnesses[0].RevisionID != "r1" || evaluation.Witnesses[1].RevisionID != "r2" {
		t.Fatalf("witness revisions must be stable: %#v", evaluation.Witnesses)
	}
}

func TestEvaluateOptionalPresentMaterialsWithoutBlocking(t *testing.T) {
	evaluation := EvaluateOptional([]MaterialRef{
		{Material: "style"},
		{Material: "missing"},
	}, []MaterialValue{{MaterialID: "style", RevisionID: "s1", Valid: true}})
	if !evaluation.Satisfied || len(evaluation.Witnesses) != 1 {
		t.Fatalf("optional inputs must not block: %#v", evaluation)
	}
	if evaluation.Witnesses[0].RevisionID != "s1" {
		t.Fatalf("present optional material must be witnessed: %#v", evaluation)
	}
}

func TestCompileUnifiedInputsPreservesAlternativesAndOptionalBindings(t *testing.T) {
	pluginYAML := `
id: unified-inputs
slots:
  - {id: outline, external: true}
  - {id: revised_outline, external: true}
  - {id: style, external: true}
  - {id: draft}
steps:
  - {id: write, label: Write}
`
	stateYAML := `
transitions:
  __start__: [{to: write}]
  write: [{to: __end__}]
steps:
  write:
    inputs:
      - material: revised_outline
        required: true
        alternatives:
          - {material: outline}
      - {material: style, required: false}
    outputs: [{material: draft}]
`
	result := Compile(pluginYAML, stateYAML, "", ProfilePublish)
	if !result.Valid {
		t.Fatalf("expected unified inputs to compile, diagnostics=%#v", result.Diagnostics)
	}
	node := result.Graph.Nodes["write"]
	if node.Input == nil || len(node.Input.Any) != 2 || node.Input.Any[0].Material != "revised_outline" || node.Input.Any[1].Material != "outline" {
		t.Fatalf("unexpected required alternatives: %#v", node.Input)
	}
	if len(node.OptionalInputs) != 1 || node.OptionalInputs[0].Material != "style" {
		t.Fatalf("unexpected optional inputs: %#v", node.OptionalInputs)
	}
}

func TestCompileRejectsAlternativesOnOptionalInput(t *testing.T) {
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
  a:
    inputs:
      - material: seed
        required: false
        alternatives: [{material: b_result}]
    outputs: []
  b: {outputs: [b_result]}
  c: {outputs: []}
  d: {outputs: [d_result]}
  e: {outputs: []}
  f: {outputs: [final]}
`
	result := Compile(validPlugin, state, "", ProfilePublish)
	found := false
	for _, diagnostic := range result.Diagnostics {
		if diagnostic.Code == "E_OPTIONAL_ALTERNATIVES_UNSUPPORTED" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected optional-alternatives diagnostic, got %#v", result.Diagnostics)
	}
}

func TestInputExpressionRejectsNestedGroupsAndBindAlias(t *testing.T) {
	nested := &Expression{All: []Expression{{Any: []Expression{
		{Material: "a"},
		{All: []Expression{{Material: "b"}, {Material: "c"}}},
	}}}}
	if diagnostics := validateInputExpressionShape(nested, "input", "step"); len(diagnostics) != 1 || diagnostics[0].Code != "E_INPUT_EXPRESSION_SHAPE" {
		t.Fatalf("expected nested input shape error, got %#v", diagnostics)
	}
	withAlias := &Expression{Material: "a", BindAs: "input"}
	diagnostics := validateExpression(withAlias, "input", "step", map[string]bool{"a": true})
	if len(diagnostics) != 1 || diagnostics[0].Code != "E_BIND_ALIAS_UNSUPPORTED" {
		t.Fatalf("expected unsupported bind alias error, got %#v", diagnostics)
	}
}

func TestProjectionShowsStaleHistoryAndRouteWithoutMaskingNewEffectiveFacts(t *testing.T) {
	graph := &CompiledStateGraph{
		StartRoute: "all",
		Nodes: map[string]CompiledNode{
			"a": {ID: "a", Route: "all"},
			"b": {ID: "b", Route: "all"},
		},
		ControlEdges: []CompiledEdge{
			{ID: "__start__->a", From: "__start__", To: "a"},
			{ID: "a->b", From: "a", To: "b"},
			{ID: "b->__end__", From: "b", To: "__end__"},
		},
	}
	projection := Project(graph, RuntimeSnapshot{
		Attempts: []AttemptFact{{StepID: "a", Status: "succeeded", Validity: "stale"}},
		Routes:   []RouteFact{{From: "a", Activated: []string{"b"}, Validity: "stale"}},
	})
	if projection.Nodes["a"].Validity != "stale" {
		t.Fatalf("stale-only attempt must project stale validity: %#v", projection.Nodes["a"])
	}
	for _, edge := range projection.Edges {
		if edge.From == "a" && edge.To == "b" && edge.State != "stale" {
			t.Fatalf("stale route must project a stale edge: %#v", edge)
		}
	}

	projection = Project(graph, RuntimeSnapshot{
		Attempts: []AttemptFact{
			{StepID: "a", Status: "succeeded", Validity: "stale"},
			{StepID: "a", Status: "succeeded", Validity: "effective"},
		},
		Routes: []RouteFact{
			{From: "a", Activated: []string{"b"}, Validity: "stale"},
			{From: "a", Activated: []string{"b"}, Validity: "effective"},
		},
	})
	if projection.Nodes["a"].Validity != "effective" {
		t.Fatalf("new effective attempt must remain effective: %#v", projection.Nodes["a"])
	}
	for _, edge := range projection.Edges {
		if edge.From == "a" && edge.To == "b" && edge.State != "active" {
			t.Fatalf("effective route must override stale history: %#v", edge)
		}
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
	projection := Project(graph, RuntimeSnapshot{
		Materials: []MaterialValue{{MaterialID: "existing", RevisionID: "r1", Valid: true}},
		Routes:    []RouteFact{{From: "__start__", Activated: decision.Activated, Pruned: decision.Pruned, Bypassed: decision.Bypassed, Validity: "effective"}},
	})
	bypassedEdges := 0
	for _, edge := range projection.Edges {
		if edge.State == "bypassed" {
			bypassedEdges++
		}
	}
	if bypassedEdges != 2 {
		t.Fatalf("both edges around a bypassed node must be visible: %#v", projection.Edges)
	}
}

func TestCompileRejectsAlwaysTrueSkip(t *testing.T) {
	plugin := `
id: skip-test
slots:
  - {id: x}
steps:
  - {id: produce}
  - {id: skipped}
`
	state := `
transitions:
  __start__: [{to: produce}]
  produce: [{to: skipped}]
  skipped: [{to: __end__}]
steps:
  produce: {outputs: [x]}
  skipped:
    skip_if: {material: x}
`
	result := Compile(plugin, state, "", ProfilePublish)
	if !hasDiagnostic(result.Diagnostics, "E_SKIP_ALWAYS_TRUE") {
		t.Fatalf("guaranteed upstream material must make skip unreachable: %#v", result.Diagnostics)
	}
}

func TestOptionalOutputDoesNotProveAlwaysTrueSkip(t *testing.T) {
	plugin := `
id: conditional-output-test
slots:
  - {id: x}
steps:
  - {id: maybe_produce}
  - {id: maybe_skip}
`
	state := `
transitions:
  __start__: [{to: maybe_produce}]
  maybe_produce: [{to: maybe_skip}]
  maybe_skip: [{to: __end__}]
steps:
  maybe_produce:
    outputs:
      - {slot: x, required: false}
  maybe_skip:
    skip_if: {material: x}
`
	result := Compile(plugin, state, "", ProfilePublish)
	if hasDiagnostic(result.Diagnostics, "E_SKIP_ALWAYS_TRUE") {
		t.Fatalf("conditional output cannot prove an always-true skip: %#v", result.Diagnostics)
	}
}

func TestBundledPluginsCompileForRuntime(t *testing.T) {
	for _, pluginID := range []string{"writer-plugin", "image-plugin"} {
		root := filepath.Join("..", "..", "..", "..", "plugins", pluginID)
		pluginYAML, err := os.ReadFile(filepath.Join(root, "plugin.yaml"))
		if err != nil {
			t.Fatalf("read %s plugin: %v", pluginID, err)
		}
		stateYAML, err := os.ReadFile(filepath.Join(root, "scenario", "state.yml"))
		if err != nil {
			t.Fatalf("read %s state: %v", pluginID, err)
		}
		scenario, _ := os.ReadFile(filepath.Join(root, "scenario", "scenario.md"))
		result := Compile(string(pluginYAML), string(stateYAML), string(scenario), ProfileRuntimeLoad)
		if !result.Valid {
			t.Fatalf("bundled plugin %s must compile: %#v", pluginID, result.Diagnostics)
		}
	}
}

func TestCompileAcceptsNaturalLanguageChoiceWithoutFallback(t *testing.T) {
	plugin := `
id: choice-test
slots:
  - {id: optional_flag, external: true}
steps:
  - {id: choose}
  - {id: selected}
`
	state := `
transitions:
  __start__: [{to: choose}]
  choose:
    - to: selected
      when: the user wants this branch
  selected: [{to: __end__}]
steps:
  choose: {route: choice}
  selected: {}
`
	result := Compile(plugin, state, "", ProfilePublish)
	if !result.Valid {
		t.Fatalf("natural-language choice hints must compile without a fallback: %#v", result.Diagnostics)
	}
}

func TestCompileAcceptsNaturalLanguageAllRouteWithoutFallback(t *testing.T) {
	plugin := `
id: all-route-test
slots:
  - {id: optional_flag, external: true}
steps:
  - {id: fanout}
  - {id: selected}
`
	state := `
transitions:
  __start__: [{to: fanout}]
  fanout:
    - to: selected
      when: the model determines this step is useful
  selected: [{to: __end__}]
steps:
  fanout: {route: all}
  selected: {}
`
	result := Compile(plugin, state, "", ProfilePublish)
	if !result.Valid {
		t.Fatalf("natural-language route hints must not require a fallback: %#v", result.Diagnostics)
	}
}

func TestCompileGraphHashIsStableAcrossMapIteration(t *testing.T) {
	plugin := `
id: stable-hash-test
slots:
  - {id: context}
  - {id: outline}
steps:
  - {id: build}
  - {id: outline}
  - {id: finish}
`
	state := `
transitions:
  __start__: [{to: build}]
  build: [{to: outline}]
  outline: [{to: finish}]
  finish: [{to: __end__}]
steps:
  build:
    outputs: [{material: context}]
  outline:
    inputs: [{material: context, required: true}]
    outputs: [{material: outline}]
  finish:
    inputs: [{material: outline, required: true}]
`
	first := Compile(plugin, state, "", ProfileRuntimeLoad)
	if !first.Valid || first.GraphHash == "" {
		t.Fatalf("initial compile failed: %#v", first.Diagnostics)
	}
	for i := 0; i < 100; i++ {
		next := Compile(plugin, state, "", ProfileRuntimeLoad)
		if next.GraphHash != first.GraphHash {
			t.Fatalf("compile %d hash=%q, want stable %q", i+1, next.GraphHash, first.GraphHash)
		}
	}
}

func TestChoiceWhenHintsExposeAllCandidatesAsReachable(t *testing.T) {
	graph := &CompiledStateGraph{
		SchemaVersion: SchemaVersion,
		StartRoute:    "choice",
		Nodes: map[string]CompiledNode{
			"write":  {ID: "write", Route: "all"},
			"revise": {ID: "revise", Route: "all"},
		},
		ControlEdges: []CompiledEdge{
			{ID: "__start__->write", From: "__start__", To: "write", When: "user accepts"},
			{ID: "__start__->revise", From: "__start__", To: "revise", When: "user requests changes"},
		},
	}
	projection := Project(graph, RuntimeSnapshot{})
	if len(projection.Ready) != 2 || projection.Ready[0] != "revise" || projection.Ready[1] != "write" {
		t.Fatalf("LLM-decided exits must all remain candidates: %#v", projection)
	}
	for _, edge := range projection.Edges {
		if edge.State != "active" || edge.When == "" {
			t.Fatalf("projected candidate edge must retain its when hint: %#v", edge)
		}
	}
	selected := SelectRouteTarget(graph, "__start__", "write", DecideRoute(graph, "__start__", nil))
	if len(selected.Activated) != 1 || selected.Activated[0] != "write" || len(selected.Pruned) != 1 || selected.Pruned[0] != "revise" {
		t.Fatalf("advancing one choice candidate must prune its siblings: %#v", selected)
	}
}

func hasDiagnostic(diagnostics []Diagnostic, code string) bool {
	for _, diagnostic := range diagnostics {
		if diagnostic.Code == code {
			return true
		}
	}
	return false
}
