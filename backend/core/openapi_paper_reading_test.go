package main

import "testing"

func TestPaperReadingOpenAPIContract(t *testing.T) {
	schemas := paperReadingOpenAPISchemas()
	guide := schemas["PDFPaperGuide"].(map[string]any)["properties"].(map[string]any)
	stages := guide["stages"].(map[string]any)
	if stages["minItems"] != 5 || stages["maxItems"] != 5 {
		t.Fatal("guide must have exactly five stages")
	}
	paths := paperReadingOpenAPIPaths()
	for _, path := range []string{"/datasets/{dataset}/documents/{document}/pdf-ir/guide", "/datasets/{dataset}/documents/{document}/pdf-ir/blocks/{block}/explanation", "/datasets/{dataset}/documents/{document}/pdf-ir/blocks/{block}/translation"} {
		if paths[path] == nil {
			t.Fatalf("missing route contract: %s", path)
		}
	}
}
