package main

import "testing"

func TestFormulaExplanationOpenAPIContract(t *testing.T) {
	schemas := formulaExplanationOpenAPISchemas()
	result := schemas["PDFFormulaExplanation"].(map[string]any)["properties"].(map[string]any)
	for _, name := range []string{"summary", "symbols", "steps", "evidence", "warnings", "model"} {
		if result[name] == nil {
			t.Fatalf("missing %s", name)
		}
	}
	paths := formulaExplanationOpenAPIPaths()
	path := paths["/datasets/{dataset}/documents/{document}/pdf-ir/formulas/{block}/explanation"].(map[string]any)
	op := path["post"].(map[string]any)
	if op["requestBody"] == nil || op["parameters"] == nil {
		t.Fatal("incomplete explanation operation")
	}
}
