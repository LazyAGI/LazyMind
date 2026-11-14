package pdfir

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

func fragmentedTableFixture(t *testing.T, mutate func(map[string]any)) ([]byte, Document) {
	t.Helper()
	var layout map[string]any
	const raw = `{"pdf_info":[{"page_idx":0,"page_size":[100,100],"preproc_blocks":[{"type":"table_body","bbox":[10,20,90,80],"lines":[{"spans":[{"type":"table","bbox":[10,20,90,80],"html":"<table><tr><th>A B</th></tr><tr><td>11</td></tr></table>"}]}]}],"para_blocks":[{"type":"table","bbox":[10,20,90,80],"blocks":[{"type":"table_body","bbox":[10,20,90,80],"lines":[{"spans":[{"type":"table","bbox":[10,20,90,80],"html":"<table><tr><th>A B</th></tr><tr><td>11</td></tr><tr><td>22</td></tr></table>"}]}]},{"type":"table_footnote","bbox":[20,170,180,190],"lines":[{"spans":[{"type":"text","content":"Footnote later.","bbox":[20,170,180,190]}]}]}]}]},{"page_idx":3,"page_size":[200,200],"preproc_blocks":[{"type":"table_body","bbox":[20,20,180,160],"lines":[{"spans":[{"type":"table","bbox":[20,20,180,160],"html":"<table><tr><th>AB</th></tr><tr><td>22</td></tr></table>"}]}]},{"type":"table_footnote","lines":[{"spans":[{"type":"text","content":"Footnote later.","bbox":[20,170,180,190]}]}]}],"para_blocks":[{"type":"table","bbox":[20,20,180,160],"cell_merge":[0],"blocks":[{"type":"table_body","bbox":[20,20,180,160],"lines_deleted":true,"lines":[]}]}]}]}`
	json.Unmarshal([]byte(raw), &layout)
	if mutate != nil {
		mutate(layout)
	}
	data, _ := json.Marshal(layout)
	return precisionPayloadArchive(t, `[{"type":"table","bbox":[100,200,900,800],"page_idx":0,"table_body":"<table><tr><th>A B</th></tr><tr><td>11</td></tr><tr><td>22</td></tr></table>","table_footnote":["Footnote later."]},{"type":"table","bbox":[100,100,900,800],"page_idx":3,"table_body":""}]`, string(data))
}

func TestPrecisionPhysicalTableFragments(t *testing.T) {
	raw, base := fragmentedTableFixture(t, nil)
	before, _ := json.Marshal(base)
	got, err := ProjectSourcePrecision(base, raw)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(base.Blocks, got.Blocks) {
		t.Fatal("canonical parent rewritten")
	}
	data, _ := json.Marshal(got.Precision)
	var p struct {
		Regions []struct {
			Kind  string          `json:"kind"`
			Page  int             `json:"page"`
			Text  string          `json:"text"`
			Table json.RawMessage `json:"table"`
		}
	}
	json.Unmarshal(data, &p)
	bodies, footnotes := 0, 0
	for _, r := range p.Regions {
		if r.Kind == "table_body_fragment" {
			bodies++
			if len(r.Table) == 0 {
				t.Fatal("missing raw typed payload")
			}
			if r.Page == 3 && !strings.Contains(r.Text, "22") {
				t.Fatal("missing later cell")
			}
		}
		if r.Kind == "table_footnote" {
			footnotes++
			if r.Page != 3 {
				t.Fatal("false footnote page")
			}
		}
	}
	if bodies != 2 || footnotes != 1 {
		t.Fatalf("body=%d footnote=%d", bodies, footnotes)
	}
	after, _ := json.Marshal(base)
	if string(before) != string(after) {
		t.Fatal("source mutated")
	}
}

