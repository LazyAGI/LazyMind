package pdfir

import "strings"

// OriginalFigureProof belongs to the paper guide catalog. It records each
// proved producer body component rather than reusing an evidence locator.
type OriginalFigureProof struct {
	BlockID string      `json:"block_id"`
	Type    string      `json:"type"`
	Page    int         `json:"page"`
	Parts   [][]float64 `json:"parts,omitempty"`
	Caption string      `json:"caption,omitempty"`
}

func proveOriginalFigure(block Block, item contentItem, parent precisionBlock, size []float64) OriginalFigureProof {
	proof := OriginalFigureProof{BlockID: block.ID, Type: block.Type, Page: block.Page}
	// v3 has already matched all of this canonical item's caption content
	// against the direct children of this parent, independently of projectImages.
	if item.Type == "image" && len(item.ImageCaptions) == 1 {
		proof.Caption = plainTableText(item.ImageCaptions[0])
	}
	if parent.Angle != 0 || len(parent.Lines) != 0 {
		return proof
	}
	group, err := precisionBox(parent.BBox, size)
	if err != nil {
		return proof
	}
	var parts [][]float64
	var ancillary [][]float64
	for _, child := range parent.Blocks {
		box, err := precisionBox(child.BBox, size)
		if err != nil || child.Angle != 0 || len(child.Blocks) != 0 {
			return proof
		}
		if child.Type == item.Type+"_caption" || child.Type == item.Type+"_footnote" {
			ancillary = append(ancillary, box)
			continue
		}
		if child.Type != item.Type+"_body" || !originalBoxContains(group, box) || !precisionContains(block.BBox, box) {
			return proof
		}
		var spans [][]float64
		for _, line := range child.Lines {
			for _, span := range line.Spans {
				bounds, err := precisionBox(span.BBox, size)
				if err != nil || span.CrossPage || span.Type != item.Type || !originalBoxContains(box, bounds) {
					return proof
				}
				spans = append(spans, bounds)
			}
		}
		if !originalBoxContains(originalEnvelope(spans), box) {
			return proof
		}
		parts = append(parts, box)
	}
	crop := originalEnvelope(parts)
	if len(crop) == 0 {
		return proof
	}
	for _, box := range ancillary {
		if crop[0] < box[2] && box[0] < crop[2] && crop[1] < box[3] && box[1] < crop[3] {
			return proof
		}
	}
	// An archive caption with unresolved attribution in the frozen projection
	// leaves the extent uncertain. Keep the page available in that case. An
	// uncaptioned body or a duplicate figure number does not imply truncation.
	if proof.Caption == "" || block.Image == nil || len(block.Image.Captions) > 0 || len(block.Image.Warnings) == 0 {
		proof.Parts = parts
	}
	return proof
}

func originalBoxContains(parent, child []float64) bool {
	return len(parent) == 4 && len(child) == 4 &&
		parent[0] <= child[0] && parent[1] <= child[1] && parent[2] >= child[2] && parent[3] >= child[3]
}

func originalEnvelope(parts [][]float64) []float64 {
	if len(parts) == 0 {
		return nil
	}
	box := append([]float64(nil), parts[0]...)
	for _, part := range parts[1:] {
		box[0], box[1] = min(box[0], part[0]), min(box[1], part[1])
		box[2], box[3] = max(box[2], part[2]), max(box[3], part[3])
	}
	return box
}

func uniqueOriginalFigureProofs(proofs []OriginalFigureProof, blocks []Block) []OriginalFigureProof {
	type figureBox struct {
		page int
		box  [4]float64
	}
	key := func(page int, box []float64) figureBox {
		return figureBox{page, [4]float64{box[0], box[1], box[2], box[3]}}
	}
	// A canonical layout parent or media component cannot prove two originals.
	groups, components := map[figureBox]int{}, map[figureBox]int{}
	blocksByID := map[string]Block{}
	for _, block := range blocks {
		blocksByID[block.ID] = block
		if (block.Type == "image" || block.Type == "chart") && len(block.BBox) == 4 {
			groups[key(block.Page, block.BBox)]++
		}
	}
	captions := map[string]int{}
	for _, proof := range proofs {
		if proof.Caption != "" {
			captions[strings.Join(strings.Fields(proof.Caption), " ")]++
		}
		for _, part := range proof.Parts {
			components[key(proof.Page, part)]++
		}
	}
	var out []OriginalFigureProof
	for _, proof := range proofs {
		box := blocksByID[proof.BlockID].BBox
		if len(box) != 4 || groups[key(proof.Page, box)] != 1 {
			continue
		}
		for _, part := range proof.Parts {
			if components[key(proof.Page, part)] != 1 {
				proof.Parts = nil
				break
			}
		}
		if proof.Caption != "" && captions[strings.Join(strings.Fields(proof.Caption), " ")] != 1 {
			proof.Caption = ""
		}
		if len(proof.Parts) != 0 || proof.Caption != "" {
			out = append(out, proof)
		}
	}
	return out
}
