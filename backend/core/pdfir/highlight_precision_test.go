package pdfir_test

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"reflect"
	"testing"

	"lazymind/core/paperguide"
	"lazymind/core/pdfir"
)

// The second physical span can be in another column while every payload and
// physical-page association remains valid. No PDF/parser/network is needed.
func TestCompletePhysicalTextControlsHighlight(t *testing.T) {
	for _, tc := range []struct {
		name, kind        string
		outside, bodyOnly bool
		want              string
	}{
		{"cross column", "text", true, false, "page"},
		{"contained multiline", "text", false, false, "text_span"},
		{"quantization", "text", false, false, "text_span"},
		{"formula outside", "equation", true, false, "page"},
		{"formula contained", "equation", false, false, "formula"},
		{"header outside", "header", true, false, "page"},
		{"image footnote outside", "image", true, false, "page"},
		{"image body only", "image", false, true, "figure_region"},
		{"table identity outside", "table", true, false, "page"},
		{"table identity contained", "table", false, false, "table_cells"},
		{"table body only", "table", false, true, "table_cells"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			box := []float64{10, 10, 45, 40}
			second := []float64{10, 25, 45, 35}
			if tc.outside {
				second = []float64{55, 25, 90, 35}
			}
			if tc.name == "quantization" {
				second = []float64{9.95, 25, 45.05, 35}
			}
			span := func(kind, text string, b []float64) map[string]any {
				return map[string]any{"type": kind, "content": text, "bbox": b}
			}
			line := func(s map[string]any) map[string]any { return map[string]any{"spans": []any{s}} }
			para := map[string]any{"type": tc.kind, "bbox": box, "lines": []any{line(span("text", "First.", []float64{10, 10, 45, 20})), line(span("text", "Second.", second))}}
			item := map[string]any{"type": tc.kind, "text": "First. Second.", "page_idx": 0, "bbox": []int{100, 100, 450, 400}}
			if tc.kind == "equation" {
				para["type"] = "interline_equation"
				para["lines"] = []any{line(span("interline_equation", "x=1", second))}
				item["text"] = "$$\nx=1\n$$"
			}
			if tc.kind == "image" || tc.kind == "table" {
				delete(para, "lines")
				delete(item, "text")
				children := []any{}
				if tc.kind == "table" {
					html := "<table><tr><td>11</td></tr></table>"
					children = append(children, map[string]any{"type": "table_body", "bbox": box, "lines": []any{line(map[string]any{"type": "table", "html": html, "bbox": box})}})
					item["table_body"] = html
				}
				if !tc.bodyOnly {
					kind := tc.kind + "_footnote"
					if tc.kind == "table" {
						kind = "table_caption"
					}
					children = append(children, map[string]any{"type": kind, "bbox": second, "lines": []any{line(span("text", "Measured by NMR.", second))}})
					item[kind] = []string{"Measured by NMR."}
				}
				para["blocks"] = children
			}
			layout := map[string]any{"_backend": "hybrid", "_version_name": "3.4.4", "pdf_info": []any{map[string]any{"page_idx": 0, "page_size": []int{100, 100}, "preproc_blocks": []any{para}, "para_blocks": []any{para}}}}
			var raw bytes.Buffer
			z := zip.NewWriter(&raw)
			for name, v := range map[string]any{"layout.json": layout, "paper_content_list.json": []any{item}} {
				f, _ := z.Create(name)
				b, _ := json.Marshal(v)
				f.Write(b)
			}
			z.Close()
			base, err := pdfir.AdaptZIP(raw.Bytes(), pdfir.Digest("physical source"), "vlm")
			if err != nil {
				t.Fatal(err)
			}
			before, _ := json.Marshal(base)
			projected, err := pdfir.ProjectSourcePrecision(base, raw.Bytes())
			if err != nil {
				t.Fatal(err)
			}
			s, err := paperguide.BuildSourceSnapshot(projected, paperguide.SourceIdentity{SourceSHA256: base.SourceSHA256, OwnerID: "o", DatasetID: "d", DocumentID: "p"})
			if err != nil {
				t.Fatal(err)
			}
			e := s.Evidence[0]
			if e.Locator.Kind != tc.want || e.Locator.Page != 0 {
				t.Fatalf("full text has misleading locator: %+v", e)
			}
			if tc.want == "page" && !reflect.DeepEqual(e.Locator, paperguide.GuideLocator{Kind: "page", Page: 0}) {
				t.Fatalf("stale precise fields: %+v", e.Locator)
			}
			if e.Text != "" && (tc.kind == "text" || tc.kind == "header") {
				loc, err := paperguide.ResolveTextLocator(s, e.ID, e.Text)
				if err != nil || loc.Kind != tc.want {
					t.Fatalf("text resolver: %+v %v", loc, err)
				}
			}
			if tc.kind == "text" && e.Text != "First. Second." {
				t.Fatal("text changed")
			}
			after, _ := json.Marshal(base)
			if !bytes.Equal(before, after) || !reflect.DeepEqual(base.Blocks, projected.Blocks) {
				t.Fatal("canonical changed")
			}
			if err = paperguide.ValidateSnapshot(s, s.Identity, s.IRDigest); err != nil {
				t.Fatal(err)
			}
		})
	}
}