func TestPhysicalTableRetainsProducerOmittedAncillary(t *testing.T) {
	raw, base := fragmentedTableFixture(t, func(l map[string]any) {
		pages := l["pdf_info"].([]any)
		for _, v := range pages {
			p := v.(map[string]any)
			blocks := p["preproc_blocks"].([]any)
			body := blocks[0].(map[string]any)
			children := append([]any{}, blocks...)
			if p["page_idx"].(float64) == 0 {
				children = append(children, map[string]any{"type": "table_footnote", "lines": []any{map[string]any{"spans": []any{map[string]any{"type": "text", "content": "Original first-page note.", "bbox": []int{10, 85, 90, 95}}}}}})
			}
			p["preproc_blocks"] = []any{map[string]any{"type": "table", "bbox": body["bbox"], "blocks": children}}
		}
	})
	got, e := ProjectSourcePrecision(base, raw)
	if e != nil {
		t.Fatal(e)
	}
	found := false
	for _, r := range got.Precision.Regions {
		if r.Text == "Original first-page note." && r.Page == 0 {
			found = true
		}
	}
	if !found {
		t.Fatal("producer-omitted physical footnote lost")
	}
}

func TestTableFragmentAssociationRejectsUnprovenChains(t *testing.T) {
	for _, name := range []string{"nonzero cell merge", "extra cell", "missing deletion proof", "duplicate candidate", "wrong body geometry"} {
		t.Run(name, func(t *testing.T) {
			raw, base := fragmentedTableFixture(t, func(l map[string]any) {
				pages := l["pdf_info"].([]any)
				p := pages[1].(map[string]any)
				para := p["para_blocks"].([]any)[0].(map[string]any)
				child := para["blocks"].([]any)[0].(map[string]any)
				switch name {
				case "nonzero cell merge":
					para["cell_merge"] = []int{1}
				case "extra cell":
					span := p["preproc_blocks"].([]any)[0].(map[string]any)["lines"].([]any)[0].(map[string]any)["spans"].([]any)[0].(map[string]any)
					span["html"] = "<table><tr><th>AB</th></tr><tr><td>22</td></tr><tr><td>uncovered</td></tr></table>"
				case "missing deletion proof":
					delete(child, "lines_deleted")
				case "duplicate candidate":
					p["preproc_blocks"] = append(p["preproc_blocks"].([]any), p["preproc_blocks"].([]any)[0])
				case "wrong body geometry":
					child["bbox"] = []int{21, 20, 180, 160}
				}
			})
			if _, e := ProjectSourcePrecision(base, raw); e == nil {
				t.Fatal("unproven fragment accepted")
			}
		})
	}
}
func TestSourceTableRejectsMalformedUnsafeAndBoundedInputs(t *testing.T) {
	for _, s := range []string{
		`<table><tr><td>unclosed`, `<table><tr><td><script>bad</script></td></tr></table>`,
		`<table><tr><td><table><tr><td>nested</td></tr></table></td></tr></table>`,
		`<table><tr><td rowspan="999">x</td></tr></table>`,
		`<table><tr><td>` + strings.Repeat(`<span>`, 65) + `deep` + strings.Repeat(`</span>`, 65) + `</td></tr></table>`,
		`<table><tr><td>` + strings.Repeat("x", maxTableHTML) + `</td></tr></table>`,
		`<table><tr>` + strings.Repeat(`<td>x</td>`, 101) + `</tr></table>`,
	} {
		if _, _, e := ParseSourceTable(s); e == nil {
			t.Fatalf("unsafe/incomplete table accepted %.60s", s)
		}
	}
}

func TestTableFragmentRejectsWrongPhysicalAncestor(t *testing.T) {
	raw, base := fragmentedTableFixture(t, func(l map[string]any) {
		p := l["pdf_info"].([]any)[0].(map[string]any)
		body := p["preproc_blocks"].([]any)[0]
		p["preproc_blocks"] = []any{map[string]any{"type": "table", "bbox": []int{10, 20, 90, 80}, "blocks": []any{}}, map[string]any{"type": "table", "bbox": []int{1, 1, 9, 9}, "blocks": []any{body}}}
	})
	if _, e := ProjectSourcePrecision(base, raw); e == nil {
		t.Fatal("foreign table ancestor authenticated")
	}
}

