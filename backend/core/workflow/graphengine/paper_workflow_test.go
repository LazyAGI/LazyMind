package graphengine

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func paperGraph(t *testing.T, edit func(string) string) *CompiledStateGraph {
	t.Helper()
	root := filepath.Join("..", "..", "..", "..", "workflows", "paper-writing")
	workflow, err := os.ReadFile(filepath.Join(root, "workflow.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	state, err := os.ReadFile(filepath.Join(root, "scenario/state.yml"))
	if err != nil {
		t.Fatal(err)
	}
	body := string(workflow)
	if edit != nil {
		body = edit(body)
	}
	result := Compile(body, string(state), "", ProfileRuntimeLoad)
	if !result.Valid {
		t.Fatalf("invalid paper graph: %+v", result.Diagnostics)
	}
	return result.Graph
}

func TestPaperGraphAllForwardScenarios(t *testing.T) {
	graph := paperGraph(t, nil)
	paths := [][]string{
		{"research_topic", "literature_review", "refine_topic", "experiment", "data_analysis", "write_outline", "write_document", "peer_review", "revise_document"},
		{"research_topic", "literature_review", "refine_topic"},
		{"literature_review", "write_document", "peer_review", "revise_document"},
		{"experiment", "data_analysis", "write_document"}, {"revise_document"}, {"write_document", "revise_document"}, {"reading_reflection"}, {"literature_review", "reading_reflection"},
		{"experiment", "data_analysis"},
		{"literature_review", "experiment", "data_analysis"},
	}
	for _, node := range []string{"research_topic", "literature_review", "experiment", "write_document", "peer_review", "reading_reflection"} {
		paths = append(paths, []string{node})
	}
	for _, path := range paths {
		t.Run(strings.Join(path, "-"), func(t *testing.T) {
			snapshot := RuntimeSnapshot{Materials: []MaterialValue{{MaterialID: "paper_input", RevisionID: "input-1", Valid: true}}}
			steps := append([]string{}, path...)
			steps = append(steps, "finish")
			previous := "__start__"
			for _, step := range steps {
				choice := SelectRouteTarget(graph, previous, step, DecideRoute(graph, previous, snapshot.Materials))
				snapshot.Routes = append(snapshot.Routes, RouteFact{
					From: previous, Activated: choice.Activated, Pruned: choice.Pruned, Validity: "effective",
				})
				projected := Project(graph, snapshot)
				if projected.Nodes[step].Reachability != "reachable" || projected.Nodes[step].Readiness != "ready" {
					t.Fatalf("%s not executable: %+v", step, projected.Nodes[step])
				}
				snapshot.Attempts = append(snapshot.Attempts, AttemptFact{StepID: step, Status: "succeeded", Validity: "effective"})
				for _, slot := range graph.Nodes[step].Outputs {
					snapshot.Materials = append(snapshot.Materials, MaterialValue{MaterialID: slot, RevisionID: slot + "-1", Valid: true})
				}
				previous = step
			}
		})
	}
}

func TestPaperDataAnalysisRequiresPreparation(t *testing.T) {
	graph := paperGraph(t, nil)
	snapshot := RuntimeSnapshot{Materials: []MaterialValue{{MaterialID: "paper_input", RevisionID: "input-1", Valid: true}}}
	candidates := DecideRoute(graph, "__start__", snapshot.Materials)
	if slices.Contains(candidates.Activated, "data_analysis") || !slices.Contains(candidates.Activated, "experiment") {
		t.Fatalf("analysis must enter through experiment preparation: %+v", candidates)
	}
	choice := SelectRouteTarget(graph, "__start__", "experiment", candidates)
	snapshot.Routes = []RouteFact{{From: "__start__", Activated: choice.Activated, Pruned: choice.Pruned, Validity: "effective"}}
	projected := Project(graph, snapshot)
	if node := projected.Nodes["experiment"]; node.Reachability != "reachable" || node.Readiness != "ready" {
		t.Fatalf("preparation is not executable: %+v", node)
	}
	if node := projected.Nodes["data_analysis"]; node.Reachability != "unreachable" || node.Readiness == "ready" {
		t.Fatalf("analysis is executable before preparation finishes: %+v", node)
	}
}

func TestRecoveryPolicyValidationAndLegacyHash(t *testing.T) {
	plugin := "id: recovery-test\nslots:\n  - {id: feedback, type: text, cardinality: single}\n  - {id: input, type: text, cardinality: single, external: true}\nsteps:\n  - {id: draft}\n"
	state := "steps:\n  draft:\n    inputs: [{material: input, required: false}]\n    outputs: [feedback]\ntransitions:\n  __start__: [{to: draft}]\n  draft: [{to: __end__}]\n"
	legacy := Compile(plugin, state, "", ProfileRuntimeLoad)
	empty := Compile(plugin+"runtime: {recovery_inputs: {}}\n", state, "", ProfileRuntimeLoad)
	if !legacy.Valid || !empty.Valid || legacy.Graph.GraphHash != empty.Graph.GraphHash {
		t.Fatal("empty opt-in changes legacy graph hash")
	}
	valid := Compile(plugin+"runtime:\n  recovery_inputs:\n    draft: {input: feedback}\n", state, "", ProfileRuntimeLoad)
	if !valid.Valid || valid.Graph.Runtime.IsZero() {
		t.Fatalf("policy not retained: %+v", valid.Diagnostics)
	}
	for _, binding := range []string{
		"unknown: {input: feedback}", "draft: {feedback: input}", "draft: {input: missing}", "draft: {missing: feedback}",
	} {
		result := Compile(plugin+"runtime:\n  recovery_inputs:\n    "+binding+"\n", state, "", ProfileRuntimeLoad)
		if result.Valid {
			t.Fatalf("invalid policy accepted: %s", binding)
		}
	}
}

func TestPaperWritingRequiresAnInputSource(t *testing.T) {
	graph := paperGraph(t, nil)
	for _, source := range []string{"", "paper_topic", "paper_input", "seed_pool", "literature_review_pool", "data_analysis_pool", "manuscript_text", "manuscript_file", "write_outline_pool", "write_outline_document"} {
		t.Run(source, func(t *testing.T) {
			snapshot := RuntimeSnapshot{}
			if source != "" {
				snapshot.Materials = []MaterialValue{{MaterialID: source, RevisionID: "input-1", Valid: true}}
			}
			node := Project(graph, snapshot).Nodes["write_document"]
			wantReady := source != "" && source != "paper_topic"
			if (node.Readiness == "ready") != wantReady {
				t.Fatalf("source %q: %+v", source, node)
			}
		})
	}
}

func TestPaperOutlineConfirmationPath(t *testing.T) {
	graph := paperGraph(t, nil)
	if graph.Nodes["write_outline"].Mode != "human" {
		t.Fatal("outline must use the existing human approval boundary")
	}
	snapshot := RuntimeSnapshot{Materials: []MaterialValue{{MaterialID: "paper_input", RevisionID: "input-1", Valid: true}}}
	previous := "__start__"
	for _, step := range []string{"write_outline", "write_document", "peer_review"} {
		choice := SelectRouteTarget(graph, previous, step, DecideRoute(graph, previous, snapshot.Materials))
		snapshot.Routes = append(snapshot.Routes, RouteFact{From: previous, Activated: choice.Activated, Pruned: choice.Pruned, Validity: "effective"})
		if node := Project(graph, snapshot).Nodes[step]; node.Readiness != "ready" {
			t.Fatalf("%s not ready: %+v", step, node)
		}
		snapshot.Attempts = append(snapshot.Attempts, AttemptFact{StepID: step, Status: "succeeded", Validity: "effective"})
		for _, slot := range graph.Nodes[step].Outputs {
			if step == "write_outline" && slot == "write_document_document" {
				t.Fatal("outline step must not publish a manuscript")
			}
			snapshot.Materials = append(snapshot.Materials, MaterialValue{MaterialID: slot, RevisionID: slot + "-1", Valid: true})
		}
		previous = step
	}
}
