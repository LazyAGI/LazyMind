package pdfir_test

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"lazymind/core/paperguide"
	"lazymind/core/pdfir"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestPrecisionAuditedPhysicalPageTargets(t *testing.T) {
	root := os.Getenv("LAZYMIND_PRECISION_FIXTURES")
	if root == "" {
		t.Skip("offline source-derived excerpts not requested")
	}
	paths, err := filepath.Glob(filepath.Join(root, "*.json"))
	if err != nil || len(paths) != 5 {
		t.Fatal("missing source-derived fixtures")
	}
	for _, path := range paths {
		t.Run(filepath.Base(path), func(t *testing.T) {
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			var fixture struct {
				Hash    string          `json:"source_sha256"`
				Items   json.RawMessage `json:"content_items"`
				Layout  json.RawMessage `json:"layout"`
				Targets []struct {
					Page   int    `json:"physical_page"`
					Phrase string `json:"phrase"`
				} `json:"targets"`
			}
			if json.Unmarshal(data, &fixture) != nil {
				t.Fatal("invalid fixture")
			}
			var raw bytes.Buffer
			w := zip.NewWriter(&raw)
			f, _ := w.Create("source_content_list.json")
			f.Write(fixture.Items)
			f, _ = w.Create("layout.json")
			f.Write(fixture.Layout)
			w.Close()
			base, err := pdfir.AdaptZIP(raw.Bytes(), fixture.Hash, "vlm")
			if err != nil {
				t.Fatal(err)
			}
			d, err := pdfir.ProjectSourcePrecision(base, raw.Bytes())
			if err != nil {
				t.Fatal(err)
			}
			s, err := paperguide.BuildSourceSnapshot(d, paperguide.SourceIdentity{SourceSHA256: fixture.Hash, OwnerID: "offline", DatasetID: "test", DocumentID: "source"})
			if err != nil {
				t.Fatal(err)
			}
			for _, target := range fixture.Targets {
				found := false
				for _, ev := range s.Evidence {
					if !strings.Contains(strings.Join(strings.Fields(ev.Text), " "), target.Phrase) {
						continue
					}
					found = true
					if ev.Locator.Page != target.Page-1 || ev.Locator.Kind != "page" || len(ev.Locator.BBox) != 0 || ev.SourceRegionID == "" {
						t.Fatalf("%q has false location: %+v", target.Phrase, ev.Locator)
					}
					loc, e := paperguide.ResolveTextLocator(s, ev.ID, target.Phrase)
					if e != nil || loc.Page != target.Page-1 || loc.Kind != "page" {
						t.Fatalf("quote lost page: %+v %v", loc, e)
					}
				}
				if !found {
					t.Fatalf("source target missing: %q", target.Phrase)
				}
			}
			// Every span is represented exactly once, and canonical parents still retain
			// their original whole text/starting page for reading and notes.
			if len(s.Assets) != 0 {
				t.Fatal("text regions became assets")
			}
			for i, b := range base.Blocks {
				if b.ID != s.Blocks[i].ID || b.Text != s.Blocks[i].Text || b.Page != s.Blocks[i].Page {
					t.Fatal("canonical parent changed")
				}
			}
		})
	}
}

