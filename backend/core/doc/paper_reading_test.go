package doc

import (
	"encoding/json"
	"lazymind/core/pdfir"
	"strings"
	"testing"
)

func TestVisualContextAndValidationUseOnlyExtractedEvidence(t *testing.T) {
	d := pdfir.Document{Blocks: []pdfir.Block{{ID: "v", Type: "table", Page: 2, Table: &pdfir.Table{Captions: []string{"Table 1: Results"}, Rows: []pdfir.TableRow{{Cells: []pdfir.TableCell{{Text: "42", RowSpan: 1, ColSpan: 1}}}}, RelatedBlocks: []pdfir.TableRelation{{BlockID: "p", Relation: "mention"}}}}, {ID: "p", Type: "text", Page: 3, Text: "The result improves."}}}
	target, ev, e := paperVisualContext(d, "v")
	if e != nil || target.ID != "v" {
		t.Fatal(e)
	}
	b, _ := json.Marshal(ev)
	if !strings.Contains(string(b), "42") || !strings.Contains(string(b), "improves") {
		t.Fatal("table values or related paragraph missing")
	}
	v, e := validatePaperVisual(`{"summary":"Context","observations":[{"text":"A claim","evidence_ids":["p","foreign"],"inferred":false}],"evidence":[{"id":"p","text":"Fake"}]}`, ev, "en")
	if e != nil || len(v.Observations[0].EvidenceIDs) != 1 || !v.Observations[0].Inferred {
		t.Fatal("visual source validation failed")
	}
	if _, _, e := paperVisualContext(d, "p"); e == nil {
		t.Fatal("text block accepted as image/table")
	}
}
