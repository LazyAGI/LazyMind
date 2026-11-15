package paperguide

import (
	"regexp"
	"strings"

	"lazymind/core/pdfir"
)

var originalEquationTag = regexp.MustCompile(`(?:^|[^\\])\\tag\*?\{([[:alnum:]](?:[[:alnum:].-]{0,30}[[:alnum:]])?)\}[[:space:]]*$`)

// The v3 loader checks the retained ZIP against the base blocks and uniquely
// matches each content item to its own physical page, type and layout bounds.
// The digest binds that immutable IR to the revision. Here we use only those
// canonical bounds; inference, adjacent blocks and model output cannot supply
// geometry. For formula bounds v3's additional full-span containment is needed.
func originalDisplayBuilder(d pdfir.Document) func(pdfir.Block, string, bool) *GuideOriginalDisplay {
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
	return func(b pdfir.Block, kind string, citationPageOnly bool) *GuideOriginalDisplay {
		display := &GuideOriginalDisplay{Kind: kind, Page: b.Page}
		if !hasProof || !validBBox(b.BBox) || regions[key(b)] != 1 {
			return display
		}
		if kind == "formula" {
			if citationPageOnly || strings.TrimSpace(b.Latex) == "" {
				return display
			}
			if m := originalEquationTag.FindStringSubmatch(b.Latex); m != nil {
				display.Number = m[1]
			}
		} else if b.Image != nil {
			// A warning on the producer's image association leaves its extent
			// unresolved (e.g. the body may only be part of a complex figure).
			// Keep the original page available without guessing a larger crop.
			if len(b.Image.Warnings) != 0 {
				return display
			}
			if len(b.Image.Captions) == 1 {
				caption := b.Image.Captions[0]
				matches := 0
				for _, link := range b.Image.RelatedBlocks {
					if related, ok := blocks[link.BlockID]; ok && link.Relation == "caption" && related.Page == b.Page && related.Type == "image_caption" && related.Text == caption {
						matches++
					}
				}
				if matches == 1 {
					display.Caption = caption
				}
			}
		}
		display.BBox = append([]float64(nil), b.BBox...)
		return display
	}
}
