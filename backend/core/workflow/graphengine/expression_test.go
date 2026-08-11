package graphengine

import (
	"testing"
)

// TestMaterials_Nil returns nil.
func TestMaterials_Nil(t *testing.T) {
	if got := Materials(nil); got != nil {
		t.Fatalf("got %v, want nil", got)
	}
}

// TestMaterials_Single extracts one material.
func TestMaterials_Single(t *testing.T) {
	expr := &Expression{Material: "result_a"}
	got := Materials(expr)
	if len(got) != 1 || got[0] != "result_a" {
		t.Fatalf("got %v, want [result_a]", got)
	}
}

// TestMaterials_All extracts materials from nested All.
func TestMaterials_All(t *testing.T) {
	expr := &Expression{All: []Expression{
		{Material: "a"},
		{Material: "b"},
	}}
	got := Materials(expr)
	if len(got) != 2 {
		t.Fatalf("got %v, want 2 materials", got)
	}
}

// TestMaterials_Any extracts materials from nested Any.
func TestMaterials_Any(t *testing.T) {
	expr := &Expression{Any: []Expression{
		{Material: "x"},
		{Material: "y"},
		{Material: "z"},
	}}
	got := Materials(expr)
	if len(got) != 3 {
		t.Fatalf("got %v, want 3 materials", got)
	}
}

// TestMaterials_Dedup returns each material only once.
func TestMaterials_Dedup(t *testing.T) {
	expr := &Expression{All: []Expression{
		{Material: "shared"},
		{Material: "shared"},
	}}
	got := Materials(expr)
	if len(got) != 1 || got[0] != "shared" {
		t.Fatalf("got %v, want [shared]", got)
	}
}

// TestValidateExpression_Nil returns nil.
func TestValidateExpression_Nil(t *testing.T) {
	known := map[string]bool{"a": true}
	if got := validateExpression(nil, "", "n1", known); got != nil {
		t.Fatalf("got %v, want nil", got)
	}
}

// TestValidateExpression_UnknownMaterial produces an error diagnostic.
func TestValidateExpression_UnknownMaterial(t *testing.T) {
	expr := &Expression{Material: "unknown_mat"}
	known := map[string]bool{"a": true}
	diags := validateExpression(expr, "$.expr", "n1", known)
	if len(diags) != 1 || diags[0].Code != "E_MATERIAL_UNKNOWN" {
		t.Fatalf("got %v, want E_MATERIAL_UNKNOWN", diags)
	}
}

// TestValidateExpression_KnownMaterial returns no diagnostic.
func TestValidateExpression_KnownMaterial(t *testing.T) {
	expr := &Expression{Material: "a"}
	known := map[string]bool{"a": true}
	diags := validateExpression(expr, "$.expr", "n1", known)
	if len(diags) != 0 {
		t.Fatalf("expected no diagnostics, got %v", diags)
	}
}

// TestValidateExpression_BindAs produces unsupported diagnostic.
func TestValidateExpression_BindAs(t *testing.T) {
	expr := &Expression{Material: "a", BindAs: "alias"}
	known := map[string]bool{"a": true}
	diags := validateExpression(expr, "$.e", "n1", known)
	found := false
	for _, d := range diags {
		if d.Code == "E_BIND_ALIAS_UNSUPPORTED" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected E_BIND_ALIAS_UNSUPPORTED, got %v", diags)
	}
}

// TestValidateExpression_MultipleKinds returns an invalid diagnostic when expression has multiple kinds.
func TestValidateExpression_MultipleKinds(t *testing.T) {
	expr := &Expression{Material: "a", All: []Expression{{Material: "b"}}}
	known := map[string]bool{"a": true, "b": true}
	diags := validateExpression(expr, "$.e", "n1", known)
	found := false
	for _, d := range diags {
		if d.Code == "E_EXPRESSION_INVALID" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected E_EXPRESSION_INVALID, got %v", diags)
	}
}

// TestValidateInputExpressionShape_ValidAndMaterial returns nil.
func TestValidateInputExpressionShape_ValidAndMaterial(t *testing.T) {
	expr := &Expression{All: []Expression{
		{Material: "a"},
		{Material: "b"},
	}}
	if got := validateInputExpressionShape(expr, "$", "n1"); got != nil {
		t.Fatalf("got %v, want nil", got)
	}
}

// TestValidateInputExpressionShape_Nil returns nil.
func TestValidateInputExpressionShape_Nil(t *testing.T) {
	if got := validateInputExpressionShape(nil, "$", "n1"); got != nil {
		t.Fatalf("got %v, want nil", got)
	}
}

