package graphengine

import (
	"encoding/json"
	"testing"
)

// TestCompiledStateGraph_JSON marshals to valid JSON.
func TestCompiledStateGraph_JSON(t *testing.T) {
	graph := &CompiledStateGraph{
		SchemaVersion: SchemaVersion,
		StartRoute:    "sequence",
		Nodes: map[string]CompiledNode{
			"a": {ID: "a", Label: "Step A", Route: "sequence"},
		},
		ControlEdges: []CompiledEdge{
			{ID: "e1", From: "__start__", To: "a"},
		},
	}
	b := graph.JSON()
	if b == nil || len(b) == 0 {
		t.Fatal("expected non-empty JSON")
	}
	var parsed map[string]any
	if err := json.Unmarshal(b, &parsed); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if parsed["schema_version"] != SchemaVersion {
		t.Fatalf("schema_version mismatch: %v", parsed)
	}
}

// TestCompiledStateGraph_JSON_Empty marshals empty graph.
func TestCompiledStateGraph_JSON_Empty(t *testing.T) {
	graph := &CompiledStateGraph{}
	b := graph.JSON()
	if len(b) == 0 {
		t.Fatal("expected non-empty JSON for empty graph")
	}
}

// TestEvaluation_SatisfiedDefaults is false when not explicitly set (Go zero value).
func TestEvaluation_SatisfiedDefaults(t *testing.T) {
	ev := Evaluation{}
	if ev.Satisfied {
		t.Fatal("zero-value Evaluation.Satisfied should be false")
	}
}

// TestRouteDecision_ActivatedDefaults is nil when not set.
func TestRouteDecision_ActivatedDefaults(t *testing.T) {
	rd := RouteDecision{}
	if rd.Activated != nil {
		t.Fatalf("expected nil Activated, got %v", rd.Activated)
	}
}

// TestDiagnostic_JSON marshals correctly with optional fields.
func TestDiagnostic_JSON(t *testing.T) {
	d := Diagnostic{
		Code:     "E_TEST",
		Severity: "error",
		Path:     "$.steps[0]",
		NodeID:   "n1",
		Message:  "test error",
		Fixable:  true,
	}
	b, _ := json.Marshal(d)
	var parsed Diagnostic
	if err := json.Unmarshal(b, &parsed); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if parsed.Code != "E_TEST" || !parsed.Fixable {
		t.Fatalf("roundtrip mismatch: %+v", parsed)
	}
}

// TestMaterialValue_JSON roundtrips correctly.
func TestMaterialValue_JSON(t *testing.T) {
	mv := MaterialValue{MaterialID: "m1", RevisionID: "r1", Valid: true}
	b, _ := json.Marshal(mv)
	var restored MaterialValue
	json.Unmarshal(b, &restored)
	if restored.MaterialID != "m1" || !restored.Valid {
		t.Fatalf("roundtrip: %+v", restored)
	}
}
