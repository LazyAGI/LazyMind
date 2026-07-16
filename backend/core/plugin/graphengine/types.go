// Package graphengine is the authoritative compiler and pure runtime projector
// for plugin workflow graphs. It intentionally has no database dependencies.
package graphengine

import "encoding/json"

const SchemaVersion = "3"

type Profile string

const (
	ProfileEditor          Profile = "editor"
	ProfileGenerationPhase Profile = "generation_phase"
	ProfilePublish         Profile = "publish"
	ProfileRuntimeLoad     Profile = "runtime_load"
)

type Diagnostic struct {
	Code       string         `json:"code"`
	Severity   string         `json:"severity"`
	Path       string         `json:"path,omitempty"`
	NodeID     string         `json:"node_id,omitempty"`
	EdgeID     string         `json:"edge_id,omitempty"`
	MaterialID string         `json:"material_id,omitempty"`
	Message    string         `json:"message"`
	Details    map[string]any `json:"details,omitempty"`
	Fixable    bool           `json:"fixable"`
}

type Expression struct {
	Material string       `json:"material,omitempty" yaml:"material,omitempty"`
	All      []Expression `json:"all,omitempty" yaml:"all,omitempty"`
	Any      []Expression `json:"any,omitempty" yaml:"any,omitempty"`
	BindAs   string       `json:"bind_as,omitempty" yaml:"bind_as,omitempty"`
}

type MaterialRef struct {
	Material string `json:"material"`
	BindAs   string `json:"bind_as,omitempty"`
}

type ProducerRef struct {
	Kind   string `json:"kind"` // external | step
	StepID string `json:"step_id,omitempty"`
}

type CompiledNode struct {
	ID              string        `json:"id"`
	Label           string        `json:"label,omitempty"`
	Route           string        `json:"route"`
	Input           *Expression   `json:"input_expression,omitempty"`
	OptionalInputs  []MaterialRef `json:"optional_inputs,omitempty"`
	Outputs         []string      `json:"outputs,omitempty"`
	RequiredOutputs []string      `json:"required_outputs,omitempty"`
	SkipIf          *Expression   `json:"skip_if,omitempty"`
}

type CompiledEdge struct {
	ID   string `json:"id"`
	From string `json:"from"`
	To   string `json:"to"`
	When string `json:"when,omitempty"`
	// Condition and Legacy are retained so schema-v3 sessions remain readable.
	Condition *Expression `json:"condition,omitempty"`
	Legacy    string      `json:"legacy_condition,omitempty"`
}

type CompiledBypass struct {
	NodeID string   `json:"node_id"`
	From   []string `json:"from"`
	To     []string `json:"to"`
}

type CompiledStateGraph struct {
	SchemaVersion     string                   `json:"schema_version"`
	GraphHash         string                   `json:"graph_hash"`
	StartRoute        string                   `json:"start_route"`
	Nodes             map[string]CompiledNode  `json:"nodes"`
	ControlEdges      []CompiledEdge           `json:"control_edges"`
	MaterialProducers map[string]ProducerRef   `json:"material_producers"`
	InputExpressions  map[string]Expression    `json:"input_expressions"`
	OptionalInputs    map[string][]MaterialRef `json:"optional_inputs"`
	SkipExpansions    []CompiledBypass         `json:"skip_expansions,omitempty"`
	StaticOrder       []string                 `json:"static_order"`
}

type CompileResult struct {
	Valid         bool                `json:"valid"`
	Profile       Profile             `json:"profile"`
	SchemaVersion string              `json:"schema_version"`
	GraphHash     string              `json:"graph_hash,omitempty"`
	Diagnostics   []Diagnostic        `json:"diagnostics"`
	Graph         *CompiledStateGraph `json:"compiled_graph,omitempty"`
}

type MaterialValue struct {
	MaterialID string `json:"material_id"`
	RevisionID string `json:"revision_id"`
	Valid      bool   `json:"valid"`
}

type Witness struct {
	MaterialID string `json:"material_id"`
	RevisionID string `json:"revision_id"`
	BindAs     string `json:"bind_as,omitempty"`
}

type Evaluation struct {
	Satisfied     bool       `json:"satisfied"`
	Witnesses     []Witness  `json:"witnesses,omitempty"`
	MissingGroups [][]string `json:"missing_groups,omitempty"`
}

type AttemptFact struct {
	StepID   string `json:"step_id"`
	Status   string `json:"status"`
	Validity string `json:"validity"`
}

type RouteFact struct {
	From      string   `json:"from"`
	Activated []string `json:"activated"`
	Pruned    []string `json:"pruned,omitempty"`
	Bypassed  []string `json:"bypassed,omitempty"`
	Validity  string   `json:"validity"`
}

type RuntimeSnapshot struct {
	Attempts  []AttemptFact   `json:"attempts"`
	Materials []MaterialValue `json:"materials"`
	Routes    []RouteFact     `json:"routes"`
}

type NodeProjection struct {
	ID           string     `json:"id"`
	Execution    string     `json:"execution"`
	Validity     string     `json:"validity"`
	Reachability string     `json:"reachability"`
	Readiness    string     `json:"readiness"`
	Branch       string     `json:"branch"`
	Evaluation   Evaluation `json:"evaluation"`
}

type Projection struct {
	Past       []string                  `json:"past"`
	Current    []string                  `json:"current"`
	Reachable  []string                  `json:"reachable"`
	Ready      []string                  `json:"ready"`
	Blocked    []string                  `json:"blocked"`
	Stale      []string                  `json:"stale"`
	Pruned     []string                  `json:"pruned"`
	Bypassed   []string                  `json:"bypassed"`
	Nodes      map[string]NodeProjection `json:"nodes"`
	Edges      []ProjectedEdge           `json:"edges"`
	EndReached bool                      `json:"end_reached"`
	Completed  bool                      `json:"completed"`
}

type ProjectedEdge struct {
	From  string `json:"from"`
	To    string `json:"to"`
	State string `json:"state"` // inactive | active | pruned | bypassed | stale
	When  string `json:"when,omitempty"`
}

type RouteDecision struct {
	Activated []string  `json:"activated"`
	Pruned    []string  `json:"pruned,omitempty"`
	Bypassed  []string  `json:"bypassed,omitempty"`
	Witnesses []Witness `json:"witnesses,omitempty"`
}

func (g *CompiledStateGraph) JSON() []byte {
	b, _ := json.Marshal(g)
	return b
}
