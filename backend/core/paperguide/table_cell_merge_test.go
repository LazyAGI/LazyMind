package paperguide

import (
	"encoding/json"
	"strings"
	"testing"

	"lazymind/core/pdfir"
)

func mergedBoundaryDocument(t *testing.T) (pdfir.Document, SourceIdentity) {
	t.Helper()
	d, id := tableFragmentDocument(t)
	start, later := &d.Precision.Regions[0], &d.Precision.Regions[1]
	for i, body := range []string{
		"<table><tr><td>north </td><td>11</td></tr></table>",
		"<table><tr><td>east</td><td></td></tr><tr><td>later</td><td>22</td></tr></table>",
	} {
		r := &d.Precision.Regions[i]
		rows, text, err := pdfir.ParseSourceTable(body)
		if err != nil {
			t.Fatal(err)
		}
		r.Table.HTML, r.Table.Rows, r.Text = body, rows, text
	}
	later.Table.CellMerge = []int{1, 1}
	for i := range d.Blocks {
		if d.Blocks[i].ID == later.Table.ContinuationBlockID {
			d.Blocks[i].BBox = append([]float64{}, later.Table.BBox...)
		}
	}
	parent := &d.Blocks[len(d.Blocks)-1]
	parent.Table.Rows = []pdfir.TableRow{{Cells: append([]pdfir.TableCell{}, start.Table.Rows[0].Cells...)}, later.Table.Rows[1]}
	parent.Table.Rows[0].Cells[0].Text = "north east"
	last := len(d.Blocks) - 1
	d.Blocks[last-1], d.Blocks[last] = d.Blocks[last], d.Blocks[last-1]
	pageOnly := []string{}
	d.Precision.PageOnlyBlockIDs = &pageOnly
	return d, id
}

func TestCellMergeSnapshotRebuildsPhysicalEvidence(t *testing.T) {
	d, id := mergedBoundaryDocument(t)
	s, err := BuildSourceSnapshot(d, id)
	if err != nil {
		t.Fatal(err)
	}
	b, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	var frozen GuideSourceSnapshot
	if err := json.Unmarshal(b, &frozen); err != nil {
		t.Fatal(err)
	}
	if err := ValidateSnapshot(frozen, id, s.IRDigest); err != nil {
		t.Fatal(err)
	}
	var parent, later GuideEvidence
	for _, ev := range frozen.Evidence {
		if ev.SourceBlockID == "continuation" {
			t.Fatal("deleted placeholder listed as a table")
		}
		if ev.SourceBlockID != "physical-parent" {
			continue
		}
		if ev.SourceRegionID == "" {
			parent = ev
		} else if ev.Locator.Page == 1 {
			later = ev
		}
	}
	if parent.ID == "" || parent.Locator.Kind != "page" || parent.Locator.Page != 0 || len(parent.Locator.BBox) != 0 ||
		later.ID == "" || later.Locator.Kind != "page" || later.Text != "east | \nlater | 22" || later.ParentAssetID != parent.ID {
		t.Fatalf("wrong parent or later physical evidence: %+v %+v", parent, later)
	}
	packet, err := RestrictEvidence(frozen, []string{later.ID})
	if err != nil || len(packet.Evidence) != 3 {
		t.Fatalf("continuation lost table evidence: got %d entries, err=%v", len(packet.Evidence), err)
	}
	for _, asset := range packet.Assets {
		if asset.ID == parent.ID && len(asset.FragmentEvidenceIDs) == 2 {
			return
		}
	}
	t.Fatal("continuation lost the parent asset or its fragment references")
}

func TestCellMergeSnapshotRejectsAlteredProof(t *testing.T) {
	tests := []struct {
		name, reason string
		alter        func(d *pdfir.Document)
	}{
		{"removed marker", "canonical table cells", func(d *pdfir.Document) {
			d.Precision.Regions[1].Table.CellMerge = nil
		}},
		{"tampered marker", "cell merge markers", func(d *pdfir.Document) {
			d.Precision.Regions[1].Table.CellMerge[1] = 0
		}},
		{"marker on home", "start fragment", func(d *pdfir.Document) {
			d.Precision.Regions[0].Table.CellMerge = []int{1, 1}
		}},
		{"wrong continuation ID", "continuation placeholder", func(d *pdfir.Document) {
			d.Precision.Regions[1].Table.ContinuationBlockID = "unrelated"
		}},
		{"repeated continuation ID", "duplicate continuation", func(d *pdfir.Document) {
			dup := d.Precision.Regions[1]
			dup.ID = "duplicate-region"
			d.Precision.Regions = append(d.Precision.Regions, dup)
		}},
		{"forged physical box", "cell merge fragment geometry", func(d *pdfir.Document) {
			d.Precision.Regions[1].Table.BBox = []float64{.01, .01, .09, .09}
		}},
		{"forged frozen material", "table fragment rendering", func(d *pdfir.Document) {
			d.Precision.Regions[1].Table.Rows[0].Cells[0].Text = "forged"
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d, id := mergedBoundaryDocument(t)
			tt.alter(&d)
			if _, err := BuildSourceSnapshot(d, id); err == nil || !strings.Contains(err.Error(), tt.reason) {
				t.Fatalf("want %q rejection, got %v", tt.reason, err)
			}
		})
	}
}

func TestCellMergeSnapshotRejectsChangedSerializedMarker(t *testing.T) {
	d, id := mergedBoundaryDocument(t)
	s, err := BuildSourceSnapshot(d, id)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	altered := strings.Replace(string(encoded), `"cell_merge":[1,1]`, `"cell_merge":[1,0]`, 1)
	if altered == string(encoded) {
		t.Fatal("snapshot did not retain a cell merge marker")
	}
	var frozen GuideSourceSnapshot
	if err := json.Unmarshal([]byte(altered), &frozen); err != nil {
		t.Fatal(err)
	}
	if ValidateSnapshot(frozen, id, s.IRDigest) == nil {
		t.Fatal("altered serialized marker accepted")
	}
}
