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
