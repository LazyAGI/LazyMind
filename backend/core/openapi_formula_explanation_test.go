package main

import "testing"

func TestFormulaExplanationFocusOpenAPIContract(t *testing.T) {
	schema := formulaExplanationOpenAPISchemas()["PDFFormulaExplanationRequest"].(map[string]any)
	properties := schema["properties"].(map[string]any)
	focus, ok := properties["focus_symbol"].(map[string]any)
	if !ok || focus["type"] != "string" || focus["maxLength"] != 500 || focus["minLength"] != 1 {
		t.Fatalf("missing bounded optional focus contract: %#v", focus)
	}
	if required, ok := schema["required"].([]string); ok {
		for _, name := range required {
			if name == "focus_symbol" {
				t.Fatal("focus must remain optional")
			}
		}
	}
}
