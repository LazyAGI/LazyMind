package pdfir

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"reflect"
	"strings"
	"testing"
)

// Synthetic producer archive: content_list contains only the parent's first
// page rectangle, but physical preproc spans retain the continuation provenance.
func precisionFixture(t *testing.T, mutate func(map[string]any)) ([]byte, Document) {
	return precisionFixtureWithText(t, mutate, "第一页😀 hyphenated transparency $x_{i}^2$")
}

func precisionFixtureWithText(t *testing.T, mutate func(map[string]any), text string) ([]byte, Document) {
	t.Helper()
	var layout map[string]any
	json.Unmarshal([]byte(`{"_backend":"hybrid","_version_name":"3.4.4","pdf_info":[{"page_idx":0,"page_size":[100,200],"preproc_blocks":[{"type":"text","bbox":[10,160,90,180],"lines":[{"spans":[{"type":"text","content":"第一页😀 hyphen-","bbox":[10,160,90,170]}]}]}],"para_blocks":[{"type":"text","bbox":[10,160,90,180],"lines":[{"spans":[{"type":"text","content":"第一页😀 hyphen-","bbox":[10,160,90,170]}]},{"spans":[{"type":"text","content":"ated transparency","bbox":[20,20,180,40],"cross_page":true},{"type":"inline_equation","content":"x_{i}^2","bbox":[20,40,100,60],"cross_page":true}]}]}]},{"page_idx":1,"page_size":[200,400],"preproc_blocks":[{"type":"text","bbox":[20,20,180,60],"lines":[{"spans":[{"type":"text","content":"ated transparency","bbox":[20,20,180,40]},{"type":"inline_equation","content":"x_{i}^2","bbox":[20,40,100,60]}]}]}],"para_blocks":[]}]}`), &layout)
	if mutate != nil {
		mutate(layout)
	}
	raw, _ := json.Marshal(layout)
	var out bytes.Buffer
	w := zip.NewWriter(&out)
	f, _ := w.Create("layout.json")
	f.Write(raw)
	f, _ = w.Create("fixture_content_list.json")
	items, _ := json.Marshal([]map[string]any{{"type": "text", "text": text, "page_idx": 0, "bbox": []int{100, 800, 900, 900}}})
	f.Write(items)
	w.Close()
	d, err := AdaptZIP(out.Bytes(), strings.Repeat("a", 64), "vlm")
	if err != nil {
		t.Fatal(err)
	}
	return out.Bytes(), d
}
func TestPrecisionSplitsPhysicalPagesWithoutChangingParent(t *testing.T) {
	raw, base := precisionFixture(t, nil)
	before, _ := json.Marshal(base)
	enriched, err := ProjectSourcePrecision(base, raw)
	if err != nil {
		t.Fatal(err)
	}
	after, _ := json.Marshal(base)
	if !bytes.Equal(before, after) || !reflect.DeepEqual(base.Blocks, enriched.Blocks) {
		t.Fatal("parent changed")
	}
	if enriched.Precision == nil || len(enriched.Precision.Regions) != 2 {
		t.Fatalf("missing regions: %+v", enriched.Precision)
	}
	r := enriched.Precision.Regions[1]
	if r.Page != 1 || !strings.Contains(r.Text, "transparency") || !strings.Contains(r.Text, "x_{i}^2") || r.ParentBlockID != base.Blocks[0].ID {
		t.Fatalf("bad region: %+v", r)
	}
	if !reflect.DeepEqual(r.Spans[0].BBox, []float64{.1, .05, .9, .1}) {
		t.Fatalf("wrong page dimensions: %+v", r.Spans[0])
	}
	if len(enriched.Pages) != 2 {
		t.Fatal("continuation page absent")
	}
	again, err := ProjectSourcePrecision(base, raw)
	if err != nil || !reflect.DeepEqual(enriched, again) {
		t.Fatal("non deterministic")
	}
}
func TestPrecisionRejectsUnprovenPhysicalPage(t *testing.T) {
	for _, kind := range []string{"missing_preproc", "missing_flag", "wrong_flag", "ambiguous", "bad_size", "bad_geometry", "wrong_base"} {
		t.Run(kind, func(t *testing.T) {
			raw, base := precisionFixture(t, func(l map[string]any) {
				ps := l["pdf_info"].([]any)
				p0 := ps[0].(map[string]any)
				p1 := ps[1].(map[string]any)
				spans := p0["para_blocks"].([]any)[0].(map[string]any)["lines"].([]any)[1].(map[string]any)["spans"].([]any)
				switch kind {
				case "missing_preproc":
					p1["preproc_blocks"] = []any{}
				case "missing_flag":
					delete(spans[0].(map[string]any), "cross_page")
				case "wrong_flag":
					spans[0].(map[string]any)["cross_page"] = false
				case "ambiguous":
					copyRaw, _ := json.Marshal(p1)
					var p2 map[string]any
					json.Unmarshal(copyRaw, &p2)
					p2["page_idx"] = 2.
					l["pdf_info"] = append(ps, p2)
				case "bad_size":
					p1["page_size"] = []any{0., 400.}
				case "bad_geometry":
					spans[0].(map[string]any)["bbox"] = []any{-1., 0., 20., 40.}
				}
			})
			if kind == "wrong_base" {
				base.Blocks[0].Text = "altered"
			}
			if _, err := ProjectSourcePrecision(base, raw); err == nil {
				t.Fatal("accepted unproven precision")
			}
		})
	}
}

