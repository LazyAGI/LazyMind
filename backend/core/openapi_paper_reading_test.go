package main

import "testing"

func TestPaperReadingOpenAPIContract(t *testing.T) {
	schemas := paperReadingOpenAPISchemas()
	if schemas["PDFPaperGuide"] != nil {
		t.Fatal("guide belongs to the separate paper-guide branch")
	}
	paths := paperReadingOpenAPIPaths()
	for _, path := range []string{"/datasets/{dataset}/documents/{document}/pdf-ir/blocks/{block}/explanation", "/datasets/{dataset}/documents/{document}/pdf-ir/blocks/{block}/translation"} {
		if paths[path] == nil {
			t.Fatalf("missing route contract: %s", path)
		}
	}
}

func TestReadingPreparationCapabilityStatusContract(t *testing.T) {
	schemas := paperReadingOpenAPISchemas()
	status := schemas["ReadingPreparationStatus"].(map[string]any)
	if status["properties"].(map[string]any)["capabilities"] == nil {
		t.Fatal("preparation response must expose per-capability status")
	}
	if schemas["ReadingCapabilityStatus"] == nil {
		t.Fatal("missing capability status schema")
	}
}
