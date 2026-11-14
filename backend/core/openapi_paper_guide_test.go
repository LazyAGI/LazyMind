package main

import (
	"os"
	"strings"
	"testing"
)

func TestPaperGuideOpenAPIHasTypedContracts(t *testing.T) {
	schemas := paperGuideOpenAPISchemas()
	for _, name := range []string{"PaperGuideStatus", "PaperGuideRevision", "PaperGuideGenerateRequest", "PaperGuideSelectionContext", "PaperGuideEvidenceResponse"} {
		if schemas[name] == nil {
			t.Fatalf("missing %s", name)
		}
	}
	paths := paperGuideOpenAPIPaths()
	if len(paths) != 8 {
		t.Fatalf("expected 8 API operations, got %d", len(paths))
	}
}

func TestPaperGuideNullablePrimitiveSchema(t *testing.T) {
	schemas := paperGuideOpenAPISchemas()
	for name, field := range map[string]string{"PaperGuideProfiles": "primary", "PaperGuideClaim": "argument_type"} {
		properties := schemas[name].(map[string]any)["properties"].(map[string]any)
		value := properties[field].(map[string]any)
		if value["type"] != "string" || value["nullable"] != true {
			t.Fatalf("%s.%s loses nullable primitive: %+v", name, field, value)
		}
	}
}

func TestPaperGuideGeneratedClientPreservesNull(t *testing.T) {
	raw, err := os.ReadFile("../../frontend/src/api/generated/core-client/api.ts")
	if err != nil {
		t.Fatal(err)
	}
	for name, field := range map[string]string{"PaperGuideProfiles": "primary", "PaperGuideClaim": "argument_type"} {
		_, body, ok := strings.Cut(string(raw), "export interface "+name+" {")
		if !ok {
			t.Fatalf("missing generated interface %s", name)
		}
		body, _, _ = strings.Cut(body, "}")
		if !strings.Contains(body, "'"+field+"': string | null;") {
			t.Fatalf("generated %s.%s lacks null: %s", name, field, body)
		}
	}
}
