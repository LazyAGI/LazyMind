package pdfir

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func tableArchive(t *testing.T, content, layout string) []byte {
	t.Helper()
	var b bytes.Buffer
	w := zip.NewWriter(&b)
	f, _ := w.Create("paper_content_list.json")
	f.Write([]byte(content))
	if layout != "" {
		f, _ = w.Create("layout.json")
		f.Write([]byte(layout))
	}
	w.Close()
	return b.Bytes()
}
func TestTableProjectionSanitizesAndPreservesMergedCells(t *testing.T) {
	d, e := AdaptZIP(tableArchive(t, `[{"type":"table","page_idx":0,"bbox":[10,10,900,900],"table_caption":["Table 1. Results"],"table_footnote":["<b>Notes</b>"],"table_body":"<table><tr><th rowspan='2'>Model<script>steal()</script></th><th colspan='2'>Score</th></tr><tr><td onclick='bad()'>A &amp; B</td><td>42</td></tr></table>"},{"type":"text","page_idx":1,"text":"We compare results in Table 1."}]`, ""), "sha", "vlm")
	if e != nil {
		t.Fatal(e)
	}
	b := d.Blocks[0]
	if d.ProjectionVersion != ProjectionVersion || b.Table == nil || b.Table.Number != "1" || b.Table.Rows[0].Cells[0].RowSpan != 2 || b.Table.Rows[0].Cells[1].ColSpan != 2 || !b.Table.Rows[0].Cells[0].Header || b.Table.Rows[1].Cells[0].Text != "A & B" {
		t.Fatalf("bad table: %+v", b.Table)
	}
	raw, _ := json.Marshal(b.Table)
	if strings.Contains(string(raw), "steal") || strings.Contains(string(raw), "onclick") || strings.Contains(string(raw), "<b>") {
		t.Fatal("unsafe markup retained")
	}
	found := false
	for _, r := range b.Table.RelatedBlocks {
		found = found || r.BlockID == d.Blocks[1].ID && r.Relation == "mention"
	}
	if !found {
		t.Fatal("explicit mention missing")
	}
}
func TestTableProjectionDoesNotGuessMultipleCaptionsOrDuplicateLabels(t *testing.T) {
	d, e := AdaptZIP(tableArchive(t, `[{"type":"table","page_idx":0,"table_caption":[],"table_body":"<table><tr><td>x</td></tr></table>"},{"type":"table","page_idx":0,"table_caption":["Table 1: a","Table 2: b"],"table_body":"<table><tr><td>y</td></tr></table>"},{"type":"text","page_idx":0,"text":"Table 1 and Table 2 explain this."}]`, ""), "s", "vlm")
	if e != nil {
		t.Fatal(e)
	}
	for _, b := range d.Blocks {
		if b.Table != nil && (b.Table.Number != "" || len(b.Table.RelatedBlocks) != 0) {
			t.Fatal("ambiguous caption assigned")
		}
	}
}
func TestTableCaptionGeometryRecoversMisgroupedCaptions(t *testing.T) {
	content := `[{"type":"table","page_idx":0,"bbox":[100,100,900,300],"table_body":"<table><tr><td>a</td></tr></table>"},{"type":"table","page_idx":0,"bbox":[100,400,900,600],"table_caption":["Table 1: First","Table 2: Second"],"table_body":"<table><tr><td>b</td></tr></table>"}]`
	layout := `{"pdf_info":[{"page_idx":0,"page_size":[1000,1000],"para_blocks":[{"type":"table","bbox":[100,100,900,300]},{"type":"table","bbox":[100,400,900,600],"blocks":[{"type":"table_caption","bbox":[100,310,900,335],"lines":[{"spans":[{"content":"Table 1: First"}]}]},{"type":"table_caption","bbox":[100,610,900,635],"lines":[{"spans":[{"content":"Table 2: Second"}]}]}]}]}]}`
	d, e := AdaptZIP(tableArchive(t, content, layout), "s", "vlm")
	if e != nil {
		t.Fatal(e)
	}
	if d.Blocks[0].Table.Number != "1" || d.Blocks[1].Table.Number != "2" || len(d.Blocks) != 4 {
		t.Fatalf("caption mapping incorrect: %+v", d.Blocks)
	}
	if d.Blocks[2].Type != "table_caption" || len(d.Blocks[2].BBox) != 4 {
		t.Fatal("missing caption source geometry")
	}
	if d.Blocks[0].ID != Digest("s:" + Revision + ":vlm:0")[:24] {
		t.Fatal("legacy block ID changed")
	}
}
func TestTableProjectionBoundsHostileSpans(t *testing.T) {
	d, e := AdaptZIP(tableArchive(t, `[{"type":"table","page_idx":0,"table_body":"<table><tr><td rowspan='9999999' colspan='999999'>x</td><td>y</td></tr></table>"}]`, ""), "s", "vlm")
	if e != nil {
		t.Fatal(e)
	}
	tb := d.Blocks[0].Table
	if tb.Rows[0].Cells[0].RowSpan != 1 || tb.Rows[0].Cells[0].ColSpan > 100 || len(tb.Warnings) == 0 {
		t.Fatal("unbounded spans")
	}
}

