package main

import (
	"encoding/json"
	"lazymind/core/common/orm"
	"testing"
)

// Algorithm emits only labels for table/figure entries, and omits default
// empty IR blocks. These response shapes are valid at the Core boundary.
func TestDocumentNumberingAlgorithmOptionalFields(t *testing.T) {
	for _, representation := range []string{"markdown", "ir"} {
		t.Run(representation, func(t *testing.T) {
			f := newNumberingFixture(t, representation, false)
			f.numbering["entries"].(map[string]any)["figure-1"] = map[string]any{"label": "1"}
			if representation == "ir" {
				f.canonical = map[string]any{"document_id": "empty-doc", "title": "Empty"}
				f.view = f.canonical
			}
			newNumberingServer(t, f, f.update())
			result := rewriteData(t, f.post(t.Context(), "execute", "descriptor-owner", f.body("execute", f.update()), ""))
			requireNumberingView(t, f, result)
			var revision orm.WorkflowSlotRevision
			if err := f.db.First(&revision, "id = ?", result["artifact_id"]).Error; err != nil {
				t.Fatal(err)
			}
			var human orm.WorkflowHumanArtifact
			if revision.HumanArtifactID == nil {
				t.Fatal("missing human")
			}
			if err := f.db.First(&human, "id = ?", *revision.HumanArtifactID).Error; err != nil {
				t.Fatal(err)
			}
			var stored map[string]any
			if err := json.Unmarshal(human.Value, &stored); err != nil {
				t.Fatal(err)
			}
			key := "text"
			if representation == "ir" {
				key = "data"
			}
			if !jsonEqualNumbering(stored[key], f.canonical) {
				t.Error("optional response changed canonical content")
			}
		})
	}
}

func TestDocumentExecuteResponseVariantsDoNotOverlap(t *testing.T) {
	f := newNumberingFixture(t, "markdown", false)
	raw, err := buildOpenAPISpecFromRouter(f.router)
	if err != nil {
		t.Fatal(err)
	}
	var spec map[string]any
	if err := json.Unmarshal(raw, &spec); err != nil {
		t.Fatal(err)
	}
	schemas := spec["components"].(map[string]any)["schemas"].(map[string]any)
	// Numbering includes rewrite's three identity fields. A closed rewrite
	// schema prevents that payload from matching both branches of oneOf.
	rewrite := documentActionResultSchemaForTest(t, map[string]any{"$ref": "#/components/schemas/DocumentRewriteExecuteResult"}, schemas, "rewrite_selection")
	if rewrite["additionalProperties"] != false {
		t.Fatal("numbering response also matches rewrite schema")
	}
}