func TestPrecisionRejectsCrossPageAssetFootnoteWithoutFlag(t *testing.T) {
	raw, base := precisionFixture(t, func(l map[string]any) {
		p := l["pdf_info"].([]any)[0].(map[string]any)
		var footnote any
		json.Unmarshal([]byte(`{"type":"table_footnote","bbox":[20,20,180,40],"lines":[{"spans":[{"type":"text","content":"ated transparency","bbox":[20,20,180,40]}]}]}`), &footnote)
		p["para_blocks"] = append(p["para_blocks"].([]any), footnote)
	})
	if _, err := ProjectSourcePrecision(base, raw); err == nil {
		t.Fatal("asset footnote was left on false home page")
	}
}

func TestPrecisionRejectsMergedTableWithoutFootnote(t *testing.T) {
	raw, base := precisionFixture(t, func(l map[string]any) {
		p := l["pdf_info"].([]any)[0].(map[string]any)
		var merged, physical any
		json.Unmarshal([]byte(`{"type":"table_body","bbox":[10,10,90,80],"lines":[{"spans":[{"type":"table","html":"<table><tr><td>home</td></tr><tr><td>next page</td></tr></table>","bbox":[10,10,90,80]}]}]}`), &merged)
		json.Unmarshal([]byte(`{"type":"table_body","bbox":[10,10,90,80],"lines":[{"spans":[{"type":"table","html":"<table><tr><td>home</td></tr></table>","bbox":[10,10,90,80]}]}]}`), &physical)
		p["para_blocks"] = append(p["para_blocks"].([]any), merged)
		p["preproc_blocks"] = append(p["preproc_blocks"].([]any), physical)
	})
	if _, err := ProjectSourcePrecision(base, raw); err == nil {
		t.Fatal("multi-page table masqueraded as a first-page asset")
	}
}