func TestThreeAndFourPhysicalPagesPreserveDuplicateRowsAndOtherTable(t *testing.T) {
	for _, count := range []int{3, 4} {
		t.Run(fmt.Sprint(count), func(t *testing.T) {
			pages := []any{}
			items := []any{}
			header := "<tr><th>Value</th></tr>"
			row := "<tr><td>22</td></tr>"
			merged := "<table>" + header
			for i := 0; i < count; i++ {
				merged += fmt.Sprintf("<tr><td>marker %d</td></tr>", i) + row
			}
			merged += "</table>"
			block := func(body string, box []int) map[string]any {
				return map[string]any{"type": "table_body", "bbox": box, "lines": []any{map[string]any{"spans": []any{map[string]any{"type": "table", "bbox": box, "html": body}}}}}
			}
			for i := 0; i < count; i++ {
				page := i * 3
				box := []int{10, 20, 90, 80}
				body := block("<table>"+header+fmt.Sprintf("<tr><td>marker %d</td></tr>", i)+row+"</table>", box)
				paraBody := block(merged, box)
				if i > 0 {
					paraBody = map[string]any{"type": "table_body", "bbox": box, "lines": []any{}, "lines_deleted": true}
				}
				para := map[string]any{"type": "table", "bbox": box, "blocks": []any{paraBody}}
				pre := []any{body}
				paras := []any{para}
				content := merged
				if i > 0 {
					content = ""
				}
				items = append(items, map[string]any{"type": "table", "page_idx": page, "bbox": []int{100, 200, 900, 800}, "table_body": content})
				if i == 1 {
					other := block("<table><tr><td>unrelated</td></tr></table>", []int{10, 81, 90, 99})
					pre = append(pre, other)
					paras = append(paras, map[string]any{"type": "table", "bbox": []int{10, 81, 90, 99}, "blocks": []any{other}})
					items = append(items, map[string]any{"type": "table", "page_idx": page, "bbox": []int{100, 810, 900, 990}, "table_body": "<table><tr><td>unrelated</td></tr></table>"})
				}
				pages = append(pages, map[string]any{"page_idx": page, "page_size": []int{100, 100}, "preproc_blocks": pre, "para_blocks": paras})
			}
			rawItems, _ := json.Marshal(items)
			layout, _ := json.Marshal(map[string]any{"pdf_info": pages})
			raw, base := precisionPayloadArchive(t, string(rawItems), string(layout))
			got, e := ProjectSourcePrecision(base, raw)
			if e != nil {
				t.Fatal(e)
			}
			duplicates := 0
			for _, r := range got.Precision.Regions {
				if strings.Contains(r.Text, "22") {
					duplicates++
				}
			}
			if duplicates != count {
				t.Fatalf("duplicate physical rows lost %d/%d", duplicates, count)
			}
		})
	}
}

func TestSourceTableRejectsModelTextOverflowWithoutTruncation(t *testing.T) {
	body := "<table>" + strings.Repeat("<tr>"+strings.Repeat("<td></td>", 100)+"</tr>", 1000) + "</table>"
	if _, _, e := ParseSourceTable(body); e == nil {
		t.Fatal("formatted table exceeds canonical RawText budget")
	}
}

func TestVerifiedIndependentEmptyTableIsNotAContinuation(t *testing.T) {
	layout := strings.ReplaceAll(payloadTableLayout, "<table><tr><td>home</td></tr></table>", "<table></table>")
	raw, base := precisionPayloadArchive(t, `[{"type":"table","table_body":"<table></table>","page_idx":0,"bbox":[100,100,900,800]}]`, layout)
	got, e := ProjectSourcePrecision(base, raw)
	if e != nil {
		t.Fatal(e)
	}
	if len(got.Blocks) != 1 || len(got.Precision.Regions) != 1 || got.Precision.Regions[0].Table.ContinuationBlockID != "" {
		t.Fatal("independent empty table collapsed")
	}
}
