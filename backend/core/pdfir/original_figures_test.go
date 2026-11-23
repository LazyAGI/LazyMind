package pdfir_test

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"lazymind/core/paperguide"
	"lazymind/core/pdfir"
)

func TestOriginalFigureProofUsesCompleteBodyAndOwnCanonicalCaption(t *testing.T) {
	for _, tc := range []struct {
		name                            string
		leftParts                       [][]int
		swapCaptions, duplicateCaptions bool
		wantBox                         []float64
		footnoteSpan                    []int
		leftTitle                       string
		leftCaptionBox                  []int
	}{
		{"two adjacent figures", [][]int{{5, 10, 20, 40}, {20, 10, 40, 40}}, false, false, []float64{.05, .1, .4, .4}, nil, "", nil},
		{"no body", nil, false, false, nil, nil, "", nil},
		{"second panel outside parent", [][]int{{5, 10, 20, 40}, {20, 10, 45, 40}}, false, false, nil, nil, "", nil},
		{"swapped caption geometry", [][]int{{5, 10, 40, 40}}, true, false, []float64{.05, .1, .4, .4}, nil, "", nil},
		{"duplicate caption", [][]int{{5, 10, 40, 40}}, false, true, []float64{.05, .1, .4, .4}, nil, "", nil},
		{"footnote span crosses into the figure", [][]int{{5, 10, 40, 40}}, false, false, nil, []int{8, 35, 30, 38}, "", nil},
		{"footnote span leaves its outer box without overlapping the crop", [][]int{{5, 10, 40, 40}}, false, false, nil, []int{8, 55, 30, 58}, "", nil},
		{"caption naming cannot downgrade complete geometry", [][]int{{5, 10, 40, 40}}, false, false, []float64{.05, .1, .4, .4}, nil, "Abbildung 1: Linkes Exemplar", nil},
		{"wide caption leaves side label extent unresolved", [][]int{{8, 10, 36, 40}}, false, false, nil, nil, "", []int{2, 43, 42, 48}},
		{"small caption overhang preserves a complete body", [][]int{{8, 10, 36, 40}}, false, false, []float64{.08, .1, .36, .4}, nil, "", []int{5, 43, 40, 48}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			bbox := func(x1, y1, x2, y2 int) []int { return []int{x1, y1, x2, y2} }
			leaf := func(kind, spanKind, text string, box []int) map[string]any {
				span := map[string]any{"type": spanKind, "bbox": box}
				if text != "" {
					span["content"] = text
				}
				return map[string]any{"type": kind, "bbox": box, "lines": []any{map[string]any{"spans": []any{span}}}}
			}
			leftBox, rightBox := bbox(5, 10, 40, 40), bbox(55, 10, 90, 40)
			leftCaption, rightCaption := "Figure 1: Left specimen", "Figure 2: Right specimen"
			if tc.leftTitle != "" {
				leftCaption = tc.leftTitle
			}
			if tc.duplicateCaptions {
				rightCaption = leftCaption
			}
			leftCaptionBox, rightCaptionBox := bbox(5, 43, 40, 48), bbox(55, 43, 90, 48)
			if tc.leftCaptionBox != nil {
				leftCaptionBox = tc.leftCaptionBox
			}
			if tc.swapCaptions {
				leftCaptionBox, rightCaptionBox = rightCaptionBox, leftCaptionBox
			}
			image := func(box, captionBox []int, caption string, parts [][]int) (map[string]any, map[string]any) {
				children := []any{leaf("image_caption", "text", caption, captionBox)}
				for _, part := range parts {
					children = append(children, leaf("image_body", "image", "", part))
				}
				// A nearby footnote is explicitly ancillary and excluded from the body crop.
				noteBox := bbox(box[0], 50, box[2], 54)
				spanBox := noteBox
				if caption == leftCaption && len(tc.footnoteSpan) > 0 {
					spanBox = tc.footnoteSpan
				}
				note := leaf("image_footnote", "text", "Physical footnote", spanBox)
				note["bbox"] = noteBox
				children = append(children, note)
				scaled := []int{box[0] * 10, box[1] * 10, box[2] * 10, box[3] * 10}
				return map[string]any{"type": "image", "page_idx": 0, "bbox": scaled, "image_caption": []string{caption}, "image_footnote": []string{"Physical footnote"}},
					map[string]any{"type": "image", "bbox": box, "blocks": children}
			}
			a, p := image(leftBox, leftCaptionBox, leftCaption, tc.leftParts)
			b, q := image(rightBox, rightCaptionBox, rightCaption, [][]int{rightBox})
			layout := map[string]any{"_backend": "hybrid", "_version_name": "3.4.4", "pdf_info": []any{map[string]any{
				"page_idx": 0, "page_size": []int{100, 100}, "para_blocks": []any{p, q}, "preproc_blocks": []any{p, q},
			}}}
			var raw bytes.Buffer
			z := zip.NewWriter(&raw)
			for name, v := range map[string]any{"source_content_list.json": []any{a, b}, "layout.json": layout} {
				f, err := z.Create(name)
				if err != nil {
					t.Fatal(err)
				}
				if err = json.NewEncoder(f).Encode(v); err != nil {
					t.Fatal(err)
				}
			}
			if err := z.Close(); err != nil {
				t.Fatal(err)
			}
			base, err := pdfir.AdaptZIP(raw.Bytes(), strings.Repeat("a", 64), "vlm")
			if err != nil {
				t.Fatal(err)
			}
			v3, err := pdfir.ProjectSourcePrecision(base, raw.Bytes())
			if err != nil {
				t.Fatal(err)
			}
			d, proofs, err := pdfir.ProjectGuideSourcePrecision(base, raw.Bytes())
			if err != nil || !reflect.DeepEqual(d, v3) {
				t.Fatalf("guide figure proof changed v3: %v", err)
			}
			id := paperguide.SourceIdentity{SourceSHA256: d.SourceSHA256, DatasetID: "ds", DocumentID: "paper", OwnerID: "o"}
			s, err := paperguide.BuildSourceSnapshotWithFigureProofs(d, id, proofs)
			if err != nil {
				t.Fatal(err)
			}
			left, right := s.Assets[0].OriginalDisplay, s.Assets[1].OriginalDisplay
			if tc.swapCaptions && !strings.Contains(s.Evidence[0].Text, rightCaption) {
				t.Fatal("fixture did not reproduce the legacy proximity swap")
			}
			if tc.duplicateCaptions {
				leftCaption, rightCaption = "", ""
			}
			if !reflect.DeepEqual(left.BBox, tc.wantBox) || left.Caption != leftCaption || right.Caption != rightCaption {
				t.Fatalf("wrong displays: left=%+v right=%+v", left, right)
			}
			if !reflect.DeepEqual(right.BBox, []float64{.55, .1, .9, .4}) {
				t.Fatalf("adjacent figure lost its own body: %+v", right)
			}
			if s.Evidence[0].Locator.Kind != "page" {
				t.Fatal("proof moved an existing citation")
			}
			if err = paperguide.ValidateSnapshot(s, id, s.IRDigest); err != nil {
				t.Fatal(err)
			}
			s.Assets[0].OriginalDisplay = right
			if err = paperguide.ValidateSnapshot(s, id, s.IRDigest); err == nil {
				t.Fatal("swapped display accepted")
			}
			if len(proofs) > 1 {
				s.Assets[0].OriginalDisplay = left
				s.FigureProofs[0].BlockID = s.FigureProofs[1].BlockID
				if err = paperguide.ValidateSnapshot(s, id, s.IRDigest); err == nil {
					t.Fatal("reassigned archive proof accepted")
				}
			}
		})
	}
}
