package paperguide

import (
	"fmt"
	"regexp"
	"strings"

	"lazymind/core/pdfir"
)

var originalEquationTag = regexp.MustCompile(`(?:^|[^\\])\\tag\*?\{([[:alnum:]](?:[[:alnum:].-]{0,30}[[:alnum:]])?)\}[[:space:]]*$`)

// SnapshotFilename anchors new proof bytes in the trusted revision's path.
// This makes tampering detectable even when the original archive is no longer
// available for a historical read. Historical catalog paths remain readable.
func (s GuideSourceSnapshot) SnapshotFilename() string {
	name := s.Identity.SourceSHA256 + "-" + s.IRDigest + "-" + s.CatalogVersion
	if s.CatalogVersion == SourceCatalogVersion {
		name += "-" + digest([]any{s.Identity, s.IRDigest, s.FigureProofs})
	}
	return name + ".json"
}

func originalDisplayBuilder(d pdfir.Document, proofs []pdfir.OriginalFigureProof) (func(pdfir.Block, string, bool) *GuideOriginalDisplay, error) {
	type regionKey struct {
		page int
		kind string
		box  [4]float64
	}
	key := func(b pdfir.Block) regionKey {
		return regionKey{b.Page, b.Type, [4]float64{b.BBox[0], b.BBox[1], b.BBox[2], b.BBox[3]}}
	}
	regions := map[regionKey]int{}
	blocks := map[string]pdfir.Block{}
	for _, b := range d.Blocks {
		blocks[b.ID] = b
		if validBBox(b.BBox) {
			regions[key(b)]++
		}
	}
	hasProof := d.Precision != nil && d.Precision.PageOnlyBlockIDs != nil && d.CoordinateSpace == "normalized_top_left"
	figures := map[string]*GuideOriginalDisplay{}
	for _, proof := range proofs {
		block, ok := blocks[proof.BlockID]
		if !hasProof || !ok || !validBBox(block.BBox) || regions[key(block)] != 1 ||
			figures[proof.BlockID] != nil || proof.Page != block.Page || proof.Type != block.Type ||
			(proof.Type != "image" && proof.Type != "chart") || (len(proof.Parts) == 0 && proof.Caption == "") {
			return nil, fmt.Errorf("%w: invalid original figure proof", ErrInvalidSource)
		}
		display := &GuideOriginalDisplay{Kind: "figure", Page: proof.Page, Caption: proof.Caption}
		for _, part := range proof.Parts {
			if !validBBox(part) || part[0] < block.BBox[0]-.001001 || part[1] < block.BBox[1]-.001001 ||
				part[2] > block.BBox[2]+.001001 || part[3] > block.BBox[3]+.001001 {
				return nil, fmt.Errorf("%w: invalid original figure region", ErrInvalidSource)
			}
			if len(display.BBox) == 0 {
				display.BBox = append([]float64(nil), part...)
			} else {
				box := display.BBox
				box[0], box[1] = min(box[0], part[0]), min(box[1], part[1])
				box[2], box[3] = max(box[2], part[2]), max(box[3], part[3])
			}
		}
		if proof.CaptionTop != 0 {
			band := []float64{0, 0, 1, proof.CaptionTop}
			if !validBBox(band) || proof.CaptionTop >= 1 || proof.Caption == "" || len(display.BBox) != 4 || display.BBox[3] > proof.CaptionTop {
				return nil, fmt.Errorf("%w: invalid isolated figure band", ErrInvalidSource)
			}
			display.BBox = band
		}
		figures[proof.BlockID] = display
	}
	return func(b pdfir.Block, kind string, citationPageOnly bool) *GuideOriginalDisplay {
		display := &GuideOriginalDisplay{Kind: kind, Page: b.Page}
		if kind == "figure" {
			if proved := figures[b.ID]; proved != nil {
				return proved
			}
			return display
		}
		if !hasProof || !validBBox(b.BBox) || regions[key(b)] != 1 || citationPageOnly || strings.TrimSpace(b.Latex) == "" {
			return display
		}
		if m := originalEquationTag.FindStringSubmatch(b.Latex); m != nil {
			display.Number = m[1]
		}
		display.BBox = append([]float64(nil), b.BBox...)
		return display
	}, nil
}
