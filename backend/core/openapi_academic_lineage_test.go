package main

import (
	"reflect"
	"testing"
)

func TestAcademicOpenAPIDistinguishesQueryReceiptsFromFrozenDiscoveries(t *testing.T) {
	schemas := generatedOpenAPISchemas(t)
	operation := academicWorkflowPaths()["/academic/expansions/{expansion_id}"].(map[string]any)["get"].(map[string]any)
	response := operation["responses"].(map[string]any)["200"].(map[string]any)
	properties := response["content"].(map[string]any)["application/json"].(map[string]any)["schema"].(map[string]any)["properties"].(map[string]any)
	if properties["source_queries"].(map[string]any)["items"].(map[string]any)["$ref"] != "#/components/schemas/AcademicSourceQueryReceipt" {
		t.Fatal("expansion context must expose query receipt IDs for investigation bindings")
	}
	receipt := schemaPropertiesForTest(t, schemas, "AcademicSourceQueryReceipt")
	for _, field := range []string{"id", "query", "mode", "paper_id", "created_at", "sources", "paper_ids"} {
		if receipt[field] == nil {
			t.Fatalf("query receipt lost %s", field)
		}
	}
	report := schemaPropertiesForTest(t, schemas, "AcademicExpansionReport")
	for _, field := range []string{"source_queries", "reused_source_queries"} {
		if report[field].(map[string]any)["items"].(map[string]any)["$ref"] != "#/components/schemas/AcademicDiscoveryQuery" {
			t.Fatalf("frozen %s must describe the persisted discovery observations", field)
		}
	}
	discovery := schemaPropertiesForTest(t, schemas, "AcademicDiscoveryQuery")
	if discovery["cache_key"] == nil || discovery["papers"] == nil || discovery["sources"] == nil {
		t.Fatal("frozen discovery contract omitted cache identity, papers or source outcomes")
	}
}

func TestAcademicOpenAPIResearchAnalysisAndReportPreserveVersionedEvidenceAndSelection(t *testing.T) {
	schemas := generatedOpenAPISchemas(t)
	for _, name := range []string{"AcademicRelationAnalysis", "AcademicExpansionReport"} {
		properties := schemaPropertiesForTest(t, schemas, name)
		if properties["input"].(map[string]any)["$ref"] != "#/components/schemas/AcademicReviewInput" || properties["analysis_version"] == nil {
			t.Fatalf("%s lost the checked complete input or analysis version", name)
		}
		if properties["completion"].(map[string]any)["$ref"] != "#/components/schemas/AcademicResearchCompletion" {
			t.Fatalf("%s lost its checked research completion outcome", name)
		}
		if properties["selection"].(map[string]any)["$ref"] != "#/components/schemas/AcademicPaperSelection" {
			t.Fatalf("%s lost its frozen candidate changes", name)
		}
		for field, item := range map[string]string{"source_versions": "AcademicSourceVersion", "coverage_gaps": "AcademicCoverageGap", "investigation": "AcademicInvestigation", "claims": "AcademicResearchClaim"} {
			if properties[field].(map[string]any)["items"].(map[string]any)["$ref"] != "#/components/schemas/"+item {
				t.Fatalf("%s.%s lost its structured contract", name, field)
			}
		}
	}
	completion := schemaPropertiesForTest(t, schemas, "AcademicResearchCompletion")
	if !reflect.DeepEqual(completion["status"].(map[string]any)["enum"], []any{"complete", "incomplete"}) || !reflect.DeepEqual(completion["reason"].(map[string]any)["enum"], []any{"scope_covered", "budget_exhausted", "material_limited"}) {
		t.Fatal("research completion must distinguish research coverage from actual stopping reasons")
	}
	summary := schemaPropertiesForTest(t, schemas, "AcademicChatSummary")
	if summary["completion"].(map[string]any)["$ref"] != "#/components/schemas/AcademicResearchCompletion" || summary["research_complete"] == nil || summary["coverage_gaps"] == nil {
		t.Fatal("Chat summary must expose known research outcomes separately from execution status")
	}
	versions := schemaPropertiesForTest(t, schemas, "AcademicSourceVersion")
	for _, field := range []string{"paper_id", "status", "content_hash", "source_version"} {
		if versions[field] == nil {
			t.Fatalf("source version pin lost %s", field)
		}
	}
	investigation := schemaPropertiesForTest(t, schemas, "AcademicInvestigation")
	if investigation["query_ids"] == nil || investigation["relation_ids"] == nil || investigation["unresolved_clues"] == nil {
		t.Fatal("investigation lost its query/relation bindings or unresolved clues")
	}
	if investigation["clue_resolutions"].(map[string]any)["items"].(map[string]any)["$ref"] != "#/components/schemas/AcademicClueResolution" {
		t.Fatal("investigation must carry dispositions for original named clues")
	}
	clue := schemaPropertiesForTest(t, schemas, "AcademicClueResolution")
	gap := schemaPropertiesForTest(t, schemas, "AcademicCoverageGap")
	if clue["source_paper_id"] == nil || clue["name"] == nil || clue["reason"] == nil || gap["reason"] == nil || gap["clue_name"] == nil || gap["question_id"] == nil {
		t.Fatal("named clue dispositions and visible gaps lost their provenance or concrete reasons")
	}
	selection := schemaPropertiesForTest(t, schemas, "AcademicPaperSelection")
	if selection["before_count"] == nil || selection["changes"].(map[string]any)["items"].(map[string]any)["$ref"] != "#/components/schemas/AcademicPaperSelectionChange" {
		t.Fatal("selection must bind its baseline count and per-paper changes")
	}
	change := schemaPropertiesForTest(t, schemas, "AcademicPaperSelectionChange")
	if !reflect.DeepEqual(change["action"].(map[string]any)["enum"], []any{"added", "removed"}) || change["paper"].(map[string]any)["$ref"] != "#/components/schemas/AcademicWorkflowPaper" || change["reason"] == nil {
		t.Fatal("candidate changes must retain action, paper metadata and reason")
	}
	claim := schemaPropertiesForTest(t, schemas, "AcademicResearchClaim")
	if claim["evidence"].(map[string]any)["items"].(map[string]any)["$ref"] != "#/components/schemas/AcademicSourceEvidence" {
		t.Fatal("claim evidence must expose its own source hash, version and content positions")
	}
}
