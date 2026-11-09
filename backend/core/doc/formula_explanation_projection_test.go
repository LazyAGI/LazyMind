package doc

import (
	"archive/zip"
	"bytes"
	"fmt"
	"lazymind/core/pdfir"
	"testing"
)

func TestFormulaExplanationContextExcludesProjectedCaptions(t *testing.T) {
	for _, kind := range []string{"table", "image"} {
		t.Run(kind, func(t *testing.T) {
			label := "Table 1. Earlier experiment"
			if kind == "image" {
				label = "Figure 1. Earlier experiment"
			}
			content := fmt.Sprintf(`[
				{"type":%q,"page_idx":0,"bbox":[100,100,900,500],"table_body":"<table><tr><td>Earlier result</td></tr></table>"},
				{"type":"text","page_idx":2,"text":"Let x denote the distance."},
				{"type":"equation","page_idx":2,"text":"x^2"},
				{"type":"text","page_idx":2,"text":"The squared distance is nonnegative."}
			]`, kind)
			layout := fmt.Sprintf(`{"pdf_info":[{"page_idx":0,"page_size":[1000,1000],"para_blocks":[
				{"type":%q,"bbox":[100,510,900,540],"lines":[{"spans":[{"content":%q}]}]}
			]}]}`, kind+"_caption", label)
			var archive bytes.Buffer
			writer := zip.NewWriter(&archive)
			for name, body := range map[string]string{"paper_content_list.json": content, "layout.json": layout} {
				entry, err := writer.Create(name)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := entry.Write([]byte(body)); err != nil {
					t.Fatal(err)
				}
			}
			if err := writer.Close(); err != nil {
				t.Fatal(err)
			}
			d, err := pdfir.AdaptZIP(archive.Bytes(), "source", "vlm")
			if err != nil {
				t.Fatal(err)
			}
			if len(d.Blocks) != 5 || d.Blocks[4].Type != kind+"_caption" {
				t.Fatalf("fixture did not project a caption: %+v", d.Blocks)
			}
			// Captions remain available through their explicit visual association.
			_, visualEvidence, err := paperVisualContext(d, d.Blocks[0].ID)
			if err != nil {
				t.Fatal(err)
			}
			captionFound := false
			for _, item := range visualEvidence {
				captionFound = captionFound || item.ID == d.Blocks[4].ID
			}
			if !captionFound {
				t.Fatal("projected caption missing from visual evidence")
			}
			_, evidence, err := formulaExplanationContext(d, d.Blocks[2].ID)
			if err != nil {
				t.Fatal(err)
			}
			if len(evidence) != 3 {
				t.Fatalf("expected adjacent prose and formula only, got %+v", evidence)
			}
			for i, item := range evidence {
				if item.ID != d.Blocks[i+1].ID {
					t.Fatalf("adjacent source block %d changed: %+v", i, item)
				}
			}
		})
	}
}