// TestValidateInputExpressionShape_InvalidShape returns error diagnostic.
func TestValidateInputExpressionShape_InvalidShape(t *testing.T) {
	// Nested All inside All is not a valid input shape.
	expr := &Expression{All: []Expression{
		{All: []Expression{{Material: "a"}}},
	}}
	diags := validateInputExpressionShape(expr, "$", "n1")
	if len(diags) != 1 || diags[0].Code != "E_INPUT_EXPRESSION_SHAPE" {
		t.Fatalf("got %v, want E_INPUT_EXPRESSION_SHAPE", diags)
	}
}

// TestValidateSkipExpressionShape_ValidMaterial returns nil.
func TestValidateSkipExpressionShape_ValidMaterial(t *testing.T) {
	expr := &Expression{Material: "flag_a"}
	if got := validateSkipExpressionShape(expr, "$", "n1"); got != nil {
		t.Fatalf("got %v, want nil", got)
	}
}

// TestValidateSkipExpressionShape_Nil returns nil.
func TestValidateSkipExpressionShape_Nil(t *testing.T) {
	if got := validateSkipExpressionShape(nil, "$", "n1"); got != nil {
		t.Fatalf("got %v, want nil", got)
	}
}

// TestEvaluate_Nil returns satisfied.
func TestEvaluate_Nil(t *testing.T) {
	result := Evaluate(nil, nil)
	if !result.Satisfied {
		t.Fatal("expected satisfied for nil expression")
	}
}

// TestEvaluate_SingleMaterial returns satisfied with witness.
func TestEvaluate_SingleMaterial(t *testing.T) {
	expr := &Expression{Material: "output_a"}
	materials := []MaterialValue{{MaterialID: "output_a", RevisionID: "r1", Valid: true}}
	result := Evaluate(expr, materials)
	if !result.Satisfied || len(result.Witnesses) != 1 || result.Witnesses[0].RevisionID != "r1" {
		t.Fatalf("unexpected result: %+v", result)
	}
}

// TestEvaluate_MissingMaterial returns unsatisfied with MissingGroups.
func TestEvaluate_MissingMaterial(t *testing.T) {
	expr := &Expression{Material: "missing"}
	materials := []MaterialValue{{MaterialID: "other", RevisionID: "r1", Valid: true}}
	result := Evaluate(expr, materials)
	if result.Satisfied {
		t.Fatal("expected unsatisfied")
	}
	if len(result.MissingGroups) == 0 {
		t.Fatal("expected MissingGroups")
	}
}

// TestEvaluate_AllAnd returns satisfied only when all materials present.
func TestEvaluate_AllAnd(t *testing.T) {
	expr := &Expression{All: []Expression{
		{Material: "a"},
		{Material: "b"},
	}}
	materials := []MaterialValue{
		{MaterialID: "a", RevisionID: "r1", Valid: true},
		{MaterialID: "b", RevisionID: "r2", Valid: true},
	}
	result := Evaluate(expr, materials)
	if !result.Satisfied || len(result.Witnesses) != 2 {
		t.Fatalf("unexpected result: %+v", result)
	}
}

// TestEvaluate_AnyOr returns first satisfied branch.
func TestEvaluate_AnyOr(t *testing.T) {
	expr := &Expression{Any: []Expression{
		{Material: "primary"},
		{Material: "fallback"},
	}}
	materials := []MaterialValue{
		{MaterialID: "fallback", RevisionID: "r2", Valid: true},
	}
	result := Evaluate(expr, materials)
	if !result.Satisfied || len(result.Witnesses) != 1 {
		t.Fatalf("unexpected result: %+v", result)
	}
}

// TestEvaluate_InvalidMaterialExcluded ignores non-valid materials.
func TestEvaluate_InvalidMaterialExcluded(t *testing.T) {
	expr := &Expression{Material: "output_a"}
	materials := []MaterialValue{{MaterialID: "output_a", RevisionID: "r1", Valid: false}}
	result := Evaluate(expr, materials)
	if result.Satisfied {
		t.Fatal("expected unsatisfied when material is invalid")
	}
}

// TestEvaluateOptional returns satisfied with witnesses for available materials.
func TestEvaluateOptional(t *testing.T) {
	refs := []MaterialRef{
		{Material: "opt_a", BindAs: "alias"},
		{Material: "opt_b"},
	}
	materials := []MaterialValue{
		{MaterialID: "opt_a", RevisionID: "r1", Valid: true},
	}
	result := EvaluateOptional(refs, materials)
	if !result.Satisfied || len(result.Witnesses) != 1 {
		t.Fatalf("unexpected result: %+v", result)
	}
}
