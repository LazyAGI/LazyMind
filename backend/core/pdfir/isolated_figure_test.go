package pdfir

import "testing"

// Geometry from the retained 612 × 792 point Mamba page 8 layout. The H3,
// Gated MLP and Mamba panels share a legend, then a wider caption underneath.
func TestIsolatedMambaArchitectureWithWideCaption(t *testing.T) {
	size := []float64{612, 792}
	body := []float64{96, 70, 537, 247}
	caption := []float64{68, 255, 564, 310}
	leaf := func(kind string, box []float64, span precisionSpan) precisionBlock {
		b := precisionBlock{Type: kind, BBox: box}
		b.Lines = append(b.Lines, struct {
			Spans []precisionSpan `json:"spans"`
		}{[]precisionSpan{span}})
		return b
	}
	figure := precisionBlock{Type: "image", BBox: body, Blocks: []precisionBlock{
		leaf("image_body", body, precisionSpan{Type: "image", BBox: body}),
		leaf("image_caption", caption, precisionSpan{Type: "text", BBox: []float64{70, 257, 560, 266}}),
	}}
	item := contentItem{Type: "image", ImageCaptions: []string{"Figure 3: (Architecture.)"}}
	box, err := precisionBox(body, size)
	if err != nil {
		t.Fatal(err)
	}
	block := Block{ID: "mamba-figure-3", Type: "image", Page: 7, BBox: box}
	header := precisionBlock{Type: "page_header", BBox: []float64{30, 10, 500, 30}}
	neighbor := precisionBlock{Type: "image", BBox: []float64{40, 70, 90, 245}}
	continuation := leaf("text", []float64{68, 580, 564, 700}, precisionSpan{
		Type: "text", BBox: []float64{69, 75, 500, 84}, CrossPage: true,
	})
	for _, tc := range []struct {
		name     string
		update   func(*precisionPage)
		wantBand bool
	}{
		{"isolated", func(*precisionPage) {}, true},
		{"continuation on another physical page", func(p *precisionPage) { p.Paras = append(p.Paras, continuation) }, true},
		{"neighboring column", func(p *precisionPage) { p.Preproc = append(p.Preproc, neighbor) }, false},
		{"discarded header", func(p *precisionPage) { p.Discarded = append(p.Discarded, header) }, false},
		{"physical span reaches above caption", func(p *precisionPage) { p.Preproc = append(p.Preproc, continuation) }, false},
		{"missing physical owner", func(p *precisionPage) { p.Preproc = nil }, false},
		{"duplicated physical owner", func(p *precisionPage) { p.Preproc = append(p.Preproc, figure) }, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			page := precisionPage{Size: size, Paras: []precisionBlock{figure}, Preproc: []precisionBlock{figure}}
			tc.update(&page)
			proof := proveOriginalFigure(block, item, figure, page)
			if tc.wantBand {
				if proof.CaptionTop != 255.0/792 || len(proof.Parts) != 1 {
					t.Fatalf("missing isolated band: %+v", proof)
				}
			} else if proof.CaptionTop != 0 || len(proof.Parts) != 0 || proof.Caption == "" {
				t.Fatalf("ambiguous geometry must retain only caption: %+v", proof)
			}
		})
	}
}