func TestDuplicateTableNumbersDoNotLinkMentions(t *testing.T) {
	d, e := AdaptZIP(tableArchive(t, `[{"type":"table","page_idx":0,"table_caption":["Table 1: A"]},{"type":"table","page_idx":1,"table_caption":["Table 1: B"]},{"type":"text","page_idx":0,"text":"See Table 1."}]`, ""), "s", "vlm")
	if e != nil {
		t.Fatal(e)
	}
	for _, b := range d.Blocks {
		if b.Table != nil && (len(b.Table.RelatedBlocks) != 0 || len(b.Table.Warnings) == 0) {
			t.Fatal("duplicate numbered table linked to paragraph")
		}
	}
}
func TestConflictingCaptionDirectionsStayUnverified(t *testing.T) {
	content := `[{"type":"table","page_idx":0,"bbox":[100,100,900,300],"table_caption":["Table 1: a"]},{"type":"table","page_idx":1,"bbox":[100,100,900,300],"table_caption":["Table 2: b"]}]`
	layout := `{"pdf_info":[{"page_idx":0,"page_size":[1000,1000],"para_blocks":[{"type":"table_caption","bbox":[100,310,900,330],"lines":[{"spans":[{"content":"Table 1: a"}]}]}]},{"page_idx":1,"page_size":[1000,1000],"para_blocks":[{"type":"table_caption","bbox":[100,70,900,90],"lines":[{"spans":[{"content":"Table 2: b"}]}]}]}]}`
	d, e := AdaptZIP(tableArchive(t, content, layout), "s", "vlm")
	if e != nil {
		t.Fatal(e)
	}
	for _, b := range d.Blocks {
		if b.Table.Number != "" || len(b.Table.RelatedBlocks) != 0 {
			t.Fatal("mixed caption positions guessed")
		}
	}
}
func TestTableGridBoundsAggregateSpans(t *testing.T) {
	d, e := AdaptZIP(tableArchive(t, `[{"type":"table","page_idx":0,"table_body":"<table><tr><td colspan='99' rowspan='2'>a</td><td>b</td></tr><tr><td colspan='100'>c</td><td>d</td></tr></table>"}]`, ""), "s", "vlm")
	if e != nil {
		t.Fatal(e)
	}
	tb := d.Blocks[0].Table
	if len(tb.Rows[1].Cells) != 1 || tb.Rows[1].Cells[0].ColSpan != 1 || len(tb.Warnings) == 0 {
		t.Fatal("effective grid exceeds 100 columns")
	}
}
func TestSingleCaptionOnMultipleTablesWithoutGeometryIsUnverified(t *testing.T) {
	d, e := AdaptZIP(tableArchive(t, `[{"type":"table","page_idx":0},{"type":"table","page_idx":0,"table_caption":["Table 1: May belong to the first table"]},{"type":"text","page_idx":0,"text":"See Table 1."}]`, ""), "s", "vlm")
	if e != nil {
		t.Fatal(e)
	}
	for _, b := range d.Blocks {
		if b.Table != nil && (b.Table.Number != "" || len(b.Table.RelatedBlocks) != 0) {
			t.Fatal("single caption assumed to belong to last table")
		}
	}
}