func TestPrecisionThreePhysicalPagesAndRepeatedHomeText(t *testing.T) {
	raw, base := precisionFixtureWithText(t, func(l map[string]any) {
		ps := l["pdf_info"].([]any)
		home := ps[0].(map[string]any)
		next := ps[1].(map[string]any)
		b, _ := json.Marshal(next)
		var third map[string]any
		json.Unmarshal(b, &third)
		third["page_idx"] = 3.
		third["page_size"] = []any{400., 800.}
		nextSpan := third["preproc_blocks"].([]any)[0].(map[string]any)["lines"].([]any)[0].(map[string]any)["spans"].([]any)
		nextSpan = nextSpan[:1]
		nextSpan[0].(map[string]any)["content"] = "third physical region 😀"
		third["preproc_blocks"].([]any)[0].(map[string]any)["lines"].([]any)[0].(map[string]any)["spans"] = nextSpan
		copySpan, _ := json.Marshal(nextSpan[0])
		var continuation map[string]any
		json.Unmarshal(copySpan, &continuation)
		continuation["cross_page"] = true
		para := home["para_blocks"].([]any)[0].(map[string]any)
		para["lines"] = append(para["lines"].([]any), map[string]any{"spans": []any{continuation}})
		// Identical original text/box on another page must not invalidate a proven
		// unflagged home occurrence, or consume the other page's occurrence.
		third["preproc_blocks"] = append(third["preproc_blocks"].([]any), home["preproc_blocks"].([]any)[0])
		l["pdf_info"] = append(ps, third)
	}, "第一页😀 hyphenated transparency $x_{i}^2$ third physical region 😀")
	d, err := ProjectSourcePrecision(base, raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(d.Precision.Regions) != 3 || d.Precision.Regions[2].Page != 3 || len(d.Precision.Regions[0].Spans) != 1 || len(d.Precision.Regions[1].Spans) != 2 {
		t.Fatalf("coverage changed: %+v", d.Precision)
	}
	if d.Precision.Regions[2].Spans[0].BBox[0] != .05 {
		t.Fatal("used the parent's page dimensions")
	}
}
func TestPrecisionBoundsAndUnsafeArchives(t *testing.T) {
	source, original := precisionFixture(t, nil)
	for _, name := range []string{"missing", "duplicate", "unsafe", "members", "large", "json", "depth"} {
		t.Run(name, func(t *testing.T) {
			raw, base := source, original
			if name == "depth" {
				raw, base = precisionFixture(t, func(l map[string]any) {
					p := l["pdf_info"].([]any)[0].(map[string]any)
					var nested any = map[string]any{"type": "container"}
					for i := 0; i < 18; i++ {
						nested = map[string]any{"type": "container", "blocks": []any{nested}}
					}
					p["preproc_blocks"] = append(p["preproc_blocks"].([]any), nested)
				})
			} else {
				var out bytes.Buffer
				w := zip.NewWriter(&out)
				z, _ := zip.NewReader(bytes.NewReader(raw), int64(len(raw)))
				for _, f := range z.File {
					if strings.HasSuffix(f.Name, "_content_list.json") {
						r, _ := f.Open()
						data, _ := io.ReadAll(r)
						r.Close()
						x, _ := w.Create(f.Name)
						x.Write(data)
					}
				}
				switch name {
				case "duplicate":
					for i := 0; i < 2; i++ {
						f, _ := w.Create(fmt.Sprintf("%d/layout.json", i))
						f.Write([]byte(`{}`))
					}
				case "unsafe":
					f, _ := w.Create("../layout.json")
					f.Write([]byte(`{}`))
				case "members":
					for i := 0; i < 4097; i++ {
						w.Create(fmt.Sprintf("item%d", i))
					}
				case "large":
					f, _ := w.Create("layout.json")
					f.Write(bytes.Repeat([]byte(" "), int(maxJSONBytes+1)))
				case "json":
					f, _ := w.Create("layout.json")
					f.Write([]byte(`{"_backend":"hybrid","_version_name":"3.4.4","pdf_info":`))
				}
				w.Close()
				raw = out.Bytes()
			}
			if _, err := ProjectSourcePrecision(base, raw); err == nil {
				t.Fatal("accepted unbounded or unverifiable archive")
			}
		})
	}
}

func TestPrecisionRejectsCanonicalTableMissingLayoutAssociation(t *testing.T) {
	var raw bytes.Buffer
	z := zip.NewWriter(&raw)
	f, _ := z.Create("paper_content_list.json")
	f.Write([]byte(`[{"type":"table","table_body":"<table><tr><td>A</td></tr></table>","page_idx":0,"bbox":[100,100,900,900]}]`))
	f, _ = z.Create("layout.json")
	f.Write([]byte(`{"_backend":"hybrid","_version_name":"3.4.4","pdf_info":[{"page_idx":0,"page_size":[100,100],"preproc_blocks":[],"para_blocks":[]}]}`))
	z.Close()
	d, err := AdaptZIP(raw.Bytes(), strings.Repeat("a", 64), "vlm")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = ProjectSourcePrecision(d, raw.Bytes()); err == nil {
		t.Fatal("table with missing layout silently accepted")
	}
}
