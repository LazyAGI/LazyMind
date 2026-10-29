package main

import "testing"

func TestPDFIROpenAPIContract(t *testing.T) {
	schemas := generatedOpenAPISchemas(t)
	state, ok := schemas["PDFIRState"].(map[string]any)
	if !ok {
		t.Fatal("missing PDF IR state schema")
	}
	props := state["properties"].(map[string]any)
	for _, field := range []string{"status", "source_sha256", "document", "error_message"} {
		if props[field] == nil {
			t.Fatalf("missing state field %s", field)
		}
	}
	for _, field := range []string{"api_key", "ir_path", "raw_path", "payload_json"} {
		if props[field] != nil {
			t.Fatalf("private field %s exposed", field)
		}
	}
	block := schemas["PDFIRBlock"].(map[string]any)["properties"].(map[string]any)
	if block["latex"] == nil || block["bbox"] == nil || block["page"] == nil {
		t.Fatal("formula geometry contract missing")
	}
}
