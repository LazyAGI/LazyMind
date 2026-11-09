package doc

import (
	"encoding/json"
	"fmt"
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

func TestVisualContextBoundsProseWithoutLosingTargetAndExplicitReferences(t *testing.T) {
	d := pdfir.Document{Blocks: []pdfir.Block{{ID: "v", Type: "image", Page: 0, Image: &pdfir.Image{Captions: []string{"Architecture"}, RelatedBlocks: []pdfir.TableRelation{{BlockID: "description", Relation: "mention"}}}}}}
	for i := 0; i < 20; i++ {
		id := fmt.Sprintf("p%d", i)
		if i == 19 {
			id = "description"
		}
		d.Blocks = append(d.Blocks, pdfir.Block{ID: id, Type: "text", Page: 0, Text: strings.Repeat("x", 5000)})
	}
	_, ev, err := paperVisualContext(d, "v")
	if err != nil || len(ev) > 8 || ev[0].ID != "v" || ev[1].ID != "description" {
		t.Fatalf("bad context selection: %v %v", ev, err)
	}
	count := 0
	for _, block := range ev {
		n := len([]rune(block.Text))
		count += n
		if block.ID != "v" && n > 1500 {
			t.Fatal("unbounded prose")
		}
	}
	if count > 12000 {
		t.Fatal("unbounded context")
	}
}

func TestVisualContextFollowsProseAcrossPageBreaks(t *testing.T) {
	d := pdfir.Document{Blocks: []pdfir.Block{
		{ID: "v", Type: "image", Page: 2, Image: &pdfir.Image{Captions: []string{"Architecture"}}},
		{ID: "h", Type: "heading", Page: 2, Text: "Encoder"},
		{ID: "encoder", Type: "text", Page: 2, Text: "Encoder computation"},
		{ID: "h2", Type: "heading", Page: 3, Text: "Decoder"},
		{ID: "decoder", Type: "text", Page: 3, Text: "Decoder uses cross attention"},
		{ID: "caption", Type: "image_caption", Page: 2, Text: "Projected caption"},
	}}
	_, ev, err := paperVisualContext(d, "v")
	if err != nil {
		t.Fatal(err)
	}
	ids := map[string]bool{}
	for _, e := range ev {
		ids[e.ID] = true
	}
	if !ids["encoder"] || !ids["decoder"] || ids["caption"] {
		t.Fatalf("incorrect nearby context: %v", ids)
	}
}

func TestVisualTableEvidencePreservesMergedCellsOverFlattenedText(t *testing.T) {
	block := pdfir.Block{ID: "table", Type: "table", Text: "flattened OCR", Table: &pdfir.Table{
		Captions: []string{"Results"}, Footnotes: []string{"Same training run"},
		Rows: []pdfir.TableRow{{Cells: []pdfir.TableCell{
			{Text: "Model", RowSpan: 2, ColSpan: 1, Header: true},
			{Text: "Score", RowSpan: 1, ColSpan: 2, Header: true},
		}}, {Cells: []pdfir.TableCell{{Text: "A", RowSpan: 1, ColSpan: 1}, {Text: "B", RowSpan: 1, ColSpan: 1}}}},
	}}
	text := paperBlockText(block)
	for _, want := range []string{"Results", "rowspan=2", "colspan=2", "Model", "Score", "A | B", "Same training run"} {
		if !strings.Contains(text, want) {
			t.Errorf("table evidence missing %q: %s", want, text)
		}
	}
}