// Exercises the actual adapter -> precision -> guide catalog path with entirely
// synthetic assets, including every text field consumed by the guide catalog.
func TestPrecisionVerifiedPayloadReachesGuideCatalog(t *testing.T) {
	for _, kind := range []string{"text", "equation", "table", "image"} {
		t.Run(kind, func(t *testing.T) {
			box := []int{10, 10, 90, 80}
			span := func(kind, text string) map[string]any {
				return map[string]any{"type": kind, "content": text, "bbox": box}
			}
			block := func(kind string, s map[string]any) map[string]any {
				return map[string]any{"type": kind, "bbox": box, "lines": []any{map[string]any{"spans": []any{s}}}}
			}
			item := map[string]any{"type": kind, "page_idx": 0, "bbox": []int{100, 100, 900, 800}}
			para := block("text", span("text", "Synthetic proven statement"))
			want := []string{"Synthetic proven statement"}
			switch kind {
			case "text":
				item["text"] = want[0]
			case "equation":
				item["text"] = "$$\nx_{i}^2 \\tag{7}\n$$"
				para = block("interline_equation", span("interline_equation", "x_{i}^2 \\tag{7}"))
				want = []string{"x_{i}^2"}
			case "table", "image":
				label := "Figure 1: Synthetic proven caption"
				if kind == "table" {
					label = "Table 1: Synthetic proven caption"
				}
				item[kind+"_caption"] = []string{label}
				item[kind+"_footnote"] = []string{"Synthetic proven footnote"}
				captionSpan := span("text", label)
				captionSpan["bbox"] = []int{10, 2, 90, 8}
				caption := block(kind+"_caption", captionSpan)
				caption["bbox"] = []int{10, 2, 90, 8}
				children := []any{caption, block(kind+"_footnote", span("text", "Synthetic proven footnote"))}
				want = []string{label, "Synthetic proven footnote"}
				if kind == "table" {
					body := "<table><tr><td>Synthetic proven cell</td><td><eq>x_i</eq></td></tr></table>"
					item["table_body"] = "<table><tr><td>Synthetic proven cell</td><td> $x_i$ </td></tr></table>"
					children = append(children, block("table_body", map[string]any{"type": "table", "html": body, "bbox": box}))
					want = append(want, "Synthetic proven cell", "x_i")
				}
				para = map[string]any{"type": kind, "bbox": box, "blocks": children}
			}
			layout := map[string]any{"_backend": "hybrid", "_version_name": "3.4.4", "pdf_info": []any{map[string]any{"page_idx": 0, "page_size": []int{100, 100}, "para_blocks": []any{para}, "preproc_blocks": []any{para}}}}
			var raw bytes.Buffer
			z := zip.NewWriter(&raw)
			for name, v := range map[string]any{"paper_content_list.json": []any{item}, "layout.json": layout} {
				f, _ := z.Create(name)
				data, _ := json.Marshal(v)
				f.Write(data)
			}
			z.Close()
			base, e := pdfir.AdaptZIP(raw.Bytes(), strings.Repeat("a", 64), "vlm")
			if e != nil {
				t.Fatal(e)
			}
			d, e := pdfir.ProjectSourcePrecision(base, raw.Bytes())
			if e != nil {
				t.Fatal(e)
			}
			s, e := paperguide.BuildSourceSnapshot(d, paperguide.SourceIdentity{SourceSHA256: base.SourceSHA256, OwnerID: "synthetic", DatasetID: "synthetic", DocumentID: "synthetic"})
			if e != nil {
				t.Fatal(e)
			}
			if e := paperguide.ValidateSnapshot(s, s.Identity, s.IRDigest); e != nil {
				t.Fatal(e)
			}
			if kind == "image" || kind == "equation" {
				// Assert on the wire so this regression also catches omission of the optional API data.
				var display struct {
					Original *struct {
						Kind, Caption, Number string
						Page                  int
						BBox                  []float64
					} `json:"original_display"`
				}
				asset := s.Assets[0]
				encoded, _ := json.Marshal(asset)
				if err := json.Unmarshal(encoded, &display); err != nil || display.Original == nil {
					t.Fatalf("original display missing: %s", encoded)
				}
				wantKind, wantNumber, wantCaption := "formula", "7", ""
				if kind == "image" {
					wantKind, wantNumber, wantCaption = "figure", "", "Figure 1: Synthetic proven caption"
					if asset.Locator.Kind != "page" || len(asset.Locator.BBox) != 0 {
						t.Fatal("figure body changed the caption citation")
					}
				}
				o := display.Original
				if o.Kind != wantKind || o.Number != wantNumber || o.Caption != wantCaption || o.Page != 0 || !reflect.DeepEqual(o.BBox, []float64{.1, .1, .9, .8}) {
					t.Fatalf("wrong original location: %+v", o)
				}
				tampered := bytes.Replace(encoded, []byte(`"original_display":{"kind":"`+wantKind+`","page":0`), []byte(`"original_display":{"kind":"`+wantKind+`","page":999`), 1)
				if bytes.Equal(encoded, tampered) {
					t.Fatal("wrong page mutation did not apply")
				}
				if err := json.Unmarshal(tampered, &s.Assets[0]); err != nil {
					t.Fatal(err)
				}
				if err := paperguide.ValidateSnapshot(s, s.Identity, s.IRDigest); err == nil {
					t.Fatal("tampered physical page survived snapshot verification")
				}
			}
			for _, phrase := range want {
				found := false
				for _, ev := range s.Evidence {
					if strings.Contains(ev.Text, phrase) {
						found = true
						if ev.Locator.Page != 0 {
							t.Fatal("payload acquired a foreign physical page")
						}
					}
				}
				if !found {
					t.Fatalf("proven field missing from guide catalog: %q", phrase)
				}
			}
		})
	}
}
