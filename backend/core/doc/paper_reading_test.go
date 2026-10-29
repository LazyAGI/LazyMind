package doc

import (
	"encoding/json"
	"fmt"
	"lazymind/core/pdfir"
	"strings"
	"testing"
)

func TestPaperGuideContextCoversEndPagesWithinBudget(t *testing.T) {
	d := pdfir.Document{}
	for p := 0; p < 80; p++ {
		for b := 0; b < 12; b++ {
			d.Blocks = append(d.Blocks, pdfir.Block{ID: fmt.Sprintf("p%d-b%d", p, b), Type: "text", Page: p, Text: strings.Repeat(fmt.Sprintf("page %d evidence. ", p), 600)})
		}
	}
	ev, _ := paperGuideContext(d)
	seen := map[int]bool{}
	runes := 0
	for _, e := range ev {
		seen[e.Page] = true
		runes += len([]rune(e.Text))
	}
	if len(seen) != 80 || runes > 100000 || len(ev) > 400 {
		t.Fatalf("unbalanced/unbounded context: pages=%d runes=%d blocks=%d", len(seen), runes, len(ev))
	}
}
func TestGuideValidationSuppliesFiveStagesAndAuthoritativeEvidence(t *testing.T) {
	ev := []formulaEvidence{{ID: "p", Text: "Quoted source", Page: 9, BBox: []float64{.1, .2, .3, .4}}}
	raw := `{"stages":[{"key":"problem","title":"Problem","summary":"Paraphrase","evidence_ids":["p","evil"],"related_block_ids":["figure","evil"],"inferred":false},{"key":"method","title":"Method","summary":"Fabrication","evidence_ids":["evil"]}],"evidence":[{"id":"p","text":"invented","page":999}]}`
	g, e := validatePaperGuide(raw, ev, []pdfir.Block{{ID: "figure", Type: "image"}}, "en")
	if e != nil {
		t.Fatal(e)
	}
	if len(g.Stages) != 5 || g.Stages[4].Key != "limitations" || len(g.Stages[4].EvidenceIDs) != 0 || !strings.Contains(g.Stages[4].Summary, "unavailable") {
		t.Fatalf("missing stage contract: %+v", g.Stages)
	}
	if len(g.Stages[0].EvidenceIDs) != 1 || len(g.Stages[0].RelatedBlockIDs) != 1 || !g.Stages[0].Inferred || g.Evidence[0].Page != 9 || g.Evidence[0].Text != "Quoted source" {
		t.Fatal("fabricated provenance trusted")
	}
	if g.Stages[2].Summary == "Fabrication" {
		t.Fatal("unsupported claim retained")
	}
}
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

func TestGuideContextPreservesCompleteParagraphsAndRedistributesShortBlocks(t *testing.T) {
	d := pdfir.Document{Blocks: []pdfir.Block{{ID: "heading", Type: "heading", Page: 0, Text: "Method"}, {ID: "paragraph", Type: "text", Page: 0, Text: strings.Repeat("complete prose ", 5000)}, {ID: "tail", Type: "text", Page: 1, Text: "Limitations at the end."}}}
	ev, _ := paperGuideContext(d)
	if len(ev) != 3 || ev[1].Text != d.Blocks[1].Text || ev[2].Text != d.Blocks[2].Text {
		t.Fatal("fitting paper was unnecessarily truncated")
	}
	d.Blocks[1].Text = strings.Repeat("a", 140000)
	ev, _ = paperGuideContext(d)
	if len(ev[1].Text) < 99000 || ev[2].Text != d.Blocks[2].Text {
		t.Fatal("short blocks did not return unused budget")
	}
}

func TestPaperReadingPromptsRequireDistinctGroundedFindings(t *testing.T) {
	for _, phrase := range []string{"precise reported quantitative comparison", "dataset, metric, compared methods", "core mechanism", "statistical significance"} {
		if !strings.Contains(paperGuideInstruction, phrase) {
			t.Errorf("guide quality constraint missing: %s", phrase)
		}
	}
	for _, phrase := range []string{"3 to 5 distinct", "Do not enumerate duplicate captions", "purpose", "how to read", "exact reported values", "statistical significance", "no image pixels"} {
		if !strings.Contains(paperVisualInstruction, phrase) {
			t.Errorf("visual quality constraint missing: %s", phrase)
		}
	}
	if paperReadingPromptVersion == "paper-reading-v1" {
		t.Fatal("old generic explanations would remain cached")
	}
}
