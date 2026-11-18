package main

import (
	"os"
	"slices"
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

func TestPaperGuideOpenAPIResumeBlockIsOptional(t *testing.T) {
	schema := paperGuideOpenAPISchemas()["PaperGuideRunView"].(map[string]any)
	properties := schema["properties"].(map[string]any)
	field, ok := properties["resume_block_reason"].(map[string]any)
	if !ok || field["type"] != "string" || slices.Contains(schema["required"].([]string), "resume_block_reason") {
		t.Fatalf("resume_block_reason must be an optional string: %+v", schema)
	}
	raw, err := os.ReadFile("../../frontend/src/api/generated/core-client/api.ts")
	if err != nil {
		t.Fatal(err)
	}
	_, body, ok := strings.Cut(string(raw), "export interface PaperGuideRunView {")
	body, _, _ = strings.Cut(body, "}")
	if !ok || !strings.Contains(body, "'resume_block_reason'?: string;") {
		t.Fatalf("generated client lacks the optional resume block: %s", body)
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

func TestPaperGuideInteractionIsOptionalAndTyped(t *testing.T) {
	schemas := paperGuideOpenAPISchemas()
	block := schemas["PaperGuideBlock"].(map[string]any)
	field, ok := block["properties"].(map[string]any)["interaction"].(map[string]any)
	if !ok || field["nullable"] != true || slices.Contains(block["required"].([]string), "interaction") {
		t.Fatalf("interaction must be optional and nullable on a block: %+v", block)
	}
	interaction := schemas["PaperGuideInteraction"].(map[string]any)
	properties := interaction["properties"].(map[string]any)
	if len(interaction["required"].([]string)) != 11 || interaction["additionalProperties"] != false {
		t.Fatalf("interaction fields must be strict and required: %+v", interaction)
	}
	if properties["rows"].(map[string]any)["maxItems"] != 40 ||
		schemas["PaperGuideInteractionRow"].(map[string]any)["properties"].(map[string]any)["value"].(map[string]any)["type"] != "number" {
		t.Fatalf("result rows must be bounded and numeric: %+v", properties["rows"])
	}
	kind := properties["kind"].(map[string]any)
	if values, ok := kind["enum"].([]string); !ok || !slices.Contains(values, "calculator") {
		t.Fatalf("expected a typed interaction kind: %+v", kind)
	}
}

func TestFlashpapersPublicSchema(t *testing.T) {
	schemas := paperGuideOpenAPISchemas()
	plan := schemas["PaperGuidePlan"].(map[string]any)["properties"].(map[string]any)
	for _, field := range []string{"layout", "title", "subtitle"} {
		if plan[field] == nil {
			t.Fatalf("missing plan.%s", field)
		}
	}
	block := schemas["PaperGuideBlock"].(map[string]any)["properties"].(map[string]any)
	if block["playground"] == nil || schemas["PaperGuidePlayground"] == nil {
		t.Fatal("missing playground contract")
	}
}
